package lightstepreceiver

import (
	"context"
	"fmt"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/lightstep_pb/grpc"
	"github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/lightstep_pb/http"
	"github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/lightstep_thrift"
	"github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/metadata"
	"github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/telemetry"
)

type lightstepReceiver struct {
	cfg *Config

	serverGRPC   *grpc.ServerGRPC
	serverPbHTTP *http.ServerHTTP
	serverThrift *lightstep_thrift.ThriftServer

	nextTraces consumer.Traces

	logger   *zap.Logger
	settings *receiver.Settings
	tracer   trace.Tracer

	obsrepGRPC   *receiverhelper.ObsReport
	obsrepPbHTTP *receiverhelper.ObsReport
	obsrepThrift *receiverhelper.ObsReport

	telemetry *telemetry.Telemetry
}

func (r *lightstepReceiver) Start(ctx context.Context, host component.Host) error {
	r.logger.Info("starting servers")
	var err error

	if r.serverGRPC != nil {
		if err = r.serverGRPC.Start(host); err != nil {
			r.telemetry.Logger.Error("can't start grpc server", zap.Error(err))
			return fmt.Errorf("can't start grpc server %s", err)
		}
	}

	if r.serverPbHTTP != nil {
		if err = r.serverPbHTTP.Start(ctx, host); err != nil {
			r.telemetry.Logger.Error("can't start pb http server", zap.Error(err))
			return fmt.Errorf("can't start pb http server %s", err)
		}
	}

	if r.serverThrift != nil {
		if err = r.serverThrift.Start(ctx, host); err != nil {
			r.telemetry.Logger.Error("can't start thrift server", zap.Error(err))
			return fmt.Errorf("can't start thrift server %s", err)
		}
	}

	r.logger.Info("servers started")
	return nil
}

func (r *lightstepReceiver) Shutdown(ctx context.Context) error {
	var errs error
	r.logger.Info("shutting down server")

	if r.serverGRPC != nil {
		r.serverGRPC.Shutdown()
	}

	if r.serverPbHTTP != nil {
		r.serverPbHTTP.Shutdown(ctx)
	}

	if r.serverThrift != nil {
		r.serverThrift.Shutdown(ctx)
	}

	return errs
}

func newLightstepReceiver(cfg *Config, set *receiver.Settings, nextTraces consumer.Traces) (*lightstepReceiver, error) {
	var err error

	r := &lightstepReceiver{
		cfg:          cfg,
		serverGRPC:   nil,
		serverPbHTTP: nil,
		serverThrift: nil,
		nextTraces:   nextTraces,
		settings:     set,
		logger:       nil,
		tracer:       set.TracerProvider.Tracer(metadata.Type.String()),
	}

	r.logger = set.Logger

	r.telemetry = &telemetry.Telemetry{Name: metadata.Type.String()}
	r.telemetry.Init(*set)

	r.telemetry.Logger.Debug("config", zap.Any("config", cfg))
	if cfg.PbGrpc != nil {
		r.obsrepGRPC, err = receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
			ReceiverID:             set.ID,
			Transport:              "pbgrpc",
			ReceiverCreateSettings: *set,
		})
		if err != nil {
			return nil, fmt.Errorf("can't init telemetry: %s", err)
		}
		r.serverGRPC = grpc.NewServer(cfg.PbGrpc, set, r.logger, nextTraces, r.obsrepGRPC, r.telemetry)
	}

	if cfg.PbHTTP != nil {
		r.obsrepPbHTTP, err = receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
			ReceiverID:             set.ID,
			Transport:              "pbhttp",
			ReceiverCreateSettings: *set,
		})
		if err != nil {
			return nil, fmt.Errorf("can't init telemetry: %s", err)
		}
		r.serverPbHTTP = http.NewServer(cfg.PbHTTP, set, nextTraces, r.obsrepPbHTTP, r.telemetry)
	}

	if cfg.Thrift != nil {
		r.obsrepThrift, err = receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
			ReceiverID:             set.ID,
			Transport:              "thrift",
			ReceiverCreateSettings: *set,
		})
		if err != nil {
			return nil, fmt.Errorf("can't init telemetry: %s", err)
		}
		r.serverThrift = lightstep_thrift.NewServer(cfg.Thrift, set, nextTraces, r.obsrepThrift, r.telemetry)
	}

	return r, nil
}
