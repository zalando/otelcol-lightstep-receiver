package lightstep_thrift

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.opentelemetry.io/collector/receiver/receivertest"

	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_thrift/collectorthrift"
	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_thrift/thrift_0_9_2/lib/go/thrift"
	"github.com/zalando/otelcol-lightstep-receiver/internal/telemetry"
)

func newTestServer(t *testing.T, consumed *int, token *string) *ThriftServer {
	t.Helper()
	next, err := consumer.NewTraces(func(ctx context.Context, td ptrace.Traces) error {
		*consumed += td.SpanCount()
		if values := client.FromContext(ctx).Metadata.Get("lightstep-access-token"); len(values) == 1 {
			*token = values[0]
		}
		return nil
	})
	require.NoError(t, err)
	set := receivertest.NewNopSettings(component.MustNewType("lightstepreceiver"))
	obs, err := receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
		ReceiverID: set.ID, Transport: transport, ReceiverCreateSettings: set,
	})
	require.NoError(t, err)
	metrics := &telemetry.Telemetry{}
	metrics.Init(set)
	return NewServer(nil, &set, next, obs, metrics)
}

const validJSONSpan = `{"span_guid":"0000000000000002","trace_guid":"0000000000000001","span_name":"test-span","oldest_micros":1722075128424658,"youngest_micros":1722075128524658}`

func TestHandleThriftJSONRequestV0(t *testing.T) {
	for _, tt := range []struct {
		name     string
		body     string
		code     int
		consumed int
		errMsg   string
	}{
		{
			name:     "valid",
			body:     `{"runtime":{"group_name":"test-service"},"span_records":[` + validJSONSpan + `]}`,
			code:     http.StatusOK,
			consumed: 1,
		},
		{
			name:   "missing runtime",
			body:   `{"span_records":[` + validJSONSpan + `]}`,
			code:   http.StatusBadRequest,
			errMsg: "invalid thrift report: missing runtime",
		},
		{
			name:   "null runtime",
			body:   `{"runtime":null,"span_records":[` + validJSONSpan + `]}`,
			code:   http.StatusBadRequest,
			errMsg: "invalid thrift report: missing runtime",
		},
		{
			name:   "null span record",
			body:   `{"runtime":{"group_name":"test-service"},"span_records":[null]}`,
			code:   http.StatusBadRequest,
			errMsg: "invalid thrift report: span_records[0] is null",
		},
		{
			name:   "missing start timestamp",
			body:   `{"runtime":{"group_name":"test-service"},"span_records":[{"span_guid":"0000000000000002","youngest_micros":1722075128524658}]}`,
			code:   http.StatusBadRequest,
			errMsg: "invalid thrift report: span_records[0].oldest_micros is missing",
		},
		{
			name:   "missing end timestamp",
			body:   `{"runtime":{"group_name":"test-service"},"span_records":[{"span_guid":"0000000000000002","oldest_micros":1722075128424658}]}`,
			code:   http.StatusBadRequest,
			errMsg: "invalid thrift report: span_records[0].youngest_micros is missing",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			consumed, token := 0, ""
			s := newTestServer(t, &consumed, &token)
			rq := httptest.NewRequest(http.MethodPost, "/api/v0/reports", bytes.NewBufferString(tt.body))
			rq.Header.Set(headerLightstepAccessToken, "test-token")
			w := httptest.NewRecorder()
			require.NotPanics(t, func() { s.HandleThriftJSONRequestV0(w, rq) })
			require.Equal(t, tt.code, w.Code)
			require.Equal(t, tt.consumed, consumed)
			var resp collectorthrift.ReportResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			if tt.errMsg == "" {
				require.Empty(t, resp.Errors)
				require.Equal(t, "test-token", token)
			} else {
				require.Equal(t, []string{tt.errMsg}, resp.Errors)
			}
		})
	}
}

func TestHandleThriftBinaryRequestInvalidReport(t *testing.T) {
	for _, tt := range []struct {
		name     string
		report   *collectorthrift.ReportRequest
		code     int
		consumed int
	}{
		{"valid", validThriftReport(), http.StatusOK, 1},
		{"missing runtime", &collectorthrift.ReportRequest{}, http.StatusBadRequest, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			token := "test-token"
			buf := thrift.NewTMemoryBuffer()
			oprot := thrift.NewTBinaryProtocolTransport(buf)
			require.NoError(t, oprot.WriteMessageBegin("Report", thrift.CALL, 1))
			require.NoError(t, (&collectorthrift.ReportArgs{
				Auth:    &collectorthrift.Auth{AccessToken: &token},
				Request: tt.report,
			}).Write(oprot))
			require.NoError(t, oprot.WriteMessageEnd())

			consumed, gotToken := 0, ""
			s := newTestServer(t, &consumed, &gotToken)
			rq := httptest.NewRequest(http.MethodPost, "/_rpc/v1/reports/binary", bytes.NewReader(buf.Bytes()))
			w := httptest.NewRecorder()
			require.NotPanics(t, func() { s.HandleThriftBinaryRequest(w, rq) })
			require.Equal(t, tt.code, w.Code)
			require.Equal(t, tt.consumed, consumed)
		})
	}
}
