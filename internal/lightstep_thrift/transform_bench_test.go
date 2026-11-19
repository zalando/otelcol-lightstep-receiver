package lightstep_thrift

import (
	"testing"

	"github.com/zalando/otelcol-lightstep-receiver/internal/lightstep_thrift/collectorthrift"
)

func BenchmarkValidateReportValid1000Spans(b *testing.B) {
	report := validThriftReport()
	report.SpanRecords = make([]*collectorthrift.SpanRecord, 1000)
	for i := range report.SpanRecords {
		report.SpanRecords[i] = validThriftReport().SpanRecords[0]
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validateReport(report); err != nil {
			b.Fatal(err)
		}
	}
}
