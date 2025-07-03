package lightstep_pb

import (
	"context"
	"testing"

	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/matryer/is"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"

	pb "github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_pb/collectorpb"
	"github.com/zalando/otelcol-lightstep-receiver/internal/telemetry"
)

func initTelemetry() *telemetry.Telemetry {
	t := &telemetry.Telemetry{}

	logger, _ := zap.NewDevelopment(zap.WithCaller(true))
	t.Init(receiver.Settings{
		ID: component.ID{},
		TelemetrySettings: component.TelemetrySettings{
			TracerProvider: noop.NewTracerProvider(),
			Logger:         logger,
		},
		BuildInfo: component.BuildInfo{},
	})
	return t
}
func TestTransformation(t *testing.T) {
	is := is.New(t)
	rq := Request{
		orig: &pb.ReportRequest{
			Auth: &pb.Auth{
				AccessToken: "access-token",
			},
			Reporter: &pb.Reporter{
				ReporterId: 0,
				Tags: []*pb.KeyValue{
					{
						Key:   "reporter-tag",
						Value: &pb.KeyValue_StringValue{StringValue: []byte("reporter-value-string")},
					},
					{
						Key:   "lightstep.component_name",
						Value: &pb.KeyValue_StringValue{StringValue: []byte("service.name")},
					},
				},
			},
			Spans: []*pb.Span{
				{
					SpanContext: &pb.SpanContext{
						TraceId: 5633477863404139573,
						SpanId:  13131584195926266923,
						Baggage: nil,
					},
					OperationName: "operation-name",
					References: []*pb.Reference{
						{
							SpanContext: &pb.SpanContext{
								SpanId: 2647710007585667870,
							},
						},
					},
					StartTimestamp: &timestamp.Timestamp{
						Seconds: 1718207928,
						Nanos:   350615000,
					},
					DurationMicros: 100,
					Tags: []*pb.KeyValue{
						{
							Key:   "tag-string",
							Value: &pb.KeyValue_StringValue{StringValue: []byte("tag-value-string")},
						},
						{
							Key:   "tag-bool",
							Value: &pb.KeyValue_BoolValue{BoolValue: false},
						},
						{
							Key:   "tag-int",
							Value: &pb.KeyValue_IntValue{IntValue: 55},
						},
						{
							Key:   "tag-double",
							Value: &pb.KeyValue_DoubleValue{DoubleValue: 55.01},
						},
						{
							Key:   "error",
							Value: &pb.KeyValue_BoolValue{BoolValue: true},
						},
						{
							Key:   "span.kind",
							Value: &pb.KeyValue_StringValue{StringValue: []byte("client")},
						},
					},
					Logs: []*pb.Log{
						{
							Timestamp: &timestamp.Timestamp{
								Seconds: 1718207928,
								Nanos:   350615000,
							},
							Fields: []*pb.KeyValue{
								{
									Key:   "log-field",
									Value: &pb.KeyValue_StringValue{StringValue: []byte("log-value-string")},
								},
								{
									Key:   "event",
									Value: &pb.KeyValue_StringValue{StringValue: []byte("event-name")},
								},
							},
						},
					},
				},
			},
		},
		telemetry: initTelemetry(),
	}
	rqOtel, err := rq.ToOtel(context.Background())
	is.NoErr(err)
	is.True(rqOtel.SpanCount() == 1)

	is.Equal(rqOtel.AccessToken, "access-token")
	is.Equal(rqOtel.ClientSpansDropped, int64(0))

	resource := rqOtel.ResourceSpans().At(0).Resource()
	span := rqOtel.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	is.Equal(span.Name(), "operation-name")
	is.Equal(span.Status().Code(), ptrace.StatusCodeError)
	is.Equal(span.ParentSpanID().String(), "24be8e394663fb1e")
	is.Equal(span.StartTimestamp().AsTime().UnixNano(), int64(1718207928350615000))
	is.Equal(span.EndTimestamp().AsTime().UnixNano(), int64(1718207928350715000))
	is.Equal(span.Kind(), ptrace.SpanKindClient)

	var (
		ok bool
		v  pcommon.Value
	)

	v, ok = resource.Attributes().Get("reporter-tag")
	is.True(ok)
	is.Equal(v.Str(), "reporter-value-string")

	v, ok = span.Attributes().Get("tag-string")
	is.True(ok)
	is.Equal(v.Str(), "tag-value-string")

	v, ok = span.Attributes().Get("tag-bool")
	is.True(ok)
	is.Equal(v.Bool(), false)

	v, ok = span.Attributes().Get("tag-int")
	is.True(ok)
	is.Equal(v.Int(), int64(55))

	v, ok = span.Attributes().Get("tag-double")
	is.True(ok)
	is.Equal(v.Double(), 55.01)

	is.Equal(span.Events().Len(), 1)
	is.Equal(span.Events().At(0).Name(), "event-name")
	is.Equal(span.Events().At(0).Timestamp().AsTime().UnixNano(), int64(1718207928350615000))

	v, ok = span.Events().At(0).Attributes().Get("log-field")
	is.True(ok)
	is.Equal(v.Str(), "log-value-string")
}

func TestTransformation_NoServiceName(t *testing.T) {
	is := is.New(t)
	rq := Request{
		orig: &pb.ReportRequest{
			Auth: &pb.Auth{
				AccessToken: "access-token",
			},
			Reporter: &pb.Reporter{
				ReporterId: 0,
				Tags:       nil,
			},
			Spans: nil,
		},
		telemetry: initTelemetry(),
	}
	_, err := rq.ToOtel(context.Background())
	is.Equal(err, nil)
}

func TestTransformation_NoAccessToken(t *testing.T) {
	is := is.New(t)
	rq := Request{
		orig: &pb.ReportRequest{
			Auth: nil,
			Reporter: &pb.Reporter{
				ReporterId: 0,
				Tags:       nil,
			},
			Spans: nil,
		},
		telemetry: initTelemetry(),
	}
	_, err := rq.ToOtel(context.Background())
	is.Equal(err, nil)
}

func TestTransformation_GetClientDropSpans(t *testing.T) {
	is := is.New(t)
	rq := Request{
		orig: &pb.ReportRequest{
			Auth: &pb.Auth{
				AccessToken: "access-token",
			},
			Reporter: &pb.Reporter{
				ReporterId: 0,
				Tags: []*pb.KeyValue{
					{
						Key:   "lightstep.component_name",
						Value: &pb.KeyValue_StringValue{StringValue: []byte("service.name")},
					},
				},
			},
			InternalMetrics: &pb.InternalMetrics{
				Counts: []*pb.MetricsSample{
					{
						Name:  "spans.dropped",
						Value: &pb.MetricsSample_IntValue{IntValue: 10},
					}},
			},
			Spans: nil,
		},
		telemetry: initTelemetry(),
	}
	rqOtel, err := rq.ToOtel(context.Background())
	is.NoErr(err)
	is.Equal(rqOtel.ClientSpansDropped, int64(10))
}

func TestConvertTraceID(t *testing.T) {
	is := is.New(t)
	c := convertTraceID(11823890906499043596)
	is.Equal(c.String(), "0000000000000000a416e62240d8d10c")
}

func TestConvertSpanID(t *testing.T) {
	is := is.New(t)
	c := convertSpanID(11823890906499043596)
	is.Equal(c.String(), "a416e62240d8d10c")
}

func TestInvalidUTF8(t *testing.T) {
	is := is.New(t)
	rq := Request{
		orig: &pb.ReportRequest{
			Auth: &pb.Auth{
				AccessToken: "access-token",
			},
			Reporter: &pb.Reporter{
				ReporterId: 0,
				Tags: []*pb.KeyValue{
					{
						Key:   "lightstep.component_name",
						Value: &pb.KeyValue_StringValue{StringValue: []byte("service.name")},
					},
					{
						Key:   "not-utf8-string",
						Value: &pb.KeyValue_StringValue{StringValue: []byte("pok\xE9mon")},
					},
				},
			},
			Spans: nil,
		},
		transport: "foo",
		telemetry: initTelemetry(),
	}
	res, err := rq.ToOtel(context.Background())
	is.NoErr(err)
	is.Equal(rq.telemetry.NonUTF8Attributes[rq.transport], int64(1))
	is.Equal(res.ResourceSpans().At(0).Resource().Attributes().Len(), 2)
}
