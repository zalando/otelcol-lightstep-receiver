package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang/protobuf/proto" //nolint:staticcheck
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_pb/collectorpb"
	"github.com/zalando/otelcol-lightstep-receiver/internal/telemetry"
)

func TestWriteResponseStatus(t *testing.T) {
	unauthenticated := status.Error(codes.Unauthenticated, "invalid report credentials")
	for _, tt := range []struct {
		name string
		err  error
		code int
	}{
		{"accepted", nil, http.StatusOK},
		{"authentication failure", unauthenticated, http.StatusUnauthorized},
		{"wrapped authentication failure", fmt.Errorf("consumer: %w", unauthenticated), http.StatusUnauthorized},
		{"report error", errors.New("invalid report"), http.StatusBadRequest},
		{"untyped authentication text", errors.New("invalid report credentials"), http.StatusBadRequest},
		{"other gRPC error", status.Error(codes.Internal, "consumer failed"), http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			received := time.Now().UTC()
			(&ServerHTTP{}).writeResponse(w, received, tt.err)
			response := w.Result()
			defer response.Body.Close()
			require.Equal(t, tt.code, response.StatusCode)
			require.Equal(t, "application/octet-stream", response.Header.Get("Content-Type"))
			if tt.code == http.StatusUnauthorized {
				require.Equal(t, `Lightstep realm="collector"`, response.Header.Get("WWW-Authenticate"))
			} else {
				require.Empty(t, response.Header.Get("WWW-Authenticate"))
			}
			var report collectorpb.ReportResponse
			require.NoError(t, proto.Unmarshal(w.Body.Bytes(), &report))
			require.Equal(t, received, report.ReceiveTimestamp.AsTime())
			require.NoError(t, report.TransmitTimestamp.CheckValid())
			if tt.err == nil {
				require.Empty(t, report.Errors)
			} else {
				require.Equal(t, []string{tt.err.Error()}, report.Errors)
			}
		})
	}
}

func TestHandleRequestAuthenticationStatus(t *testing.T) {
	for _, tt := range []struct {
		name       string
		auth       *collectorpb.Auth
		malformed  bool
		downstream error
		code       int
		consumed   bool
	}{
		{name: "valid", auth: &collectorpb.Auth{AccessToken: "valid-test-token"}, code: http.StatusOK, consumed: true},
		{name: "missing", code: http.StatusUnauthorized, consumed: true},
		{name: "empty", auth: &collectorpb.Auth{}, code: http.StatusUnauthorized, consumed: true},
		{name: "invalid", auth: &collectorpb.Auth{AccessToken: "invalid-test-token"}, code: http.StatusUnauthorized, consumed: true},
		{name: "known bad", auth: &collectorpb.Auth{AccessToken: "known-bad-test-token"}, code: http.StatusUnauthorized, consumed: true},
		{name: "downstream failure", auth: &collectorpb.Auth{AccessToken: "valid-test-token"}, downstream: errors.New("invalid report"), code: http.StatusBadRequest, consumed: true},
		{name: "malformed protobuf", malformed: true, code: http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			consumed := false
			next, err := consumer.NewTraces(func(ctx context.Context, _ ptrace.Traces) error {
				consumed = true
				values := client.FromContext(ctx).Metadata.Get("lightstep-access-token")
				if len(values) != 1 || values[0] != "valid-test-token" {
					return status.Error(codes.Unauthenticated, "invalid report credentials")
				}
				return tt.downstream
			})
			require.NoError(t, err)
			set := receivertest.NewNopSettings(component.MustNewType("lightstepreceiver"))
			obs, err := receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
				ReceiverID: set.ID, Transport: transport, ReceiverCreateSettings: set,
			})
			require.NoError(t, err)
			metrics := &telemetry.Telemetry{}
			metrics.Init(set)
			s := NewServer(nil, &set, next, obs, metrics)
			body, err := proto.Marshal(&collectorpb.ReportRequest{
				Auth:     tt.auth,
				Reporter: &collectorpb.Reporter{},
			})
			require.NoError(t, err)
			if tt.malformed {
				body = []byte{0xff}
			}
			w := httptest.NewRecorder()
			rq := httptest.NewRequest(http.MethodPost, "/api/v2/reports", bytes.NewReader(body))
			s.HandleRequest(w, rq)
			require.Equal(t, tt.code, w.Code)
			require.Equal(t, tt.consumed, consumed)
			var report collectorpb.ReportResponse
			require.NoError(t, proto.Unmarshal(w.Body.Bytes(), &report))
			if tt.code == http.StatusOK {
				require.Empty(t, report.Errors)
			} else {
				require.Len(t, report.Errors, 1)
			}
		})
	}
}
