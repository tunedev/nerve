// Package telemetry is the OpenTelemetry side of the core's Recorder port: one
// call through one mount becomes a span, a duration measurement and a log line,
// all three carrying the same trace.
//
// Nothing in the core knows this package exists. What it knows is that a call
// began and what the call returned; that this becomes OTLP over gRPC, and where
// it goes, is decided at composition.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// scope is the instrumentation name every signal is emitted under, so a
// collector can tell nerved's own telemetry from a library's.
const scope = "github.com/tunedev/nerve"

// Options are what the process knows and this package does not: where to send
// telemetry, which build is sending it, and how much of the log to keep.
type Options struct {
	// Endpoint is the collector's OTLP gRPC URL. An http:// scheme is a
	// plaintext connection and https:// is a TLS one, which is the same reading
	// the OTel exporters give the standard environment variable. Empty exports
	// nothing.
	Endpoint string
	// Service is the name this process reports itself under.
	Service string
	// Version is the build it reports.
	Version string
	// Level is the lowest log level kept.
	Level slog.Level
}

// Pipeline is the running SDK: what the core records into, what the process
// logs through, and the shutdown that flushes both.
//
// It is a struct rather than three return values because the three are one
// lifetime. Whoever builds it owns ending it, and ending it is what turns a
// batch that has not been sent yet into one that has.
type Pipeline struct {
	// Recorder is what the mount registry reports calls to.
	Recorder vfs.Recorder
	// Logger is the process logger. Its records carry the trace of whatever
	// call they were written under, which is what makes a slow trace and the
	// lines explaining it the same search.
	Logger *slog.Logger

	closers []func(context.Context) error
}

// New builds the pipeline. An empty endpoint builds one that exports nothing:
// the recorder still records, into the API's no-op providers, and the logger
// still writes to stderr. That is the same code path a build exporting to a
// collector takes, which is what keeps the wiring honest on a laptop with
// nothing running.
//
// Whether the collector is up is not a boot question. The exporters connect
// lazily and drop what they cannot send, because a telemetry backend that is
// down must not stop the process it is watching.
//
// Nor is the endpoint checked here. The exporters log a URL they cannot parse
// and quietly keep their own default, so an endpoint that is not a URL would
// export to localhost rather than fail — which is why config validates it
// before this is called, and why validating it here would be the second place
// that has to remember.
func New(ctx context.Context, opts Options) (*Pipeline, error) {
	stderr := levelled(opts.Level)

	if opts.Endpoint == "" {
		logger := slog.New(stderr)
		return &Pipeline{Recorder: newRecorder(logger), Logger: logger}, nil
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(opts.Service),
		semconv.ServiceVersion(opts.Version),
	))
	if err != nil {
		return nil, fmt.Errorf("telemetry resource: %w", err)
	}

	traces, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(opts.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("telemetry traces: %w", err)
	}
	metrics, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(opts.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("telemetry metrics: %w", err)
	}
	logs, err := otlploggrpc.New(ctx, otlploggrpc.WithEndpointURL(opts.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("telemetry logs: %w", err)
	}

	tracer := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traces), sdktrace.WithResource(res))
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics)), sdkmetric.WithResource(res))
	logging := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logs)), sdklog.WithResource(res))

	logger := slog.New(both(stderr, otelslog.NewHandler(scope, otelslog.WithLoggerProvider(logging))))

	return &Pipeline{
		Recorder: newRecorder(logger, withTracer(tracer.Tracer(scope)), withMeter(meter.Meter(scope))),
		Logger:   logger,
		// Traces and metrics are flushed before logs, because shutting a
		// provider down logs, and a log provider already closed drops what it
		// is told.
		closers: []func(context.Context) error{tracer.Shutdown, meter.Shutdown, logging.Shutdown},
	}, nil
}

// Shutdown flushes everything the pipeline is still holding. What is in a batch
// and not yet sent is lost without it, and the last thing a process does is
// usually the thing worth reading.
//
// It reports every failure rather than the first, because a trace that could
// not be flushed and a metric that could not be are two separate things to know
// about a shutdown.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	errs := make([]error, 0, len(p.closers))
	for _, close := range p.closers {
		errs = append(errs, close(ctx))
	}
	return errors.Join(errs...)
}
