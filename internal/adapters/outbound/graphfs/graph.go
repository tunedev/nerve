package graphfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"time"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// document is one text the graph was given and what an extractor read out of
// it. The graph holds and hands back copies, so the slice a caller wrote is not
// the one kept and the one it reads is not the one held.
//
// The text is kept beside the extraction rather than thrown away, for two
// reasons: a caller that wrote a text can read it back, which is what makes the
// texts an ordinary place in the tree; and an extraction is only ever as good
// as the endpoint that made it, so the thing worth keeping is the text.
//
// The fields are exported because this is also the persisted form. There is one
// shape rather than a second one for the blob, so nothing can be kept that
// cannot be loaded.
type document struct {
	Body    []byte
	Mode    fs.FileMode
	Written time.Time
	Read    llm.Extraction
}

// graph is the whole of what a mount holds, and the whole of what its blob
// says.
//
// The documents are the record. Nodes and edges are derived from them on the
// way out rather than kept beside them, so there is no second structure that
// can disagree with the first about what a document said.
type graph struct {
	Documents map[string]document

	// Standing is the label of the snapshot this graph was set back to, and
	// when. It is emptied by every other change, so a graph that names one has
	// had nothing written to it since: the claim is exact rather than a note of
	// where the contents once came from.
	Standing string
	Restored time.Time
}

// load reads the graph kept under name. A name with nothing under it is a graph
// with nothing in it, which is the first boot and not a failure.
func load(ctx context.Context, store Store, name string) (graph, error) {
	body, err := store.Load(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return graph{Documents: map[string]document{}}, nil
	}
	if err != nil {
		return graph{}, err
	}

	return decode(body, name)
}

// decode reads a blob back as the graph it was kept as. It is what load does
// and what a restore does to a snapshot, so a blob this build cannot read is
// refused the same way wherever it came from.
func decode(body []byte, name string) (graph, error) {
	var held graph
	if err := json.Unmarshal(body, &held); err != nil {
		return graph{}, fmt.Errorf("graph %q is kept as something this build cannot read: %w", name, err)
	}
	if held.Documents == nil {
		held.Documents = map[string]document{}
	}
	return held, nil
}

// commit applies change to a copy of the graph, writes the whole of the result
// to the store, and only then makes it the graph this mount answers from.
//
// That order is the whole of the durability story: the blob is the record, so a
// save that failed is a change that did not happen, here or anywhere. The
// alternative — change memory and then try to persist it — leaves a mount whose
// answers no restart can reproduce, which is worse than a write that failed
// where the caller can see it.
//
// The copy is what makes it possible. It is shallow, which is enough: a
// document is replaced rather than edited, so nothing the copy shares is ever
// written through.
func (f *FS) commit(ctx context.Context, change func(map[string]document) error) error {
	return f.save(ctx, func(next *graph) error { return change(next.Documents) })
}

// save is commit over the whole graph rather than only its documents, which is
// what a restore needs: it is the one change that sets the standing label
// instead of clearing it.
//
// Clearing is by construction. The next graph is built with the documents
// copied and nothing else carried, so a change that does not set the label does
// not have one.
func (f *FS) save(ctx context.Context, change func(*graph) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	next := graph{Documents: maps.Clone(f.graph.Documents)}
	if err := change(&next); err != nil {
		return err
	}

	body, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := f.store.Save(ctx, f.name, body); err != nil {
		return err
	}
	f.graph = next
	return nil
}

// held hands one document back, and whether there was one.
func (f *FS) held(key string) (document, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	held, there := f.graph.Documents[key]
	return held, there
}

// read is what a query walks: every document the graph holds, under the read
// lock for as long as walk runs.
func (f *FS) read(walk func(map[string]document)) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	walk(f.graph.Documents)
}

// info is what the graph says about a document.
func (d document) info(name string) vfs.FileInfo {
	return vfs.FileInfo{
		Name:    base(name),
		Size:    int64(len(d.Body)),
		Mode:    d.Mode.Perm(),
		ModTime: d.Written,
	}
}
