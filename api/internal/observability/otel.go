// Package observability wires up OpenTelemetry tracer and meter providers
// for the users-api service. It exports via OTLP HTTP and falls back to
// no-op providers when no exporter endpoint is configured (local dev).
package observability

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	nooptrace "go.opentelemetry.io/otel/trace/noop"

	"github.com/moduleforge/users-module/api/internal/config"
)

// ShutdownFunc flushes and shuts down all OTel providers. Callers should
// invoke it with a context that carries the graceful-shutdown deadline.
type ShutdownFunc func(context.Context) error

// Init sets up the global OTel tracer and meter providers. If
// cfg.OTel.ExporterEndpoint is empty (and the standard
// OTEL_EXPORTER_OTLP_ENDPOINT env var is also unset), Init installs
// no-op providers so callers never need to nil-check and returns a
// no-op shutdown function.
//
// On success, Init returns a ShutdownFunc that the caller must invoke
// during graceful shutdown to flush pending telemetry.
func Init(ctx context.Context, cfg *config.Config) (ShutdownFunc, error) {
	if cfg.OTel.ExporterEndpoint == "" {
		slog.InfoContext(ctx, "otel: no exporter endpoint configured, running in no-op mode")
		installNoopProviders()
		return func(context.Context) error { return nil }, nil
	}

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}

	tracerShutdown, err := initTracer(ctx, cfg, res)
	if err != nil {
		return nil, fmt.Errorf("observability: init tracer: %w", err)
	}

	meterShutdown, err := initMeter(ctx, cfg, res)
	if err != nil {
		// Best-effort cleanup of the tracer we already started.
		_ = tracerShutdown(ctx)
		return nil, fmt.Errorf("observability: init meter: %w", err)
	}

	shutdown := func(ctx context.Context) error {
		var errs []error
		if err := tracerShutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer shutdown: %w", err))
		}
		if err := meterShutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter shutdown: %w", err))
		}
		if len(errs) > 0 {
			return fmt.Errorf("observability shutdown errors: %v", errs)
		}
		return nil
	}

	return shutdown, nil
}

// buildResource constructs an OTel resource describing this service
// instance with the semantic conventions for service name and
// deployment environment.
func buildResource(ctx context.Context, cfg *config.Config) (*sdkresource.Resource, error) {
	return sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(cfg.OTel.ServiceName),
			semconv.DeploymentEnvironment(string(cfg.DeployMode)),
		),
		sdkresource.WithProcess(),
		sdkresource.WithOS(),
		sdkresource.WithHost(),
	)
}

// initTracer creates an OTLP HTTP trace exporter, wires it into an SDK
// TracerProvider, and sets it as the global tracer. It returns a shutdown
// function scoped to the trace provider.
func initTracer(ctx context.Context, cfg *config.Config, res *sdkresource.Resource) (func(context.Context) error, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(cfg.OTel.ExporterEndpoint),
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// initMeter creates an OTLP HTTP metric exporter, wires it into an SDK
// MeterProvider with periodic collection, and sets it as the global
// meter. It returns a shutdown function scoped to the meter provider.
func initMeter(ctx context.Context, cfg *config.Config, res *sdkresource.Resource) (func(context.Context) error, error) {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(cfg.OTel.ExporterEndpoint),
	}

	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(res),
	)

	otel.SetMeterProvider(mp)

	return mp.Shutdown, nil
}

// installNoopProviders sets the global tracer and meter to no-op
// implementations so that code which calls otel.Tracer() or
// otel.Meter() never receives a nil and doesn't need to guard against it.
func installNoopProviders() {
	otel.SetTracerProvider(nooptrace.NewTracerProvider())
	otel.SetMeterProvider(noop.NewMeterProvider())
}
