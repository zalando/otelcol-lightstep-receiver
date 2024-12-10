package lightstep_common

import (
	"errors"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

var (
	// ErrNoAccessToken happens when there's no Access Token supplied in the payload
	ErrNoAccessToken = errors.New("missing auth token")
	// ErrNoServiceName happens when there's no lightstep.component_name label supplied, which is used as service name
	ErrNoServiceName = errors.New("missing service.name (lightstep.component_name)")
	// ErrNonUTF8Attribute happens if there's an attribute containing non UTF8 string
	ErrNonUTF8Attribute = errors.New("attribute is not UTF8 string")
)

// ProjectTraces contains Traces in Otel format and access token
type ProjectTraces struct {
	AccessToken        string
	ServiceName        string
	ClientSpansDropped int64
	ptrace.Traces
}

type OtelTransformer interface {
	ToOtel() (*ProjectTraces, error)
}
