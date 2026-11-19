package lightstep_thrift

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	lightstepConstants "github.com/lightstep/lightstep-tracer-go/constants"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	lightstepCommon "github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_common"
	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_thrift/collectorthrift"
	"github.com/zalando/otelcol-lightstep-receiver/internal/telemetry"
)

// ErrInvalidReport happens when a thrift report misses a structure or timestamp that the conversion needs
var ErrInvalidReport = errors.New("invalid thrift report")

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

	if err := validateReport(tr.orig); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	result := &lightstepCommon.ProjectTraces{}

	if tr.auth == nil || tr.auth.AccessToken == nil {
		span.SetStatus(codes.Error, lightstepCommon.ErrNoAccessToken.Error())
	} else {
		result.AccessToken = tr.auth.GetAccessToken()
	}

	data := ptrace.NewTraces()
	rs := data.ResourceSpans().AppendEmpty()
	rAttr := rs.Resource().Attributes()

	tr.kvToAttr(tr.orig.Runtime.Attrs, &rAttr)
	serviceName, ok := rAttr.Get(lightstepConstants.ComponentNameKey)
	if ok {
		result.ServiceName = serviceName.Str()
		rAttr.PutStr("service.name", serviceName.Str())
	} else {
		span.SetStatus(codes.Error, lightstepCommon.ErrNoServiceName.Error())
	}

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

		if value, ok := attr.Get("error"); ok {
			if lightstepCommon.IsErrorAttributeValueActuallyError(value) {
				s.Status().SetCode(ptrace.StatusCodeError)
				attr.Remove("error")
			}
		}

		if spanKind, ok := attr.Get("span.kind"); ok {
			otelSpanKind, stringSpanKind := lightstepCommon.ParseSpanKindAttributeValue(spanKind)
			s.SetKind(otelSpanKind)
			if otelSpanKind == ptrace.SpanKindUnspecified {
				attr.PutStr("span.custom_kind", stringSpanKind)
			}
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

// validateReport rejects reports with nil structures that ToOtel would dereference.
// Thrift JSON can send null list entries and omit optional fields, so these reports
// get HTTP 400 instead of a handler panic. A missing span timestamp is rejected, not
// set to the Unix epoch.
func validateReport(rq *collectorthrift.ReportRequest) error {
	if rq == nil {
		return fmt.Errorf("%w: missing report", ErrInvalidReport)
	}
	if rq.Runtime == nil {
		return fmt.Errorf("%w: missing runtime", ErrInvalidReport)
	}
	if err := validateKeyValues(rq.Runtime.Attrs, "runtime.attrs"); err != nil {
		return err
	}
	if rq.InternalMetrics != nil {
		for i, m := range rq.InternalMetrics.Counts {
			if m == nil {
				return fmt.Errorf("%w: internal_metrics.counts[%d] is null", ErrInvalidReport, i)
			}
		}
	}
	for i, s := range rq.SpanRecords {
		if s == nil {
			return fmt.Errorf("%w: span_records[%d] is null", ErrInvalidReport, i)
		}
		if s.OldestMicros == nil {
			return fmt.Errorf("%w: span_records[%d].oldest_micros is missing", ErrInvalidReport, i)
		}
		if s.YoungestMicros == nil {
			return fmt.Errorf("%w: span_records[%d].youngest_micros is missing", ErrInvalidReport, i)
		}
		for j, attr := range s.Attributes {
			if attr == nil {
				return fmt.Errorf("%w: span_records[%d].attributes[%d] is null", ErrInvalidReport, i, j)
			}
		}
		for j, l := range s.LogRecords {
			if l == nil {
				return fmt.Errorf("%w: span_records[%d].log_records[%d] is null", ErrInvalidReport, i, j)
			}
			if l.TimestampMicros == nil {
				return fmt.Errorf("%w: span_records[%d].log_records[%d].timestamp_micros is missing", ErrInvalidReport, i, j)
			}
			for k, field := range l.Fields {
				if field == nil {
					return fmt.Errorf("%w: span_records[%d].log_records[%d].fields[%d] is null", ErrInvalidReport, i, j, k)
				}
			}
		}
	}
	return nil
}

func validateKeyValues(kv []*collectorthrift.KeyValue, path string) error {
	for i, t := range kv {
		if t == nil {
			return fmt.Errorf("%w: %s[%d] is null", ErrInvalidReport, path, i)
		}
	}
	return nil
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
