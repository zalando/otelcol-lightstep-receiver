package lightstep_thrift

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
	lightstepConstants "github.com/lightstep/lightstep-tracer-go/constants"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.uber.org/zap"

	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_thrift/thrift_0_9_2/lib/go/thrift"

	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_thrift/collectorthrift"
	"github.com/zalando/otelcol-lightstep-receiver/internal/telemetry"
)

const transport = "thrift"

// ThriftServer defines the http server handling thrift binary requests
type ThriftServer struct {
	config *confighttp.ServerConfig
	server *http.Server

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
) *ThriftServer {
	return &ThriftServer{
		config:     config,
		settings:   set,
		obsreport:  obsreport,
		nextTraces: nextTraces,
		telemetry:  telemetry,
	}
}

const (
	contentTypeApplicationXThrift = "application/x-thrift"
	contentTypeApplicationJson    = "application/json"
	headerLightstepAccessToken    = "Lightstep-Access-Token"
)

// Start starts the http thrift server
func (ts *ThriftServer) Start(ctx context.Context, host component.Host) error {
	var (
		ln  net.Listener
		err error
	)

	ln, err = ts.config.ToListener(ctx)
	if err != nil {
		return fmt.Errorf("can't init thrift server: %s", err)
	}

	rt := mux.NewRouter()
	rt.HandleFunc("/_rpc/v1/reports/binary", ts.HandleThriftBinaryRequest).Methods(http.MethodPost)
	rt.HandleFunc("/api/v0/reports", ts.HandleThriftJSONRequestV0).Methods(http.MethodPost)

	ts.server, err = ts.config.ToServer(ctx, host, ts.settings.TelemetrySettings, rt)
	if err != nil {
		return fmt.Errorf("can't start thrift http server %s", err)
	}

	ts.shutdownWG.Add(1)
	go func() {
		defer ts.shutdownWG.Done()

		if errHTTP := ts.server.Serve(ln); !errors.Is(errHTTP, http.ErrServerClosed) && errHTTP != nil {
			componentstatus.ReportStatus(host, componentstatus.NewFatalErrorEvent(errHTTP))
		}
	}()
	ts.telemetry.Logger.Info("started thrift listener",
		zap.String("address", ln.Addr().String()),
	)

	return nil
}

// Shutdown stops http thrift server
func (ts *ThriftServer) Shutdown(ctx context.Context) {
	if ts.server != nil {
		err := ts.server.Shutdown(ctx)
		if err != nil {
			ts.telemetry.Logger.Error("failed to stop thrift server", zap.Error(err))
		}
		ts.shutdownWG.Wait()
	}
}

func (ts *ThriftServer) writeThriftBinaryResponse(err error, w http.ResponseWriter, data *thrift.TMemoryBuffer) {
	switch err != nil {
	case true:
		w.WriteHeader(http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusOK)
	}

	w.Header().Set("Content-Type", contentTypeApplicationXThrift)
	_, _ = data.WriteTo(w)
}

func (ts *ThriftServer) writeJsonResponse(err error, w http.ResponseWriter, resp *collectorthrift.ReportResponse) {
	switch err != nil {
	case true:
		w.WriteHeader(http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusOK)
	}

	w.Header().Set("Content-Type", contentTypeApplicationJson)
	dt, _ := json.Marshal(resp)
	_, _ = w.Write(dt)
}

// HandleThriftBinaryRequest is a handler for http thrift binary calls
func (ts *ThriftServer) HandleThriftBinaryRequest(w http.ResponseWriter, rq *http.Request) {
	ctx := client.NewContext(rq.Context(), client.Info{
		Metadata: client.NewMetadata(
			map[string][]string{
				"format": {"thrift-binary"},
			},
		),
	})

	bodyBytes, err := io.ReadAll(rq.Body)
	ts.telemetry.Logger.Debug("thrift binary message received", zap.Int("len", len(bodyBytes)))

	transp := thrift.NewTMemoryBuffer()
	oprot := thrift.NewTBinaryProtocolTransport(transp)

	tsr := &ThriftServerReportRequest{
		context:    ctx,
		obsreport:  ts.obsreport,
		nextTraces: ts.nextTraces,
		telemetry:  ts.telemetry,
	}

	if err != nil {
		resp := tsr.newReportResponse(err)
		_ = resp.Write(oprot)
		ts.writeThriftBinaryResponse(err, w, transp)
		return
	}

	iprot := thrift.NewTBinaryProtocolTransport(
		&thrift.TMemoryBuffer{
			Buffer: bytes.NewBuffer(bodyBytes),
		},
	)

	processor := collectorthrift.NewReportingServiceProcessor(tsr)
	_, err = processor.Process(iprot, oprot)
	ts.writeThriftBinaryResponse(err, w, transp)
}

// HandleThriftJSONRequestV0 is a handler for http thrift json calls at /api/v0/reports
func (ts *ThriftServer) HandleThriftJSONRequestV0(w http.ResponseWriter, rq *http.Request) {
	var (
		err         error
		reader      io.ReadCloser
		accessToken string
	)

	ctx := client.NewContext(rq.Context(), client.Info{
		Metadata: client.NewMetadata(
			map[string][]string{
				"format": {"thrift-json"},
			},
		),
	})

	tsr := &ThriftServerReportRequest{
		context:    ctx,
		obsreport:  ts.obsreport,
		nextTraces: ts.nextTraces,
		telemetry:  ts.telemetry,
	}

	switch rq.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(rq.Body)
		if err != nil {
			ts.telemetry.Logger.Error("can't read compressed request", zap.Error(err))
		}
		defer func(reader io.ReadCloser) {
			_ = reader.Close()
		}(reader)
	default:
		reader = rq.Body
	}

	bodyBytes, err := io.ReadAll(reader)
	ts.telemetry.Logger.Debug("thrift json message received",
		zap.Int("len", len(bodyBytes)),
		zap.Any("headers", rq.Header),
		zap.String("body", base64.StdEncoding.EncodeToString(bodyBytes)),
		zap.Error(err),
	)

	rr := collectorthrift.ReportRequest{}

	err = json.Unmarshal(bodyBytes, &rr)
	if err != nil {
		ts.telemetry.Logger.Debug("failed to parse thrift json message",
			zap.Error(err),
		)
		resp := tsr.newReportResponse(err)
		ts.writeJsonResponse(err, w, resp)
		return
	}

	// a trick for V0 format:
	//   service.name <- runtime.group_name
	//   access token <- header Lightstep-Access-Token
	accessToken = rq.Header.Get(headerLightstepAccessToken)
	if rr.Runtime != nil && rr.Runtime.GroupName != nil {
		rr.Runtime.Attrs = append(
			rr.Runtime.Attrs,
			&collectorthrift.KeyValue{
				Key:   lightstepConstants.ComponentNameKey,
				Value: *rr.Runtime.GroupName,
			},
		)
	}

	resp, err := tsr.Report(
		&collectorthrift.Auth{
			AccessToken: &accessToken,
		},
		&rr,
	)

	ts.writeJsonResponse(err, w, resp)
}
