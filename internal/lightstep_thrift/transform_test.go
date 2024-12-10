package lightstep_thrift

import (
	"github.com/matryer/is"
	"go.opentelemetry.io/collector/receiver"
	"testing"

	"github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/telemetry"
)

func initTr() *Request {
	res := &Request{}
	res.telemetry = &telemetry.Telemetry{}
	res.telemetry.Init(receiver.Settings{})
	return res
}

func TestConvertSpanID(t *testing.T) {
	is := is.New(t)
	tr := initTr()
	val := tr.convertSpanID("1c5994087c3bf8be")
	is.Equal(val.String(), "1c5994087c3bf8be")
}

func TestConvertSpanID_Odd(t *testing.T) {
	is := is.New(t)
	tr := initTr()
	val := tr.convertSpanID("c5994087c3bf8be")
	is.Equal(val.String(), "0c5994087c3bf8be")
}

func TestConvertTraceID(t *testing.T) {
	is := is.New(t)
	tr := initTr()
	val := tr.convertTraceID("1c5994087c3bf8be")
	is.Equal(val.String(), "00000000000000001c5994087c3bf8be")
}

func TestConvertTraceID_Odd(t *testing.T) {
	is := is.New(t)
	tr := initTr()
	val := tr.convertTraceID("994087c3bf8be")
	is.Equal(val.String(), "0000000000000000000994087c3bf8be")
}

func TestConvertTimestamp(t *testing.T) {
	is := is.New(t)
	tr := initTr()

	v := int64(1722075128424658)
	val := tr.convertTimestamp(&v)
	is.Equal(val.String(), "2024-07-27 10:12:08.424658 +0000 UTC")
}
