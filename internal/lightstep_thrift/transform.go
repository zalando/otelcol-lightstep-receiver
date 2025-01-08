package lightstep_thrift

import (
	"context"
	"encoding/hex"
	lightstepConstants "github.com/lightstep/lightstep-tracer-go/constants"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
	"strings"
	"time"

	lightstepCommon "github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_common"
	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_thrift/collectorthrift"
	"github.com/zalando/otelcol-lightstep-receiver/internal/telemetry"
)

type Request struct {
	auth      *collectorthrift.Auth
	orig      *collectorthrift.ReportRequest
	telemetry *telemetry.Telemetry
}

func NewThriftRequest(auth *collectorthrift.Auth, orig *collectorthrift.ReportRequest, t *telemetry.Telemetry) *Request {
	return &Request{
		auth:      auth,
		orig:      orig,
		telemetry: t,
	}
}

func (tr *Request) kvToAttr(kv []*collectorthrift.KeyValue, p *pcommon.Map) {
	res := *p
	for _, t := range kv {
		if strings.HasPrefix(t.GetKey(), "lightstep.") && t.GetKey() != lightstepConstants.ComponentNameKey {
			continue
		}
		res.PutStr(t.GetKey(), t.GetValue())
	}
}

func (tr *Request) ToOtel(ctx context.Context) (*lightstepCommon.ProjectTraces, error) {
	_, span := tr.telemetry.Tracer.Start(ctx, "to-otel")
	defer span.End()

	result := &lightstepCommon.ProjectTraces{}

	if tr.auth == nil || tr.auth.AccessToken == nil {
		return result, lightstepCommon.ErrNoAccessToken
	}
	result.AccessToken = tr.auth.GetAccessToken()

	data := ptrace.NewTraces()
	rs := data.ResourceSpans().AppendEmpty()
	rAttr := rs.Resource().Attributes()

	tr.kvToAttr(tr.orig.Runtime.Attrs, &rAttr)
	serviceName, ok := rAttr.Get(lightstepConstants.ComponentNameKey)
	result.ServiceName = serviceName.Str()
	if !ok {
		span.SetStatus(codes.Error, lightstepCommon.ErrNoServiceName.Error())
		return result, lightstepCommon.ErrNoServiceName
	}
	rAttr.PutStr("service.name", serviceName.Str())

	if tr.orig.InternalMetrics != nil {
		for _, m := range tr.orig.InternalMetrics.Counts {
			if m.Name == "spans.dropped" {
				result.ClientSpansDropped = m.GetInt64Value()
				break
			}
		}
	}

	ss := rs.ScopeSpans().AppendEmpty()
	for _, span := range tr.orig.SpanRecords {
		s := ss.Spans().AppendEmpty()
		s.SetName(span.GetSpanName())

		s.SetSpanID(tr.convertSpanID(span.GetSpanGuid()))
		s.SetTraceID(tr.convertTraceID(span.GetTraceGuid()))

		s.SetStartTimestamp(tr.convertTimestamp(span.OldestMicros))
		s.SetEndTimestamp(tr.convertTimestamp(span.YoungestMicros))

		attr := s.Attributes()
		tr.kvToAttr(span.Attributes, &attr)

		if parentSpanID, ok := attr.Get("parent_span_guid"); ok {
			s.SetParentSpanID(tr.convertSpanID(parentSpanID.Str()))
			attr.Remove("parent_span_guid")
		}

		if _, ok := attr.Get("error"); ok {
			s.Status().SetCode(ptrace.StatusCodeError)
			attr.Remove("error")
		}

		for _, log := range span.LogRecords {
			ev := s.Events().AppendEmpty()

			ev.SetTimestamp(tr.convertTimestamp(log.TimestampMicros))

			evAttr := ev.Attributes()
			tr.kvToAttr(log.Fields, &evAttr)
			if evName, ok := evAttr.Get("event"); ok {
				ev.SetName(evName.Str())
				evAttr.Remove("event")
			}
		}
	}

	result.Traces = data
	return result, nil
}

func (tr *Request) convertTimestamp(v *int64) pcommon.Timestamp {
	res := *v
	return pcommon.NewTimestampFromTime(time.UnixMicro(res))
}

func (tr *Request) convertSpanID(v string) pcommon.SpanID {
	if len(v) < 16 {
		v = strings.Repeat("0", 16-len(v)) + v
	}
	b, err := hex.DecodeString(v)
	if err != nil {
		tr.telemetry.Logger.Warn("can't convert span id", zap.String("span id", v), zap.Error(err))
		return pcommon.NewSpanIDEmpty()
	}
	return pcommon.SpanID(b)
}

func (tr *Request) convertTraceID(v string) pcommon.TraceID {
	if len(v) < 16 {
		v = strings.Repeat("0", 16-len(v)) + v
	}

	b1 := make([]byte, 8)
	b2, err := hex.DecodeString(v)
	if err != nil {
		tr.telemetry.Logger.Warn("can't convert trace id", zap.String("span id", v), zap.Error(err))
		return pcommon.NewTraceIDEmpty()
	}

	b1 = append(b1, b2...)
	return pcommon.TraceID(b1)
}
