package lightstep_thrift

import (
	"context"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.uber.org/zap"
	"time"

	"github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/lightstep_thrift/collectorthrift"
	"github.bus.zalan.do/logging/otelcol-lightstep-receiver/internal/telemetry"
)

// ThriftServerReportRequest defines thrift report request with http context
type ThriftServerReportRequest struct {
	context    context.Context
	obsreport  *receiverhelper.ObsReport
	nextTraces consumer.Traces
	telemetry  *telemetry.Telemetry
}

func (tsr *ThriftServerReportRequest) getFormatFromContext() string {
	format := "undefined"
	if len(client.FromContext(tsr.context).Metadata.Get("format")) == 1 {
		format = client.FromContext(tsr.context).Metadata.Get("format")[0]
	}
	return format
}

// Report implements collectorthrift/ReportingService interface processing the thrift ReportRequest
func (tsr *ThriftServerReportRequest) Report(auth *collectorthrift.Auth, request *collectorthrift.ReportRequest) (r *collectorthrift.ReportResponse, err error) {
	ctx := tsr.obsreport.StartTracesOp(tsr.context)

	tr := NewThriftRequest(auth, request, tsr.telemetry)

	otelTr, err := tr.ToOtel(ctx)

	tsr.telemetry.Logger.Debug(
		"converted to otel",
		zap.Error(err),
	)

	if err != nil {
		tsr.telemetry.IncrementFailed(transport, 1)
		tsr.telemetry.Logger.Error("can't translate")
		tsr.obsreport.EndTracesOp(ctx, tsr.getFormatFromContext(), 0, err)
		return tsr.newReportResponse(err), err
	}

	tsr.telemetry.IncrementProcessed(transport, 1)
	tsr.telemetry.IncrementClientDropSpans(otelTr.ServiceName, otelTr.ClientSpansDropped)

	clientInfo := client.FromContext(context.Background())
	clientInfo.Metadata = client.NewMetadata(map[string][]string{"lightstep-access-token": {otelTr.AccessToken}})
	ctx = client.NewContext(ctx, clientInfo)

	err = tsr.nextTraces.ConsumeTraces(ctx, otelTr.Traces)

	tsr.obsreport.EndTracesOp(ctx, tsr.getFormatFromContext(), otelTr.Traces.SpanCount(), err)
	return tsr.newReportResponse(err), err
}

func (tsr *ThriftServerReportRequest) newReportResponse(err error) *collectorthrift.ReportResponse {
	now := time.Now().UnixMicro()
	res := &collectorthrift.ReportResponse{
		Commands: nil,
		Timing: &collectorthrift.Timing{
			ReceiveMicros:  &now,
			TransmitMicros: &now,
		},
	}
	if err != nil {
		res.Errors = []string{err.Error()}
	}
	return res
}
