package ingest_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/textproc"
	"github.com/tunedev/nerve/internal/core/ingest"
)

// refuses is a processor that cannot read what it was handed, which is what a
// corrupt file of a format this build does know looks like.
type refuses struct{ err error }

func (r refuses) Extract(context.Context, io.Reader) (string, error) { return "", r.err }

// The acceptance: which processor read a resource is the type it declared and
// the package registered under it. Nothing between the upload and the text
// names a format.
func TestExtractionRunsTheProcessorRegisteredForTheDeclaredType(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := newBus(t)
	b.pipeline = ingest.New(b.resources, b.ledger, ingest.Extraction(b.resources, registry))

	b.upload(t, "transcript.txt", "a first draft")
	b.work(t)

	if got := b.derived(t, "transcript.txt", ingest.TextSidecar); got != "A FIRST DRAFT" {
		t.Errorf("text = %q, want %q", got, "A FIRST DRAFT")
	}
}

// Adding a format is registering a package under its type. Nothing else in the
// pipeline changes, which is what makes the second format cost what the first
// one did.
func TestASecondFormatIsASecondRegistrationAndNothingElse(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(reversed{}, "application/x-backwards"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := newBus(t)
	b.pipeline = ingest.New(b.resources, b.ledger, ingest.Extraction(b.resources, registry))

	b.uploadAs(t, "transcript.txt", "text/plain", "a first draft")
	b.uploadAs(t, "mirror.bin", "application/x-backwards", "a first draft")
	b.work(t)

	if got := b.derived(t, "transcript.txt", ingest.TextSidecar); got != "A FIRST DRAFT" {
		t.Errorf("text of transcript.txt = %q, want %q", got, "A FIRST DRAFT")
	}
	if got := b.derived(t, "mirror.bin", ingest.TextSidecar); got != "tfard tsrif a" {
		t.Errorf("text of mirror.bin = %q, want %q", got, "tfard tsrif a")
	}
}

// A format this build cannot read is not a job that failed. Nothing extracted
// it, no sidecar says it did, and the next run of the same job would reach the
// same answer however many times it ran.
func TestExtractionLeavesAFormatThisBuildCannotRead(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := newBus(t)
	b.pipeline = ingest.New(b.resources, b.ledger, ingest.Extraction(b.resources, registry))

	b.uploadAs(t, "diagram.png", "image/png", "not text at all")
	b.work(t)

	if _, err := b.resources.Derived(t.Context(), here, "diagram.png", ingest.TextSidecar); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("text of diagram.png = %v, want no sidecar at all", err)
	}
}

// A processor that could not read a file of its own format is a stage that
// failed, and the run says so: this is the case E6.6 has to be able to report.
func TestExtractionFailsWhenTheProcessorForTheFormatCannotReadIt(t *testing.T) {
	corrupt := errors.New("the header is not a header")
	registry := ingest.NewRegistry()
	if err := registry.Register(refuses{err: corrupt}, "application/pdf"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := newBus(t)
	b.pipeline = ingest.New(b.resources, b.ledger, ingest.Extraction(b.resources, registry))

	b.uploadAs(t, "paper.pdf", "application/pdf", "%PDF-1.4 and then nonsense")
	err := b.worker(t).Run(t.Context())

	if !errors.Is(err, corrupt) {
		t.Fatalf("Run = %v, want %v", err, corrupt)
	}
	if !strings.Contains(err.Error(), "paper.pdf") {
		t.Errorf("Run = %v, want it to name the resource", err)
	}
}

// Extracting the same resource twice leaves one answer, because a job claimed
// again after a crash must not double what it wrote.
func TestExtractionRunAgainReplacesWhatTheLastRunWrote(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := newBus(t)
	stage := ingest.Extraction(b.resources, registry)
	b.pipeline = ingest.New(b.resources, b.ledger, stage)

	b.upload(t, "transcript.txt", "a first draft")
	for range 2 {
		if err := b.pipeline.Ingest(t.Context(), "resources/ada/hunt/transcript.txt/meta.json"); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}

	if got := b.derived(t, "transcript.txt", ingest.TextSidecar); got != "A FIRST DRAFT" {
		t.Errorf("text = %q, want %q", got, "A FIRST DRAFT")
	}
}

// reversed is a second format's package, standing in for one.
type reversed struct{}

func (reversed) Extract(_ context.Context, content io.Reader) (string, error) {
	read, err := io.ReadAll(content)
	if err != nil {
		return "", err
	}
	backwards := []byte(read)
	for i, j := 0, len(backwards)-1; i < j; i, j = i+1, j-1 {
		backwards[i], backwards[j] = backwards[j], backwards[i]
	}
	return string(backwards), nil
}

// The loop end to end, with a real format package on the far end of it: a file
// is uploaded, the caller goes away, and what the worker leaves behind is the
// document's words.
//
// The format is the one whose parsing is trivial on purpose. What a PDF parser
// does is asserted by that package's own suite; what is asserted here is the
// trip from the type a resource declared to the package registered under it.
func TestAnUploadedFileBecomesItsTextWithNobodyWaiting(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(textproc.New(), textproc.MediaTypes...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := newBus(t)
	b.pipeline = ingest.New(b.resources, b.ledger, ingest.Extraction(b.resources, registry))

	b.uploadAs(t, "notes.md", "text/markdown", "# a first draft\n\nand a second line\n")
	if _, err := b.resources.Derived(t.Context(), here, "notes.md", ingest.TextSidecar); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("text before the worker ran = %v, want none: the caller waited for none of this", err)
	}

	b.work(t)

	if got, want := b.derived(t, "notes.md", ingest.TextSidecar), "# a first draft\n\nand a second line\n"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}
