package ingest

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Indexer is one thing that can be made of a finalised resource — a full-text
// index, an embedding, a graph of the entities in it.
//
// A handler says for itself which resources are its work, because only it
// knows. The alternative is a table of content types somewhere else, and the
// first handler that selects on anything but the content type — a size, a
// digest already seen, a workspace setting — turns that table into the switch
// this interface exists to avoid.
type Indexer interface {
	// Name is what an error says failed.
	Name() string
	// Handles reports whether this resource is work for this indexer.
	Handles(r resource.Resource) bool
	// Index does the work. It is given the resource rather than its content,
	// for the reason a Stage is: an indexer that needs the bytes reads them
	// from the store it was built with.
	Index(ctx context.Context, s workspace.Scope, r resource.Resource) error
}

// Dispatch hands one resource to every indexer that claims it.
//
// It is a Stage, and that is the whole of how a handler set reaches ingestion:
// the pipeline runs stages in order and has no idea one of them is a fan-out.
// Adding a kind of indexing is a handler given to NewDispatch at composition,
// never an edit here.
//
// Indexers are independent, which is what makes this different from the stage
// list it sits in. A stage that fails ends the run because what follows it is
// written expecting it to have happened; nothing follows an indexer, so every
// claimant runs and the run reports all of them that failed. One vector store
// being down costs the embedding and not the full-text index.
type Dispatch struct {
	indexers []Indexer
}

// NewDispatch returns a dispatcher over indexers. Order is not meaningful: they
// run together.
func NewDispatch(indexers ...Indexer) *Dispatch {
	return &Dispatch{indexers: indexers}
}

// Stage is this dispatcher as a step of ingestion.
func (d *Dispatch) Stage() Stage {
	return Stage{Name: "index", Run: d.Run}
}

// Run fans the resource out to its claimants and waits for all of them.
//
// The fan-out is the point: an indexer is remote work, and running three of
// them costs one of them rather than three. Every goroutine it starts is
// finished before it returns, so the caller's deadline is the only thing
// bounding them and there is nothing left running when it is over.
//
// A resource nothing claims is a resource with no indexing to do, which is the
// ordinary case for a format no handler knows yet.
func (d *Dispatch) Run(ctx context.Context, s workspace.Scope, r resource.Resource) error {
	failures := make([]error, len(d.indexers))
	var running sync.WaitGroup

	for i, indexer := range d.indexers {
		if !indexer.Handles(r) {
			continue
		}
		running.Go(func() {
			if err := indexer.Index(ctx, s, r); err != nil {
				failures[i] = fmt.Errorf("indexer %q: %w", indexer.Name(), err)
			}
		})
	}
	running.Wait()

	return errors.Join(failures...)
}
