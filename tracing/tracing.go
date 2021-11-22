// The tracing package provides utilities for the Kiali server
// to instrument itself with tracing to provide better insights
// into server performance. Currently only integrated with Jaeger.
package tracing

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

const (
	// Service is the name of the kiali tracer service.
	Service = "kiali-server"
	// TracerName is the name of the global kiali Trace.
	TracerName = Service
	id         = 1
)

// InitTracer initalizes a TracerProvider that exports to jaeger.
// This will panic if there's an error in setup.
func InitTracer(jaegerURL string) *sdktrace.TracerProvider {
	exporter, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(jaegerURL)))
	if err != nil {
		panic(err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exporter),
		// Record information about this application in an Resource.
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(Service),
		)),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp
}

// Stop shutdown the provider.
func Stop(provider *sdktrace.TracerProvider) {
	if provider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		provider.Shutdown(ctx)
	}
}
