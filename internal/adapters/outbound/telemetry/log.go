package telemetry

import (
	"context"
	"log/slog"
	"os"
)

// levelled is the log an operator reads on the box: JSON on stderr, from the
// configured level up. It is always present, collector or no collector,
// because the first thing anyone does with a process that is misbehaving is
// read its output.
//
// Durations are written the way they are configured — "10s" rather than the
// nanoseconds slog encodes by default. This side is read by a person; the
// collector keeps the number, which is what a query wants.
func levelled(level slog.Level) slog.Handler {
	return slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level, ReplaceAttr: spelledOut})
}

// spelledOut renders a duration as the string an operator would have written.
func spelledOut(_ []string, attr slog.Attr) slog.Attr {
	if attr.Value.Kind() == slog.KindDuration {
		return slog.String(attr.Key, attr.Value.Duration().String())
	}
	return attr
}

// both writes every record to two handlers, which is how a line reaches the
// collector and the terminal at once. Sending logs only to the collector means
// a collector that is down takes the logs with it, and that is exactly when
// they are wanted.
func both(handlers ...slog.Handler) slog.Handler { return fanout(handlers) }

// fanout is a handler over handlers. It is a slice rather than a type with two
// fields because the composition is the same for two as for any number.
type fanout []slog.Handler

// Enabled reports whether any handler wants the level. One handler filtering a
// record out must not decide for another with a different threshold.
func (f fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle gives the record to every handler that wants it. A handler that
// refuses one is not an error: it is the level doing its job.
//
// What a handler reports is dropped. The failure a logger reports has nowhere
// to be reported to, and a write to stderr that failed will not succeed on a
// second telling.
func (f fanout) Handle(ctx context.Context, record slog.Record) error {
	for _, h := range f {
		if h.Enabled(ctx, record.Level) {
			_ = h.Handle(ctx, record.Clone())
		}
	}
	return nil
}

// WithAttrs and WithGroup are applied to every handler, so a logger derived
// once writes the same thing to both sides.
func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make(fanout, len(f))
	for i, h := range f {
		next[i] = h.WithAttrs(attrs)
	}
	return next
}

func (f fanout) WithGroup(name string) slog.Handler {
	next := make(fanout, len(f))
	for i, h := range f {
		next[i] = h.WithGroup(name)
	}
	return next
}
