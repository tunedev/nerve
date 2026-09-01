package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/core/ingest"
)

// budgets is a summariser that answers with what it was given, so a test can
// see both halves of the call: the text the stage found and the budget the
// level asked for.
type budgets struct{}

func (budgets) Summarise(_ context.Context, text string, sentences int) (string, error) {
	return fmt.Sprintf("%d: %s", sentences, text), nil
}

// unable is a summariser that cannot answer, which is what a summariser over a
// provider that is down looks like.
type unable struct{ err error }

func (u unable) Summarise(context.Context, string, int) (string, error) { return "", u.err }

// extracted is the pipeline as far as this stage needs it: the extraction that
// leaves the text, and the summarisation that reads it.
func extracted(t *testing.T, b *bus, summariser ingest.Summariser, levels ...ingest.Level) {
	t.Helper()

	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b.pipeline = ingest.New(b.resources, b.ledger,
		ingest.Extraction(b.resources, registry),
		ingest.Summarisation(b.resources, summariser, levels...))
}

// The acceptance: every ingested document has an abstract, an overview and a
// detail, each summarising the words extraction left and each asking for as
// much of the document as its rung is.
func TestEveryIngestedDocumentHasAbstractOverviewAndDetail(t *testing.T) {
	b := newBus(t)
	extracted(t, b, budgets{}, ingest.Levels...)

	b.upload(t, "transcript.txt", "a first draft")
	b.work(t)

	for _, level := range ingest.Levels {
		want := fmt.Sprintf("%d: A FIRST DRAFT", level.Sentences)
		if got := b.derived(t, "transcript.txt", level.Kind); got != want {
			t.Errorf("%s = %q, want %q", level.Kind, got, want)
		}
	}
}

// The ladder is data. A build that wants a fourth rung passes a fourth level at
// composition, and nothing in the stage names a level.
func TestARungOfTheLadderIsALineAtComposition(t *testing.T) {
	b := newBus(t)
	extracted(t, b, budgets{}, ingest.Level{Kind: "headline", Sentences: 1})

	b.upload(t, "transcript.txt", "a first draft")
	b.work(t)

	if got, want := b.derived(t, "transcript.txt", "headline"), "1: A FIRST DRAFT"; got != want {
		t.Errorf("headline = %q, want %q", got, want)
	}
	if _, err := b.resources.Derived(t.Context(), here, "transcript.txt", ingest.AbstractSidecar); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("abstract = %v, want no sidecar: no level asked for one", err)
	}
}

// A document nothing could read has no words to summarise. It is the same
// answer extraction gave — nothing to do, and not a failure — because the job
// claimed again would reach it again.
func TestSummarisationLeavesADocumentNothingExtracted(t *testing.T) {
	b := newBus(t)
	extracted(t, b, budgets{}, ingest.Levels...)

	b.uploadAs(t, "diagram.png", "image/png", "not text at all")
	b.work(t)

	for _, level := range ingest.Levels {
		if _, err := b.resources.Derived(t.Context(), here, "diagram.png", level.Kind); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s of diagram.png = %v, want no sidecar at all", level.Kind, err)
		}
	}
}

// A summariser that could not answer is a stage that failed: the document was
// meant to have an abstract and does not, which is what E6.6 reports.
func TestSummarisationFailsWhenTheSummariserCannotAnswer(t *testing.T) {
	down := errors.New("the provider is not answering")
	b := newBus(t)
	extracted(t, b, unable{err: down}, ingest.Levels...)

	b.upload(t, "transcript.txt", "a first draft")
	err := b.worker(t).Run(t.Context())

	if !errors.Is(err, down) {
		t.Fatalf("Run = %v, want %v", err, down)
	}
	if !strings.Contains(err.Error(), ingest.SummariseStage) {
		t.Errorf("Run = %v, want it to name the stage", err)
	}
	if got := b.indexing(t, "transcript.txt"); got.Status != ingest.Failed || got.Stage != ingest.SummariseStage {
		t.Errorf("indexing = %+v, want failed at %q", got, ingest.SummariseStage)
	}
}

// Summarising the same resource twice leaves one answer, because a job claimed
// again after a crash must not double what it wrote.
func TestSummarisationRunAgainReplacesWhatTheLastRunWrote(t *testing.T) {
	b := newBus(t)
	extracted(t, b, budgets{}, ingest.Levels...)

	b.upload(t, "transcript.txt", "a first draft")
	for range 2 {
		if err := b.pipeline.Ingest(t.Context(), "resources/ada/hunt/transcript.txt/meta.json"); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}

	if got, want := b.derived(t, "transcript.txt", ingest.AbstractSidecar), "1: A FIRST DRAFT"; got != want {
		t.Errorf("abstract = %q, want %q", got, want)
	}
}
