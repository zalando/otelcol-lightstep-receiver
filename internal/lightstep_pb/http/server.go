package http

import (
	"context"
	"errors"
	"fmt"
	"github.com/golang/protobuf/proto" //nolint:staticcheck
	"github.com/gorilla/mux"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	lightstepCommon "github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_common"
	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_pb"
	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_pb/collectorpb"
	"github.com/zalando/otelcol-lightstep-receiver/internal/telemetry"
)

const (
	transport = "pbhttp"
)

// ServerHTTP represents the PbGrpc server components satifsying Receiver interface
type ServerHTTP struct {
	config    *confighttp.ServerConfig
	server    *http.Server
	settings  *receiver.Settings
	obsreport *receiverhelper.ObsReport

	nextTraces consumer.Traces
	telemetry  *telemetry.Telemetry

	shutdownWG sync.WaitGroup
}

func NewServer(
	config *confighttp.ServerConfig,
	set *receiver.Settings,
	nextTraces consumer.Traces,
	obsreport *receiverhelper.ObsReport,
	telemetry *telemetry.Telemetry,
) *ServerHTTP {
	return &ServerHTTP{
		config:     config,
		settings:   set,
		obsreport:  obsreport,
		nextTraces: nextTraces,
		telemetry:  telemetry,
	}
}

// Start starts the http pb server
func (s *ServerHTTP) Start(ctx context.Context, host component.Host) error {
	var (
		ln  net.Listener
		err error
	)

	ln, err = s.config.ToListener(ctx)
	if err != nil {
		return fmt.Errorf("can't init http pb server: %s", err)
	}

	rt := mux.NewRouter()
	rt.HandleFunc("/api/v2/reports", s.HandleRequest).Methods(http.MethodPost)

	s.server, err = s.config.ToServer(ctx, host, s.settings.TelemetrySettings, rt)
	if err != nil {
		return fmt.Errorf("can't start http pb server %s", err)
	}

	s.shutdownWG.Add(1)
	go func() {
		defer s.shutdownWG.Done()

		if errHTTP := s.server.Serve(ln); !errors.Is(errHTTP, http.ErrServerClosed) && errHTTP != nil {
			componentstatus.ReportStatus(host, componentstatus.NewFatalErrorEvent(errHTTP))
		}
	}()
	s.telemetry.Logger.Info("started http pb listener",
		zap.String("address", ln.Addr().String()),
	)

	return nil
}

// Shutdown stops http pb server
func (s *ServerHTTP) Shutdown(ctx context.Context) {
	if s.server != nil {
		err := s.server.Shutdown(ctx)
		if err != nil {
			s.telemetry.Logger.Error("failed to stop http pb server", zap.Error(err))
		}
		s.shutdownWG.Wait()
	}
}

func (s *ServerHTTP) writeResponse(w http.ResponseWriter, receiveTimestamp time.Time, err error) {
	resp := collectorpb.ReportResponse{
		Errors:            nil,
		ReceiveTimestamp:  timestamppb.New(receiveTimestamp),
		TransmitTimestamp: timestamppb.Now(),
	}

	switch err != nil {
	case true:
		w.WriteHeader(http.StatusBadRequest)
		resp.Errors = []string{err.Error()}
	default:
		w.WriteHeader(http.StatusOK)
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	encoded, _ := proto.Marshal(&resp)
	_, _ = w.Write(encoded)
}

// HandleRequest is a handler for http calls
func (s *ServerHTTP) HandleRequest(w http.ResponseWriter, rq *http.Request) {
	var (
		err           error
		projectTraces *lightstepCommon.ProjectTraces
		spanCount     int
	)
	ctx := client.NewContext(rq.Context(), client.Info{})
	receiveTimestamp := time.Now()
	ctx = s.obsreport.StartTracesOp(ctx)

	bodyBytes, err := io.ReadAll(rq.Body)
	s.telemetry.Logger.Debug("pb http message received", zap.Int("len", len(bodyBytes)))
	if err != nil {
		s.writeResponse(w, receiveTimestamp, err)
		return
	}

	msg := &collectorpb.ReportRequest{}
	err = proto.Unmarshal(bodyBytes, msg)
	if err != nil {
		s.telemetry.Logger.Debug("can't unmarshal pb http message", zap.Error(err))
		s.writeResponse(w, receiveTimestamp, err)
		return
	}

	spanCount = len(msg.Spans)

	lr := lightstep_pb.NewLightstepRequest(msg, s.telemetry, transport)
	if projectTraces, err = lr.ToOtel(ctx); err != nil {
		s.telemetry.IncrementFailed(transport, 1)
		s.writeResponse(w, receiveTimestamp, err)
		return
	}

	s.telemetry.IncrementProcessed(transport, 1)
	s.telemetry.IncrementClientDropSpans(projectTraces.ServiceName, projectTraces.ClientSpansDropped)

	clientInfo := client.FromContext(context.Background())
	clientInfo.Metadata = client.NewMetadata(map[string][]string{"lightstep-access-token": {projectTraces.AccessToken}})
	ctx = client.NewContext(ctx, clientInfo)

	err = s.nextTraces.ConsumeTraces(ctx, projectTraces.Traces)
	s.writeResponse(w, receiveTimestamp, err)
	s.obsreport.EndTracesOp(ctx, "protobuf-http", spanCount, err)
}
