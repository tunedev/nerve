package ingest_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/core/ingest"
)

// upper is a processor standing in for a format package, doing something to
// the bytes that is visible in the answer.
type upper struct{}

func (upper) Extract(_ context.Context, content io.Reader) (string, error) {
	read, err := io.ReadAll(content)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(string(read)), nil
}

func TestRegistryAnswersForTheTypeAProcessorWasRegisteredUnder(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	processor, err := registry.For("text/plain")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	got, err := processor.Extract(t.Context(), strings.NewReader("a first draft"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if want := "A FIRST DRAFT"; got != want {
		t.Errorf("Extract = %q, want %q", got, want)
	}
}

// A content type carries parameters and the type it names is the same one.
// Matching the whole header would miss every browser upload.
func TestRegistryIgnoresTheParametersOnAContentType(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	for _, declared := range []string{"text/plain; charset=utf-8", "TEXT/PLAIN", " text/plain "} {
		if _, err := registry.For(declared); err != nil {
			t.Errorf("For(%q) = %v, want the text processor", declared, err)
		}
	}
}

// A format nobody registered is a resource this build cannot read, and the
// error says what it can, because that is what an operator acts on.
func TestRegistryRefusesATypeNobodyRegisteredAndNamesWhatItHas(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := registry.For("application/pdf")

	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("For = %v, want fs.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), "text/plain") {
		t.Errorf("For = %v, want it to name the types this build has", err)
	}
}

// Two processors under one type make which one a resource gets depend on
// registration order, the same trade vfs.Registry refuses.
func TestRegistryRefusesATypeAlreadyTaken(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := registry.Register(upper{}, "text/plain"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Register twice = %v, want fs.ErrExist", err)
	}
}

func TestRegistryReportsTheTypesThisBuildCanRead(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/markdown", "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(upper{}, "application/pdf"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	want := []string{"application/pdf", "text/markdown", "text/plain"}
	if got := registry.MediaTypes(); !slices.Equal(got, want) {
		t.Errorf("MediaTypes = %v, want %v", got, want)
	}
}

// A media type that is not one is a registration mistake, and boot is the last
// place anything can act on it.
func TestRegistryRefusesSomethingThatIsNotAMediaType(t *testing.T) {
	registry := ingest.NewRegistry()

	if err := registry.Register(upper{}, "text plain"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Register = %v, want fs.ErrInvalid", err)
	}
}

// A resource declaring something that is not a media type is one nothing can
// read, which is the same answer as a format nobody registered: asking again
// does not make it readable.
func TestRegistryRefusesAContentTypeThatIsNotOne(t *testing.T) {
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := registry.For("text plain"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("For = %v, want fs.ErrNotExist", err)
	}
}
