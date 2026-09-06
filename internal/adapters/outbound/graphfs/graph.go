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
}

// load reads the graph kept under name. A name with nothing under it is a graph
// with nothing in it, which is the first boot and not a failure.
func load(ctx context.Context, store Store, name string) (map[string]document, error) {
	body, err := store.Load(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]document{}, nil
	}
	if err != nil {
		return nil, err
	}

	var held graph
	if err := json.Unmarshal(body, &held); err != nil {
		return nil, fmt.Errorf("graph %q is kept as something this build cannot read: %w", name, err)
	}
	if held.Documents == nil {
		held.Documents = map[string]document{}
	}
	return held.Documents, nil
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
	f.mu.Lock()
	defer f.mu.Unlock()

	next := maps.Clone(f.documents)
	if err := change(next); err != nil {
		return err
	}

	body, err := json.Marshal(graph{Documents: next})
	if err != nil {
		return err
	}
	if err := f.store.Save(ctx, f.name, body); err != nil {
		return err
	}
	f.documents = next
	return nil
}

// held hands one document back, and whether there was one.
func (f *FS) held(key string) (document, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	held, there := f.documents[key]
	return held, there
}

// read is what a query walks: every document the graph holds, under the read
// lock for as long as walk runs.
func (f *FS) read(walk func(map[string]document)) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	walk(f.documents)
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
