package grpc

import (
	"context"
	"errors"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/config/configgrpc"
	"sync"

	lightstepCommon "github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_common"
	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_pb"
	pb "github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_pb/collectorpb"
	"github.com/zalando/otelcol-lightstep-receiver/internal/telemetry"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"net"
)

const transport = "pbgrpc"

// ServerGRPC represents the PbGrpc server components satifsying Receiver interface
type ServerGRPC struct {
	config *configgrpc.ServerConfig
	*grpc.Server
	logger    *zap.Logger
	settings  *receiver.Settings
	obsreport *receiverhelper.ObsReport

	nextTraces consumer.Traces
	telemetry  *telemetry.Telemetry

	shutdownWG sync.WaitGroup
}

func NewServer(config *configgrpc.ServerConfig, set *receiver.Settings, logger *zap.Logger, nextTraces consumer.Traces, obsreport *receiverhelper.ObsReport, telemetry *telemetry.Telemetry) *ServerGRPC {
	return &ServerGRPC{
		config:     config,
		settings:   set,
		logger:     logger,
		nextTraces: nextTraces,
		obsreport:  obsreport,
		telemetry:  telemetry,
	}
}

// Start starts the server
func (s *ServerGRPC) Start(host component.Host) error {
	var (
		listener net.Listener
		err      error
	)
	if listener, err = s.config.NetAddr.Listen(context.Background()); err != nil {
		return err
	}

	if s.Server, err = s.config.ToServer(context.Background(), host, s.settings.TelemetrySettings); err != nil {
		return err
	}
	pb.RegisterCollectorServiceServer(s.Server, s)

	s.shutdownWG.Add(1)
	go func() {
		defer s.shutdownWG.Done()

		if errGrpc := s.Server.Serve(listener); errGrpc != nil && !errors.Is(errGrpc, grpc.ErrServerStopped) {
			componentstatus.ReportStatus(host, componentstatus.NewFatalErrorEvent(errGrpc))
		}
	}()
	return nil
}

// Shutdown shuts server down
func (s *ServerGRPC) Shutdown() {
	if s.Server != nil {
		s.Server.GracefulStop()
	}

	s.shutdownWG.Wait()
}

// Report listens to incoming PbGrpc calls
func (s *ServerGRPC) Report(ctx context.Context, rq *pb.ReportRequest) (*pb.ReportResponse, error) {
	var (
		err           error
		projectTraces *lightstepCommon.ProjectTraces
		spanCount     int
	)
	ctx = client.NewContext(ctx, client.Info{})

	ctx = s.obsreport.StartTracesOp(ctx)
	spanCount = len(rq.Spans)
	s.logger.Debug("report", zap.Any("incoming", rq))
	lr := lightstep_pb.NewLightstepRequest(rq, s.telemetry, transport)
	if projectTraces, err = lr.ToOtel(ctx); err != nil {
		s.telemetry.IncrementFailed(transport, 1)
		return nil, err
	}
	s.telemetry.IncrementProcessed(transport, 1)
	s.telemetry.IncrementClientDropSpans(projectTraces.ServiceName, projectTraces.ClientSpansDropped)

	s.logger.Debug("report", zap.Any("outgoing", projectTraces))

	clientInfo := client.FromContext(context.Background())
	clientInfo.Metadata = client.NewMetadata(map[string][]string{"lightstep-access-token": {projectTraces.AccessToken}})
	ctx = client.NewContext(ctx, clientInfo)

	err = s.nextTraces.ConsumeTraces(ctx, projectTraces.Traces)
	s.obsreport.EndTracesOp(ctx, "protobuf-grpc", spanCount, err)

	return &pb.ReportResponse{Errors: nil}, err
}
