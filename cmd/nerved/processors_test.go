package main

import (
	"bytes"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/pdfproc"
	"github.com/tunedev/nerve/internal/core/ingest"
)

// The acceptance: which formats a build can ingest is what registered here, and
// a second format was a package plus a line rather than an edit to a switch.
func TestRegisterProcessorsDeclaresTheFormatsThisBuildReads(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registerProcessors(registry); err != nil {
		t.Fatalf("registerProcessors() = %v", err)
	}

	want := []string{"application/pdf", "text/markdown", "text/plain"}
	if got := registry.MediaTypes(); !slices.Equal(got, want) {
		t.Errorf("MediaTypes() = %v, want %v", got, want)
	}
}

// Each type reaches the package that reads it, through the type an upload
// declares rather than through anything that names a format.
func TestEveryRegisteredFormatReachesItsOwnProcessor(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registerProcessors(registry); err != nil {
		t.Fatalf("registerProcessors() = %v", err)
	}

	text, err := registry.For("text/plain; charset=utf-8")
	if err != nil {
		t.Fatalf("For(text/plain) = %v", err)
	}
	got, err := text.Extract(t.Context(), strings.NewReader("a first draft"))
	if err != nil || got != "a first draft" {
		t.Errorf("the text processor read %q, %v, want the words back", got, err)
	}

	document, err := registry.For(pdfproc.MediaType)
	if err != nil {
		t.Fatalf("For(application/pdf) = %v", err)
	}
	if _, err := document.Extract(t.Context(), bytes.NewReader([]byte("not a PDF"))); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("the PDF processor read a file that is not one = %v, want fs.ErrInvalid", err)
	}
}

// A format this build has no package for is a resource nothing reads, and the
// refusal names what it does have.
func TestAFormatThisBuildHasNoPackageForIsRefused(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registerProcessors(registry); err != nil {
		t.Fatalf("registerProcessors() = %v", err)
	}

	_, err := registry.For("image/png")

	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("For(image/png) = %v, want fs.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), "application/pdf") {
		t.Errorf("For(image/png) = %v, want it to name the formats this build reads", err)
	}
}
