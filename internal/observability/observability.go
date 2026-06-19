// Package observability initializes OpenTelemetry for the shortn services: it
// constructs the trace and metric providers, wires their exporters, sets the
// global propagator, and returns a single Shutdown to flush them on exit.
package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config is the settings need to bootstrap
type Config struct {
	ServiceName  string // indentify the service in every signal;
	InstanceID   string // distinguishes one replica
	OTLPEndpoint string // Tempo's OTLP/HTTP address,
}

// Providers bundles the constructed OTel machinery. Shutdown flushes and stops
// everything it built; the caller defers it once, at program exit.
type Providers struct {
	MetricsHandler http.Handler // mount at GET /metrics for Prometheus to scrape
	Shutdown       func(context.Context) error
}

// Setup builds the OpenTelemetry providers from cfg and installs them as the
// process globals. The caller must defer the returned Providers.Shutdown.
func Setup(ctx context.Context, cfg Config) (*Providers, error) {
	res := newResource(cfg)

	// --- Traces ---
	// Trace exporter: pushes spans to Tempo over OTLP/HTTP. It connects lazily —
	// New() does NOT dial, so a down Tempo never blocks or crashes startup; spans
	// just fail to export later. WithInsecure = plain HTTP (no TLS) on the dev net.
	traceExp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating otlp trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),                // queue spans, export in the background
		sdktrace.WithResource(res),                    // stamp every span with who emitted it
		sdktrace.WithSampler(sdktrace.AlwaysSample()), // dev: keep 100% of traces
	)

	// --- Metrics ---
	// Own registry, not Prometheus's global default, so there's no hidden
	// shared state and it's testable. The OTel Prometheus exporter is a metric
	// Reader that publishes into that registry; promhttp serves it at /metrics.
	reg := prometheus.NewRegistry()
	metricExp, err := otelprom.New(otelprom.WithRegisterer(reg)) // bridge (OTel -> Prometheus)
	if err != nil {
		return nil, fmt.Errorf("creating prometheus metric exporter: %w", err)
	}
	mp := metric.NewMeterProvider(
		metric.WithReader(metricExp),
		metric.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return &Providers{
		MetricsHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		Shutdown: func(ctx context.Context) error {
			// Reverse order, and join both errors so one can't hide the other.
			return errors.Join(mp.Shutdown(ctx), tp.Shutdown(ctx))
		},
	}, nil
}

// newResource builds the identity attached to every signal this process emits.
// We set attributes explicitly rather than merging resource.Default() to avoid
// the schema-URL-mismatch error that Merge raises across semconv versions.
func newResource(cfg Config) *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceInstanceID(cfg.InstanceID),
	)
}
