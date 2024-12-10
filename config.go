package lightstepreceiver

import (
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/collector/config/confighttp"
)

// Config represents Lightstep receiver configuration, follows the OTLP stype
type Config struct {
	Protocols `mapstructure:"protocols"`
}

// Protocols represents supported protocols
type Protocols struct {
	PbGrpc *configgrpc.ServerConfig `mapstructure:"pbgrpc"`
	PbHTTP *confighttp.ServerConfig `mapstructure:"pbhttp"`
	Thrift *confighttp.ServerConfig `mapstructure:"thrift"`
}
