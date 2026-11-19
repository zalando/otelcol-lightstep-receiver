package lightstep_thrift

import (
	"context"
	"errors"
	"testing"

	"github.com/matryer/is"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receivertest"

	lightstepCommon "github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_common"
	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_thrift/collectorthrift"
	"github.com/zalando/otelcol-lightstep-receiver/internal/telemetry"
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

func newTestTelemetry() *telemetry.Telemetry {
	t := &telemetry.Telemetry{}
	t.Init(receivertest.NewNopSettings(component.MustNewType("lightstepreceiver")))
	return t
}

func strPtr(v string) *string { return &v }

func int64Ptr(v int64) *int64 { return &v }

func validThriftReport() *collectorthrift.ReportRequest {
	return &collectorthrift.ReportRequest{
		Runtime: &collectorthrift.Runtime{
			Attrs: []*collectorthrift.KeyValue{{Key: "lightstep.component_name", Value: "test-service"}},
		},
		SpanRecords: []*collectorthrift.SpanRecord{{
			SpanGuid:       strPtr("0000000000000002"),
			TraceGuid:      strPtr("0000000000000001"),
			SpanName:       strPtr("test-span"),
			OldestMicros:   int64Ptr(1722075128424658),
			YoungestMicros: int64Ptr(1722075128524658),
			Attributes:     []*collectorthrift.KeyValue{{Key: "parent_span_guid", Value: "0000000000000003"}},
			LogRecords: []*collectorthrift.LogRecord{{
				TimestampMicros: int64Ptr(1722075128500000),
				Fields:          []*collectorthrift.KeyValue{{Key: "event", Value: "test-event"}},
			}},
		}},
		InternalMetrics: &collectorthrift.Metrics{
			Counts: []*collectorthrift.MetricsSample{{Name: "spans.dropped", Int64Value: int64Ptr(2)}},
		},
	}
}

func TestToOtelValidReport(t *testing.T) {
	token := "test-token"
	tr := NewThriftRequest(&collectorthrift.Auth{AccessToken: &token}, validThriftReport(), newTestTelemetry())
	res, err := tr.ToOtel(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-token", res.AccessToken)
	require.Equal(t, "test-service", res.ServiceName)
	require.Equal(t, int64(2), res.ClientSpansDropped)
	require.Equal(t, 1, res.Traces.SpanCount())
	span := res.Traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	require.Equal(t, "test-span", span.Name())
	require.Equal(t, "0000000000000002", span.SpanID().String())
	require.Equal(t, "00000000000000000000000000000001", span.TraceID().String())
	require.Equal(t, "0000000000000003", span.ParentSpanID().String())
	require.Equal(t, "2024-07-27 10:12:08.424658 +0000 UTC", span.StartTimestamp().String())
	require.Equal(t, "2024-07-27 10:12:08.524658 +0000 UTC", span.EndTimestamp().String())
	require.Equal(t, 1, span.Events().Len())
	require.Equal(t, "test-event", span.Events().At(0).Name())
}

func TestToOtelEmptyReport(t *testing.T) {
	report := &collectorthrift.ReportRequest{Runtime: &collectorthrift.Runtime{}}
	res, err := NewThriftRequest(nil, report, newTestTelemetry()).ToOtel(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, res.Traces.SpanCount())
}

func TestToOtelInvalidReport(t *testing.T) {
	for _, tt := range []struct {
		name   string
		modify func(*collectorthrift.ReportRequest) *collectorthrift.ReportRequest
		msg    string
	}{
		{"missing report", func(*collectorthrift.ReportRequest) *collectorthrift.ReportRequest { return nil }, "missing report"},
		{"missing runtime", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.Runtime = nil
			return r
		}, "missing runtime"},
		{"null runtime attr", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.Runtime.Attrs = append(r.Runtime.Attrs, nil)
			return r
		}, "runtime.attrs[1] is null"},
		{"null internal metric", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.InternalMetrics.Counts = []*collectorthrift.MetricsSample{nil}
			return r
		}, "internal_metrics.counts[0] is null"},
		{"null span record", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.SpanRecords = append(r.SpanRecords, nil)
			return r
		}, "span_records[1] is null"},
		{"missing start timestamp", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.SpanRecords[0].OldestMicros = nil
			return r
		}, "span_records[0].oldest_micros is missing"},
		{"missing end timestamp", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.SpanRecords[0].YoungestMicros = nil
			return r
		}, "span_records[0].youngest_micros is missing"},
		{"null span attribute", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.SpanRecords[0].Attributes = []*collectorthrift.KeyValue{nil}
			return r
		}, "span_records[0].attributes[0] is null"},
		{"null span attribute at nonzero indexes", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.SpanRecords = append(r.SpanRecords, validThriftReport().SpanRecords[0])
			r.SpanRecords[1].Attributes = append(r.SpanRecords[1].Attributes, nil)
			return r
		}, "span_records[1].attributes[1] is null"},
		{"null log record", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.SpanRecords[0].LogRecords = []*collectorthrift.LogRecord{nil}
			return r
		}, "span_records[0].log_records[0] is null"},
		{"missing log timestamp", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.SpanRecords[0].LogRecords[0].TimestampMicros = nil
			return r
		}, "span_records[0].log_records[0].timestamp_micros is missing"},
		{"null log field", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.SpanRecords[0].LogRecords[0].Fields = []*collectorthrift.KeyValue{nil}
			return r
		}, "span_records[0].log_records[0].fields[0] is null"},
		{"null log field at nonzero indexes", func(r *collectorthrift.ReportRequest) *collectorthrift.ReportRequest {
			r.SpanRecords = append(r.SpanRecords, validThriftReport().SpanRecords[0])
			r.SpanRecords[1].LogRecords = append(r.SpanRecords[1].LogRecords, validThriftReport().SpanRecords[0].LogRecords[0])
			r.SpanRecords[1].LogRecords[1].Fields = append(r.SpanRecords[1].LogRecords[1].Fields, nil)
			return r
		}, "span_records[1].log_records[1].fields[1] is null"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := tt.modify(validThriftReport())
			var (
				res *lightstepCommon.ProjectTraces
				err error
			)
			require.NotPanics(t, func() {
				res, err = NewThriftRequest(nil, report, newTestTelemetry()).ToOtel(context.Background())
			})
			require.Nil(t, res)
			require.True(t, errors.Is(err, ErrInvalidReport))
			require.Equal(t, "invalid thrift report: "+tt.msg, err.Error())
		})
	}
}
