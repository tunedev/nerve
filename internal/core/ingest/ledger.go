package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Ledger is where the ingestion of every document in every workspace stands,
// kept on the tree under root and reached by the document's name inside its own
// scope.
//
// It is what a caller asks whether its upload is searchable yet. The pipeline
// writes to it as it runs; nothing else does, and nothing here runs a stage.
//
// The latest record is the one that stands, with no rule about which status may
// follow which. A worker that died mid-run leaves a document saying it is
// working, and the retry that claims the job again has to be able to finish it.
type Ledger struct {
	fsys vfs.FileSystem
	root string
	now  func() time.Time
}

// NewLedger returns the ledger keeping records under root on fsys, stamping
// each one with now.
//
// root must be a rooted, clean path such as "/ingest" — the mount table's rule
// for a prefix, because a ledger is where a mount was put. It is its own root
// rather than a file beside the resource it is about: where a resource stands
// is the resource store's answer and only its answer, and a record written into
// that tree would be published to whatever is watching it for resources
// arriving.
func NewLedger(fsys vfs.FileSystem, root string, now func() time.Time) (*Ledger, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("ingest root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	return &Ledger{fsys: fsys, root: root, now: now}, nil
}

// Working records that the ingestion of the document named name in s has
// started.
func (l *Ledger) Working(ctx context.Context, s workspace.Scope, name string) error {
	return l.write(ctx, s, name, record{Status: Working})
}

// Indexed records that every stage ran over the document named name in s, which
// is the moment it became searchable.
func (l *Ledger) Indexed(ctx context.Context, s workspace.Scope, name string) error {
	return l.write(ctx, s, name, record{Status: Indexed})
}

// Failed records that the ingestion of the document named name in s stopped at
// stage, and why.
//
// Both are recorded because either alone leaves the caller guessing at the
// other: a reason with no stage does not say how much of the document was
// processed before it, and a stage with no reason does not say whether retrying
// could help.
func (l *Ledger) Failed(ctx context.Context, s workspace.Scope, name, stage, because string) error {
	return l.write(ctx, s, name, record{Status: Failed, Stage: stage, Failure: because})
}

// Get returns where the ingestion of the document named name in s stands.
//
// A document with no record is Pending rather than a refusal, because "it is
// not searchable yet" is what the caller asked and not an error it has to
// translate. That is also all the ledger can say: whether the document exists
// at all is the resource store's answer.
func (l *Ledger) Get(ctx context.Context, s workspace.Scope, name string) (Indexing, error) {
	fsys, at, err := l.at(s, name)
	if err != nil {
		return Indexing{}, fmt.Errorf("get indexing: %w", err)
	}
	data, err := fsys.Read(ctx, at)
	if errors.Is(err, fs.ErrNotExist) {
		return Indexing{Name: name, Status: Pending}, nil
	}
	if err != nil {
		return Indexing{}, fmt.Errorf("get indexing of %q: %w", name, err)
	}
	held, err := decode(data)
	if err != nil {
		return Indexing{}, fmt.Errorf("get indexing of %q: %w", name, err)
	}
	return Indexing{
		Name:    name,
		Status:  held.Status,
		Stage:   held.Stage,
		Failure: held.Failure,
		At:      held.At,
	}, nil
}

// write replaces the record standing for name in s, stamped with the moment it
// was written, making the scope's directory first — which is what a tree needs
// and what a key space has nothing to do about.
func (l *Ledger) write(ctx context.Context, s workspace.Scope, name string, held record) error {
	fsys, at, err := l.at(s, name)
	if err != nil {
		return fmt.Errorf("record indexing: %w", err)
	}
	held.At = l.now()
	data, err := encode(held)
	if err != nil {
		return fmt.Errorf("record indexing of %q: %w", name, err)
	}
	if err := fsys.Mkdir(ctx, path.Dir(at)); err != nil {
		return fmt.Errorf("record indexing of %q: %w", name, err)
	}
	if err := fsys.Write(ctx, at, data); err != nil {
		return fmt.Errorf("record indexing of %q: %w", name, err)
	}
	return nil
}

// at is the ledger as a caller inside s sees it, and where the record for the
// document named name stands in it.
//
// The view is what makes the scope a boundary rather than a prefix: the name is
// resolved by the subtree the tree beneath hands out, so a link standing at any
// component of the scope path is refused instead of followed. A path derived
// down to the record and handed to the whole area is resolved by the whole
// area, which follows that link.
//
// User scope is refused: a scope with no workspace derives the path holding
// every workspace that user has, so a record written there would stand where a
// workspace belongs.
func (l *Ledger) at(s workspace.Scope, name string) (vfs.FileSystem, string, error) {
	if err := vfs.ValidSegment("document name", name); err != nil {
		return nil, "", err
	}
	if s.Workspace == "" {
		return nil, "", fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	held, err := s.View(l.fsys, l.root)
	if err != nil {
		return nil, "", err
	}
	return held, path.Join("/", name), nil
}
