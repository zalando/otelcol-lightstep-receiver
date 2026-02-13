package lightstepreceiver

import (
	"context"
	"fmt"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"

	"github.com/zalando/otelcol-lightstep-receiver/internal/metadata"
)

const (
	pbGrpcPort = 4317
	pbHTTPPort = 4327
	thriftPort = 4417
)

// NewFactory creates a factory for Lightstep trace receiver
func NewFactory() receiver.Factory {
	cfgType, _ := component.NewType(metadata.Type.String())
	return receiver.NewFactory(
		cfgType,
		createDefaultConfig,
		receiver.WithTraces(createLightstepReceiver, component.StabilityLevelDevelopment),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		Protocols: Protocols{
			PbGrpc: &configgrpc.ServerConfig{
				NetAddr: confignet.AddrConfig{
					Endpoint:  fmt.Sprintf("0.0.0.0:%d", pbGrpcPort),
					Transport: confignet.TransportTypeTCP,
				},
				ReadBufferSize: 512 * 1024,
			},
			PbHTTP: &confighttp.ServerConfig{
				NetAddr: confignet.AddrConfig{
					Endpoint:  fmt.Sprintf("0.0.0.0:%d", pbHTTPPort),
					Transport: confignet.TransportTypeTCP,
				},
			},
			Thrift: &confighttp.ServerConfig{
				NetAddr: confignet.AddrConfig{
					Endpoint:  fmt.Sprintf("0.0.0.0:%d", thriftPort),
					Transport: confignet.TransportTypeTCP,
				},
			},
		},
	}
}

func createLightstepReceiver(ctx context.Context, set receiver.Settings, config component.Config, nextConsumer consumer.Traces) (receiver.Traces, error) {
	cfg := config.(*Config)

	return newLightstepReceiver(cfg, &set, nextConsumer)
}
