package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Init sets up the global OpenTelemetry tracer provider.
// If endpoint is empty, a no-op tracer is used (safe for dev/testing).
// When insecure is true, the exporter uses plaintext gRPC (suitable for
// local collectors). Set insecure to false for TLS-enabled collectors
// (e.g. Grafana Cloud, Datadog).
// sampleRatio controls the fraction of traces sampled (0.0–1.0). A value <= 0
// defaults to 0.1 (10%); a value >= 1.0 samples everything.
// Returns a shutdown function that should be called on application exit.
func Init(ctx context.Context, serviceName, endpoint string, insecure bool, sampleRatio float64) (func(context.Context) error, error) {
	if endpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(endpoint),
	}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	var sampler sdktrace.Sampler
	switch {
	case sampleRatio >= 1.0:
		sampler = sdktrace.AlwaysSample()
	case sampleRatio <= 0:
		sampler = sdktrace.TraceIDRatioBased(0.1)
	default:
		sampler = sdktrace.TraceIDRatioBased(sampleRatio)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sampler)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Tracer returns a named tracer from the global provider.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
