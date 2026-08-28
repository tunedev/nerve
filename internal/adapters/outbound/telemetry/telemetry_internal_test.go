package telemetry

// The recorder is only reachable with the SDK providers wired into it, and
// wiring them is what these tests are checking. That is what an internal test
// file is for: the assertions are about the seam between the port and the SDK,
// and the seam is not exported.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// wired is a recorder over real providers with nothing between them and the
// test: spans land in memory, metrics are collected on demand, and the log is a
// buffer. It is the pipeline New builds, minus the exporters.
type wired struct {
	recorder vfs.Recorder
	spans    *tracetest.InMemoryExporter
	reader   *sdkmetric.ManualReader
	log      *bytes.Buffer
}

func newWired(t *testing.T, level slog.Level) wired {
	t.Helper()

	spans := tracetest.NewInMemoryExporter()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	t.Cleanup(func() {
		if err := tracer.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer shutdown: %v", err)
		}
	})

	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := meter.Shutdown(context.Background()); err != nil {
			t.Errorf("meter shutdown: %v", err)
		}
	})

	log := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(log, &slog.HandlerOptions{Level: level}))

	return wired{
		recorder: newRecorder(logger, withTracer(tracer.Tracer(scope)), withMeter(meter.Meter(scope))),
		spans:    spans,
		reader:   reader,
		log:      log,
	}
}

// took returns the one histogram the recorder writes, collected now.
func (w wired) took(t *testing.T) metricdata.HistogramDataPoint[float64] {
	t.Helper()

	var collected metricdata.ResourceMetrics
	if err := w.reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, scoped := range collected.ScopeMetrics {
		for _, m := range scoped.Metrics {
			if m.Name != duration {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s is %T, want a float histogram", duration, m.Data)
			}
			if len(hist.DataPoints) != 1 {
				t.Fatalf("%s has %d data points, want 1", duration, len(hist.DataPoints))
			}
			return hist.DataPoints[0]
		}
	}

	t.Fatalf("no %s was recorded; a call nobody measured is a call nobody can explain", duration)
	return metricdata.HistogramDataPoint[float64]{}
}

// lines returns the log as records.
func (w wired) lines(t *testing.T) []map[string]any {
	t.Helper()

	var out []map[string]any
	for line := range bytes.Lines(w.log.Bytes()) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, record)
	}
	return out
}

// read is the call every test here reports.
var read = vfs.Call{Mount: "/data", Kind: "localfs", Verb: "Read", Path: "/notes/one"}

// newWiredCall runs one call through a wired recorder and returns what came out
// of it. One call is one span, one measurement and one line, and each test below
// asserts about one of the three rather than repeating the setup.
func newWiredCall(t *testing.T, level slog.Level, err error) wired {
	t.Helper()

	w := newWired(t, level)
	ctx, done := w.recorder.Begin(context.Background(), read)
	if !trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("the context the recorder returned carries no span; nothing the call goes on to do is part of the trace")
	}
	done(err)
	return w
}

func TestTheSpanNamesTheVerbTheMountAndThePath(t *testing.T) {
	w := newWiredCall(t, slog.LevelDebug, nil)

	spans := w.spans.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}

	if spans[0].Name != "Read /data" {
		t.Errorf("span name = %q, want %q; a span name is a series and a path is not one", spans[0].Name, "Read /data")
	}
	if spans[0].SpanKind != trace.SpanKindClient {
		t.Errorf("span kind = %v, want %v", spans[0].SpanKind, trace.SpanKindClient)
	}

	want := map[attribute.Key]string{
		"nerve.mount": read.Mount,
		"nerve.kind":  read.Kind,
		"nerve.verb":  read.Verb,
		"nerve.path":  read.Path,
	}
	got := make(map[attribute.Key]string, len(spans[0].Attributes))
	for _, attr := range spans[0].Attributes {
		got[attr.Key] = attr.Value.AsString()
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("span attribute %s = %q, want %q", key, got[key], value)
		}
	}
}

func TestACallThatWorkedIsMeasuredAsOk(t *testing.T) {
	w := newWiredCall(t, slog.LevelDebug, nil)

	point := w.took(t)
	labels := point.Attributes

	if point.Count != 1 {
		t.Errorf("measurements = %d, want 1", point.Count)
	}
	if outcome, _ := labels.Value("nerve.outcome"); outcome.AsString() != ok {
		t.Errorf("outcome = %q, want %q", outcome.AsString(), ok)
	}
	if mount, _ := labels.Value("nerve.mount"); mount.AsString() != read.Mount {
		t.Errorf("mount = %q, want %q", mount.AsString(), read.Mount)
	}
	if _, named := labels.Value("nerve.path"); named {
		t.Error("the path is a metric attribute; one series per path is one series per file")
	}
}

// A failure is measured under the class the core already put it in. Whether
// another attempt might have gone differently is the first thing anyone asks of
// a mount that is failing, and it is a label rather than a search.
func TestAFailureIsMeasuredUnderItsClass(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		outcome string
		level   slog.Level
	}{
		{"transient", fmt.Errorf("read: %w", vfs.ErrTransient), "retryable", slog.LevelWarn},
		{"answer", fmt.Errorf("read: %w", errors.New("no such file")), "fail-fast", slog.LevelDebug},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWiredCall(t, slog.LevelDebug, tc.err)

			labels := w.took(t).Attributes
			if outcome, _ := labels.Value("nerve.outcome"); outcome.AsString() != tc.outcome {
				t.Errorf("outcome = %q, want %q", outcome.AsString(), tc.outcome)
			}

			spans := w.spans.GetSpans()
			if spans[0].Status.Code != codes.Error {
				t.Errorf("span status = %v, want %v", spans[0].Status.Code, codes.Error)
			}
			if len(spans[0].Events) == 0 {
				t.Error("the span carries no event; the error the call returned is nowhere in the trace")
			}

			lines := w.lines(t)
			if len(lines) != 1 {
				t.Fatalf("wrote %d log lines, want 1", len(lines))
			}
			if level := lines[0]["level"]; level != tc.level.String() {
				t.Errorf("log level = %v, want %v", level, tc.level)
			}
		})
	}
}

// The line says what the call was, so a slow trace and the lines explaining it
// answer the same question.
func TestTheLineNamesTheCall(t *testing.T) {
	w := newWiredCall(t, slog.LevelDebug, nil)

	lines := w.lines(t)
	if len(lines) != 1 {
		t.Fatalf("wrote %d log lines, want 1", len(lines))
	}

	for key, want := range map[string]any{
		"mount":   read.Mount,
		"kind":    read.Kind,
		"verb":    read.Verb,
		"path":    read.Path,
		"outcome": ok,
	} {
		if lines[0][key] != want {
			t.Errorf("log %s = %v, want %v", key, lines[0][key], want)
		}
	}
	if _, timed := lines[0]["took"]; !timed {
		t.Error("the line does not say how long the call took")
	}
}

// A call that worked is not news. It is kept at debug so that a log at its
// default level is the calls that went wrong.
func TestASuccessfulCallIsQuietAtTheDefaultLevel(t *testing.T) {
	w := newWiredCall(t, slog.LevelInfo, nil)

	if lines := w.lines(t); len(lines) != 0 {
		t.Errorf("wrote %d log lines at info, want none", len(lines))
	}
}

// A build with nowhere to export to takes the same path as one exporting
// everything: the recorder records, into providers that do nothing with it.
func TestAPipelineWithNoEndpointRecordsAndLogs(t *testing.T) {
	signals, err := New(context.Background(), Options{Service: "nerved", Version: "test", Level: slog.LevelDebug})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, done := signals.Recorder.Begin(context.Background(), read)
	done(nil)

	if trace.SpanContextFromContext(ctx).IsValid() {
		t.Error("a pipeline exporting nothing produced a span context; there is nothing to correlate it with")
	}
	if err := signals.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown of a pipeline with nothing to flush: %v", err)
	}
}

// The terminal is read by a person. slog encodes a duration as nanoseconds,
// which is not a self-explaining log.
func TestDurationsAreSpelledOutForTheTerminal(t *testing.T) {
	spelled := spelledOut(nil, slog.Duration("took", 10*time.Second))

	if spelled.Value.Kind() != slog.KindString || spelled.Value.String() != "10s" {
		t.Errorf("took rendered as %v, want %q", spelled.Value, "10s")
	}
}

func TestFanoutWritesToEveryHandlerThatWantsTheRecord(t *testing.T) {
	loud, quiet := &bytes.Buffer{}, &bytes.Buffer{}
	logger := slog.New(both(
		slog.NewJSONHandler(loud, &slog.HandlerOptions{Level: slog.LevelDebug}),
		slog.NewJSONHandler(quiet, &slog.HandlerOptions{Level: slog.LevelError}),
	))

	logger.With("mount", "/data").Debug("one")

	if !bytes.Contains(loud.Bytes(), []byte(`"mount":"/data"`)) {
		t.Errorf("the handler that wanted the record got %q", loud)
	}
	if quiet.Len() != 0 {
		t.Errorf("the handler that did not want the record got %q", quiet)
	}
}
