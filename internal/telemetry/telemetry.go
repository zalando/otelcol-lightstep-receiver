package telemetry

import (
	"context"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Telemetry keeps module telemetry as meters and logger
type Telemetry struct {
	Name              string
	NonUTF8Attributes map[string]int64

	_requestsProcessed  metric.Int64Counter
	_requestsFailed     metric.Int64Counter
	_nonUTF8Attributes  metric.Int64Counter
	_clientSpansDropped metric.Int64Counter

	Logger *zap.Logger
	Tracer trace.Tracer
}

func (t *Telemetry) logError(err error, name string) {
	switch err != nil {
	case true:
		t.Logger.Warn("Can not initialized metric",
			zap.String("name", name),
			zap.Error(err),
		)
	case false:
		t.Logger.Info("Metric initialized",
			zap.String("name", name),
		)
	}
}

// Init initialises the Meters
func (t *Telemetry) Init(set receiver.Settings) {
	var (
		err         error
		name        string
		description string
	)

	if set.TracerProvider != nil {
		t.Tracer = set.TracerProvider.Tracer(t.Name)
	}
	t.NonUTF8Attributes = make(map[string]int64)

	t.Logger = set.Logger

	if set.MeterProvider == nil {
		return
	}

	meter := set.MeterProvider.Meter(set.ID.Name())

	name = "lightstep_receiver_requests_processed"
	description = "Number of processed requests"
	t._requestsProcessed, err = meter.Int64Counter(
		name,
		metric.WithDescription(description),
		metric.WithUnit("1"),
	)
	t.logError(err, name)

	name = "lightstep_receiver_requests_failed"
	description = "Number of failed requests"
	t._requestsFailed, err = meter.Int64Counter(
		name,
		metric.WithDescription(description),
		metric.WithUnit("1"),
	)
	t.logError(err, name)

	name = "lightstep_receiver_non_utf8_attributes_received"
	description = "Number of non UTF8 attributes received"
	t._nonUTF8Attributes, err = meter.Int64Counter(
		name,
		metric.WithDescription(description),
		metric.WithUnit("1"),
	)
	t.logError(err, name)

	name = "lightstep_receiver_client_spans_dropped"
	description = "Number of spans dropped in client side"
	t._clientSpansDropped, err = meter.Int64Counter(
		name,
		metric.WithDescription(description),
		metric.WithUnit("1"),
	)
	t.logError(err, name)
}

func (t *Telemetry) IncrementClientDropSpans(serviceName string, value int64) {
	if t._clientSpansDropped == nil {
		return
	}
	t._clientSpansDropped.Add(
		context.Background(),
		value,
		metric.WithAttributeSet(
			attribute.NewSet(
				attribute.String("for.service.name", serviceName),
			),
		),
	)
}

func (t *Telemetry) IncrementProcessed(transport string, value int64) {
	if t._requestsProcessed == nil {
		return
	}
	t._requestsProcessed.Add(
		context.Background(),
		value,
		metric.WithAttributeSet(
			attribute.NewSet(
				attribute.String("transport", transport),
			),
		),
	)
}

func (t *Telemetry) IncrementFailed(transport string, value int64) {
	if t._requestsFailed == nil {
		return
	}
	t._requestsFailed.Add(
		context.Background(),
		value,
		metric.WithAttributeSet(
			attribute.NewSet(
				attribute.String("transport", transport),
			),
		),
	)
}

func (t *Telemetry) IncrementNonUTF8Attributes(transport string, value int64) {
	if _, ok := t.NonUTF8Attributes[transport]; !ok {
		t.NonUTF8Attributes[transport] = value
	} else {
		t.NonUTF8Attributes[transport]++
	}

	if t._nonUTF8Attributes == nil {
		return
	}
	t._nonUTF8Attributes.Add(
		context.Background(),
		value,
		metric.WithAttributeSet(
			attribute.NewSet(
				attribute.String("transport", transport),
			),
		),
	)
}
