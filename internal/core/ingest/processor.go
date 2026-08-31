package ingest

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"mime"
	"slices"
	"strings"
)

// Processor reads one family of formats and returns what it says as text.
//
// It is given a reader rather than a resource, so a processor knows a format
// and nothing about where the bytes came from or what is done with the words
// afterwards. A format whose parser needs the whole file reads the whole file;
// one that can work as the bytes arrive does not have to.
type Processor interface {
	Extract(ctx context.Context, content io.Reader) (string, error)
}

// Registry maps a media type onto the processor that reads it, so a format this
// build can ingest is a package registered under its type rather than a branch
// somewhere that grows one arm per format.
//
// Nothing here knows a concrete processor. Registration happens at composition,
// which is the only place that does, and a registry is written there and read
// afterwards rather than shared across goroutines.
type Registry struct {
	processors map[string]Processor
}

// NewRegistry returns a registry that can read no format. Every type is unknown
// until something registers it.
func NewRegistry() *Registry {
	return &Registry{processors: make(map[string]Processor)}
}

// Register puts processor under every media type it reads. It fails with
// fs.ErrExist on a type already taken, because two processors under one type
// make which one a resource gets depend on registration order.
//
// Types are normalised on the way in, so what is registered and what an upload
// declared are compared as media types rather than as strings.
func (r *Registry) Register(processor Processor, mediaTypes ...string) error {
	for _, declared := range mediaTypes {
		mediaType, err := normalise(declared)
		if err != nil {
			return fmt.Errorf("register processor: %w", err)
		}
		if _, taken := r.processors[mediaType]; taken {
			return fmt.Errorf("register processor for %q: %w", mediaType, fs.ErrExist)
		}
		r.processors[mediaType] = processor
	}
	return nil
}

// For returns the processor reading what contentType names. Anything this build
// cannot read fails with fs.ErrNotExist naming what it can, whether the type is
// one nobody registered or a spelling that is not a media type at all: both are
// the same answer to the caller, and neither becomes readable by being asked
// again.
//
// contentType is what the upload declared, parameters and all: "text/plain;
// charset=utf-8" is the type it names. Matching the header whole would miss
// every upload a browser made.
func (r *Registry) For(contentType string) (Processor, error) {
	mediaType, err := normalise(contentType)
	if err != nil {
		return nil, fmt.Errorf("no processor for %q: it is not a media type; this build reads %s: %w", contentType, r.known(), fs.ErrNotExist)
	}
	processor, known := r.processors[mediaType]
	if !known {
		return nil, fmt.Errorf("no processor for %q; this build reads %s: %w", mediaType, r.known(), fs.ErrNotExist)
	}
	return processor, nil
}

// MediaTypes reports every type this build can read, sorted. It is what the
// composition root logs at boot and what a refusal names.
func (r *Registry) MediaTypes() []string {
	return slices.Sorted(maps.Keys(r.processors))
}

// normalise is the media type contentType names, lowercased and without its
// parameters. Something that is not a media type is refused rather than
// registered under its own spelling and never matched.
func normalise(contentType string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return "", fmt.Errorf("content type %q: %w: %w", contentType, err, fs.ErrInvalid)
	}
	return mediaType, nil
}

// known lists the readable types for an error message.
func (r *Registry) known() string {
	types := r.MediaTypes()
	if len(types) == 0 {
		return "no formats"
	}
	return strings.Join(types, ", ")
}
