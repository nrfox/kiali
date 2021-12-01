// The observability package provides utilities for the Kiali server
// to instrument itself with observability tools such as tracing to provide
// better insights into server performance.
package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"

	"github.com/kiali/kiali/config"
)

const (
	// TracingService is the name of the kiali tracer service.
	TracingService = "kiali-server"
)

// TracerName is the name of the global kiali Trace.
func TracerName() string {
	return TracingService + "." + config.Get().Deployment.Namespace
}

// InitTracer initalizes a TracerProvider that exports to jaeger.
// This will panic if there's an error in setup.
func InitTracer(jaegerURL string) *sdktrace.TracerProvider {
	exporter, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(jaegerURL)))
	if err != nil {
		panic(err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.5))), // Sample half of traces. 
		sdktrace.WithBatcher(exporter),
		// Record information about this application in an Resource.
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(TracingService),
		)),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp
}

// Stop shutdown the provider.
func StopTracer(provider *sdktrace.TracerProvider) {
	if provider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		_ = provider.Shutdown(ctx)
	}
}
