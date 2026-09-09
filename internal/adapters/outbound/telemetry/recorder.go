package telemetry

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// duration is the one instrument this package writes. A histogram already
// carries the count and the sum, so "how many calls" and "how long in total"
// are the same series as "how long each" and there is nothing to keep in step.
const duration = "nerve.mount.call.duration"

// recorder turns one call through one mount into a span, a measurement and a
// line. It holds no state about a call: what it needs is in the Call it is
// handed and in the closure it hands back.
type recorder struct {
	tracer trace.Tracer
	took   metric.Float64Histogram
	log    *slog.Logger
}

// option is how New hands the recorder the providers it built, so a pipeline
// that exports nothing and one that exports everything build the same recorder.
type option func(*recorder)

func withTracer(tracer trace.Tracer) option { return func(r *recorder) { r.tracer = tracer } }

func withMeter(meter metric.Meter) option {
	return func(r *recorder) {
		// The instrument is built once here rather than per call: an instrument
		// is a handle on a series, and building one per call builds the same
		// series over and over.
		r.took, _ = meter.Float64Histogram(duration,
			metric.WithUnit("s"),
			metric.WithDescription("time one call through one mount took, as the caller waited for it"))
	}
}

// newRecorder builds the recorder over the API's no-op providers, which the
// options replace when there is somewhere to export to. The no-ops are real
// implementations and not nil, so nothing here asks whether telemetry is on.
func newRecorder(log *slog.Logger, opts ...option) vfs.Recorder {
	took, _ := noop.Meter{}.Float64Histogram(duration)
	rec := &recorder{tracer: tracenoop.NewTracerProvider().Tracer(scope), took: took, log: log}

	for _, apply := range opts {
		apply(rec)
	}
	return rec
}

// Begin opens the span and starts the clock. Everything else happens when the
// call ends, because until then there is nothing to say about it.
//
// The span is named for the verb and the mount and not for the path: a span
// name is a series and a path is not one. The path goes on as an attribute,
// where high cardinality is what a trace is for.
func (r *recorder) Begin(ctx context.Context, call vfs.Call) (context.Context, vfs.Done) {
	started := time.Now()

	ctx, span := r.tracer.Start(ctx, call.Verb+" "+call.Mount,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("nerve.mount", call.Mount),
			attribute.String("nerve.kind", call.Kind),
			attribute.String("nerve.verb", call.Verb),
			attribute.String("nerve.path", call.Path),
		))

	return ctx, func(err error) {
		took := time.Since(started)
		outcome := outcomeOf(err)

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, outcome)
		}
		span.End()

		r.took.Record(ctx, took.Seconds(), metric.WithAttributes(
			attribute.String("nerve.mount", call.Mount),
			attribute.String("nerve.kind", call.Kind),
			attribute.String("nerve.verb", call.Verb),
			attribute.String("nerve.outcome", outcome),
		))

		r.log.LogAttrs(ctx, levelOf(err), "mount call",
			slog.String("mount", call.Mount),
			slog.String("kind", call.Kind),
			slog.String("verb", call.Verb),
			slog.String("path", call.Path),
			slog.Duration("took", took),
			slog.String("outcome", outcome),
			slog.Any("error", err))
	}
}

// ok is what a call that worked is labelled. A failure is labelled by what its
// class already says: whether another attempt might have gone differently is
// the first thing anyone asks of a mount that is failing, and the core has
// already decided it.
const ok = "ok"

func outcomeOf(err error) string {
	if err == nil {
		return ok
	}
	return stability.Classify(err).String()
}

// levelOf keeps the log readable at its default level. A transient failure is
// worth seeing without turning debug on — something is wobbling. A fail-fast
// failure is an answer the caller already has in its return value, and a path
// that does not exist is not an incident.
func levelOf(err error) slog.Level {
	if err != nil && stability.Classify(err) == stability.Retryable {
		return slog.LevelWarn
	}
	return slog.LevelDebug
}
