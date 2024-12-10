package lightstep_pb

import (
	"context"
	"encoding/binary"
	lightstepConstants "github.com/lightstep/lightstep-tracer-go/constants"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
	"strings"
	"time"
	"unicode/utf8"

	lightstepCommon "github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/lightstep_common"
	pb "github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/lightstep_pb/collectorpb"
	"github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/telemetry"
)

// Request wraps incoming Lightstep tracer ReportRequest
type Request struct {
	orig      *pb.ReportRequest
	telemetry *telemetry.Telemetry
	transport string
}

// NewLightstepRequest creates new LightstepRequest
func NewLightstepRequest(orig *pb.ReportRequest, t *telemetry.Telemetry, transport string) *Request {
	return &Request{
		orig:      orig,
		telemetry: t,
		transport: transport,
	}
}

func (r *Request) reportNonUtf8(serviceName string, keys *[]string) {
	if len(*keys) == 0 {
		return
	}
	r.telemetry.IncrementNonUTF8Attributes(r.transport, int64(len(*keys)))
	r.telemetry.Logger.Info(
		"Non UTF8 string detected",
		zap.String("service.name", serviceName),
		zap.Strings("keys", *keys),
	)
}

// ToOtel transforms data from lightstep.ReportRequest into Otel ptrace.Traces
func (r *Request) ToOtel(ctx context.Context) (*lightstepCommon.ProjectTraces, error) {
	_, span := r.telemetry.Tracer.Start(ctx, "to-otel")
	defer span.End()

	result := &lightstepCommon.ProjectTraces{}

	if r.orig.Auth == nil || r.orig.Auth.AccessToken == "" {
		span.SetStatus(codes.Error, lightstepCommon.ErrNoAccessToken.Error())
		return result, lightstepCommon.ErrNoAccessToken
	}
	result.AccessToken = r.orig.Auth.AccessToken

	data := ptrace.NewTraces()
	rs := data.ResourceSpans().AppendEmpty()
	rAttr := rs.Resource().Attributes()

	nonUtf8Keys, err := r.kvToAttr(r.orig.Reporter.Tags, &rAttr)
	serviceName, ok := rAttr.Get(lightstepConstants.ComponentNameKey)
	result.ServiceName = serviceName.Str()
	if !ok {
		return result, lightstepCommon.ErrNoServiceName
	}
	if err != nil {
		span.SetStatus(codes.Error, "non-utf8-keys")
		r.reportNonUtf8(result.ServiceName, nonUtf8Keys)
	}

	rAttr.PutStr("service.name", serviceName.Str())

	if r.orig.InternalMetrics != nil {
		for _, m := range r.orig.InternalMetrics.Counts {
			if m.Name == "spans.dropped" {
				result.ClientSpansDropped = m.GetIntValue()
				break
			}
		}
	}

	ss := rs.ScopeSpans().AppendEmpty()
	for _, span := range r.orig.Spans {
		s := ss.Spans().AppendEmpty()
		s.SetSpanID(convertSpanID(span.GetSpanContext().SpanId))
		s.SetTraceID(convertTraceID(span.GetSpanContext().TraceId))
		s.SetName(span.GetOperationName())

		if len(span.References) == 1 {
			s.SetParentSpanID(convertSpanID(span.References[0].SpanContext.SpanId))
		}

		startTimestamp := span.StartTimestamp.AsTime()
		s.SetStartTimestamp(pcommon.NewTimestampFromTime(startTimestamp))

		endTimeStamp := startTimestamp.Add(time.Duration(span.DurationMicros) * time.Microsecond)
		s.SetEndTimestamp(pcommon.NewTimestampFromTime(endTimeStamp))

		attr := s.Attributes()
		if nonUtf8Keys, err = r.kvToAttr(span.Tags, &attr); err != nil {
			r.reportNonUtf8(result.ServiceName, nonUtf8Keys)
		}

		if _, ok := attr.Get("error"); ok {
			s.Status().SetCode(ptrace.StatusCodeError)
			attr.Remove("error")
		}

		for _, log := range span.Logs {
			ev := s.Events().AppendEmpty()
			ev.SetTimestamp(pcommon.NewTimestampFromTime(log.Timestamp.AsTime()))

			evAttr := ev.Attributes()
			if nonUtf8Keys, err = r.kvToAttr(log.Fields, &evAttr); err != nil {
				r.reportNonUtf8(result.ServiceName, nonUtf8Keys)
			}
			if evName, ok := evAttr.Get("event"); ok {
				ev.SetName(evName.Str())
				evAttr.Remove("event")
			}
		}
	}
	result.Traces = data
	return result, nil
}

func (r *Request) kvToAttr(kv []*pb.KeyValue, p *pcommon.Map) (*[]string, error) {
	res := *p
	var nonUtf8Keys []string
	for _, t := range kv {
		if strings.HasPrefix(t.Key, "lightstep.") && t.Key != lightstepConstants.ComponentNameKey {
			continue
		}
		if v, ok := t.GetValue().(*pb.KeyValue_StringValue); ok {
			if !utf8.Valid(v.StringValue) {
				nonUtf8Keys = append(nonUtf8Keys, t.Key)
				continue
			}
			res.PutStr(t.Key, string(v.StringValue))
		} else if v, ok := t.GetValue().(*pb.KeyValue_BoolValue); ok {
			res.PutBool(t.Key, v.BoolValue)
		} else if v, ok := t.GetValue().(*pb.KeyValue_DoubleValue); ok {
			res.PutDouble(t.Key, v.DoubleValue)
		} else if v, ok := t.GetValue().(*pb.KeyValue_IntValue); ok {
			res.PutInt(t.Key, v.IntValue)
		} else if v, ok := t.GetValue().(*pb.KeyValue_JsonValue); ok {
			res.PutStr(t.Key, v.JsonValue)
		}
	}
	if len(nonUtf8Keys) > 0 {
		return &nonUtf8Keys, lightstepCommon.ErrNonUTF8Attribute
	}
	return nil, nil
}

func convertSpanID(v uint64) pcommon.SpanID {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return pcommon.SpanID(b)
}

func convertTraceID(v uint64) pcommon.TraceID {
	b1 := make([]byte, 8)
	b2 := make([]byte, 8)
	binary.BigEndian.PutUint64(b2, v)

	b1 = append(b1, b2...)
	return pcommon.TraceID(b1)
}
