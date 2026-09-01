// Package content is what a caller reads of a document it has already found.
//
// Finding a document and reading one are different sizes of answer. A retrieval
// layer that returned every match as its whole text would carry a megabyte to
// answer a question a sentence settles, so what comes back is the shortest true
// thing about each one — the abstract — and the caller escalates the one it
// wants: the overview, the detail, the words the document was extracted into,
// and finally the file itself.
//
// The rungs are the ones the ingestion stage wrote, given to the reader rather
// than known by it. Composition hands both the same ladder, so a build that
// summarises to a fourth rung serves it with no edit here, and a reader cannot
// offer a rung nothing writes.
//
// Reading stops at the words. The bytes that carried them leave as a grant
// rather than a stream, because a file crossing the workspace boundary is one
// resource until one moment and nothing else — which is the resource store's
// answer, and this package only puts it at the bottom of the same ladder.
package content

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Document is what a caller learns before it reads anything: what the resource
// is, and which rungs it could escalate to.
//
// Levels is what stands rather than what the ladder allows. A document whose
// ingestion has not run yet has none, which is the difference between "there is
// no cheaper answer" and "the cheaper answer is not written yet".
type Document struct {
	Resource resource.Resource
	Levels   []string
}

// Reader answers for the documents in resources, over the ladder its rungs
// stand on.
type Reader struct {
	resources *resource.Store
	ladder    []string
}

// NewReader returns the reader serving levels, cheapest first, over resources.
//
// It takes the levels the summarisation stage was built with rather than a list
// of its own, so the rungs a caller may escalate to are the rungs something
// writes. The words extraction left are the last rung and are not passed: they
// are not a summary, and every ladder ends at them.
//
// A reader with no levels is refused rather than allowed to become one that
// refuses every call it is ever given, which is a boot-time answer to a
// composition that forgot the ladder.
func NewReader(resources *resource.Store, levels ...ingest.Level) (*Reader, error) {
	if resources == nil {
		return nil, fmt.Errorf("content reader with no resources: %w", fs.ErrInvalid)
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("content reader with no levels: %w", fs.ErrInvalid)
	}
	ladder := make([]string, 0, len(levels)+1)
	for _, level := range levels {
		ladder = append(ladder, level.Kind)
	}
	return &Reader{resources: resources, ladder: append(ladder, ingest.TextSidecar)}, nil
}

// Stat returns what stands for the document named name in s: the resource
// itself, and the rungs that have been written beside it.
//
// It is the call a caller makes to decide whether escalating is worth it, so it
// moves no content: one read of what the resource says about itself, and one
// listing of what stands beside it. A sidecar that is not a rung of this
// ladder is left out — something else wrote it, and it is not somewhere a
// caller can escalate to.
func (r *Reader) Stat(ctx context.Context, s workspace.Scope, name string) (Document, error) {
	stood, err := r.resources.Stat(ctx, s, name)
	if err != nil {
		return Document{}, fmt.Errorf("stat document: %w", err)
	}
	standing, err := r.resources.Derivations(ctx, s, name)
	if err != nil {
		return Document{}, fmt.Errorf("stat document %q: %w", name, err)
	}

	levels := make([]string, 0, len(r.ladder))
	for _, rung := range r.ladder {
		if slices.Contains(standing, rung) {
			levels = append(levels, rung)
		}
	}
	return Document{Resource: stood, Levels: levels}, nil
}

// Abstract returns the cheapest thing this build says about the document named
// name in s. It is L0 on the ladder shipped here, and whatever stands first on
// a ladder composition changed.
//
// It is the answer retrieval hands back for a document it matched: a caller
// that wants the cheapest rung should not have to know which rung is cheapest.
func (r *Reader) Abstract(ctx context.Context, s workspace.Scope, name string) (string, error) {
	return r.Level(ctx, s, name, r.ladder[0])
}

// Level returns the rung named kind of the document named name in s. It is the
// escalation: the overview and the detail are this verb with a different rung,
// which is what makes a ladder of four the same code as a ladder of three.
//
// A kind that is not on the ladder is refused with fs.ErrInvalid rather than
// read. A sidecar something else left beside the document — an embedding, a
// projection — is not a rung, and a reader that served it would be a way to
// read any file a resource has by naming it.
//
// A rung nothing has written reports fs.ErrNotExist, which is what a document
// whose ingestion has not run looks like from here.
func (r *Reader) Level(ctx context.Context, s workspace.Scope, name, kind string) (string, error) {
	if !slices.Contains(r.ladder, kind) {
		return "", fmt.Errorf("level %q is not a rung of this ladder: %w", kind, fs.ErrInvalid)
	}
	data, err := r.resources.Derived(ctx, s, name, kind)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Read returns the document named name in s as the words extraction left,
// whatever format they arrived in. It is the last rung: everything above it
// summarises this, and below it there is only the file.
func (r *Reader) Read(ctx context.Context, s workspace.Scope, name string) (string, error) {
	return r.Level(ctx, s, name, ingest.TextSidecar)
}

// Download returns the grant letting its holder take the file the document was
// uploaded as, for ttl.
//
// It is past the end of the ladder and is the only rung that is not a read: the
// bytes are the part that does not fit in a call, so what comes back names one
// resource until one moment and is redeemed against the store.
func (r *Reader) Download(ctx context.Context, s workspace.Scope, name string, ttl time.Duration) (resource.Grant, error) {
	return r.resources.SignedDownload(ctx, s, name, ttl)
}
