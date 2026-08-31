// Package ingest is what happens to a resource after the upload that carried it
// is over.
//
// Ingestion is queued rather than performed. A resource is finalised by a call
// that writes one small file, and the tree that write goes through publishes
// the path that changed; the pipeline is on the other end of that queue, run by
// a worker that claims the job later. The call that finalised the resource is
// long gone by then, which is the whole point: whatever ingestion grows into,
// none of it happens while a caller is waiting.
//
// Nothing here asks to be told. A producer that had to remember to queue the
// work is a producer that will one day forget, so the trigger is the write
// itself — the same bus an indexer or a projection would be on, seen by the one
// handler that knows what a resource is.
//
// The stages are what ingestion does, in order, and they are given to the
// pipeline rather than known by it. A format that needs its own handling is a
// stage the composition root adds, not a branch here.
//
// Where a run got to is written down as it goes. The caller that uploaded the
// document is long gone and the error a failed stage returns dies with the
// worker that claimed the job, so the ledger is the only thing a caller asking
// whether its document is searchable yet has to read.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Stage is one step of ingestion. It is named so that a run which failed says
// where, and it is given the resource rather than its content: a stage that
// needs the bytes reads them from the store it was built with, which is also
// what lets a stage that does not need them cost nothing.
type Stage struct {
	Name string
	Run  func(ctx context.Context, s workspace.Scope, r resource.Resource) error
}

// Pipeline is the consuming end of ingestion: it is told a path changed and
// runs the stages over the resource that path declares.
//
// It holds no goroutine and no ticker. Whoever claims jobs drives it — a queue
// worker in the shipped stack — so a pipeline with nothing arriving is a
// pipeline doing nothing rather than one polling.
type Pipeline struct {
	resources *resource.Store
	ledger    *Ledger
	stages    []Stage
}

// New returns a pipeline that runs stages, in the order given, over resources
// in store, recording where each run got to in ledger.
func New(resources *resource.Store, ledger *Ledger, stages ...Stage) *Pipeline {
	return &Pipeline{resources: resources, ledger: ledger, stages: stages}
}

// Ingest is the work one changed path is.
//
// Three of the four things a notification can be about are nothing to do, and
// none of them is an error. A path that declares no resource belongs to
// somebody else on the same bus; a resource that is no longer there was deleted
// between the write and the claim; a resource that has not been finalised is
// one whose content is still arriving, and its own completion will queue this
// job again. Only the fourth runs the stages.
//
// A stage that fails ends the run and is named in the error, and is named in
// the ledger too. The error goes to whoever claimed the job and dies with it;
// the record is what a caller asking about its document reads afterwards.
//
// Nothing is recorded for the three notifications that are nothing to do. A
// document said to be working that nothing is working on is worse than no
// record at all, because the caller would wait for it.
func (p *Pipeline) Ingest(ctx context.Context, changed string) error {
	s, name, err := p.resources.Resolve(changed)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	arrived, err := p.resources.Stat(ctx, s, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ingest %q: %w", name, err)
	}
	if !arrived.Complete {
		return nil
	}

	if err := p.ledger.Working(ctx, s, name); err != nil {
		return fmt.Errorf("ingest %q: %w", name, err)
	}
	for _, stage := range p.stages {
		if err := stage.Run(ctx, s, arrived); err != nil {
			failed := fmt.Errorf("ingest %q: stage %q: %w", name, stage.Name, err)
			// The stage is what went wrong and is what the caller is told. A
			// ledger that could not be written is joined to it rather than
			// dropped: it leaves the document saying it is working and nothing
			// else would ever say otherwise.
			return errors.Join(failed, p.ledger.Failed(ctx, s, name, stage.Name, err.Error()))
		}
	}
	if err := p.ledger.Indexed(ctx, s, name); err != nil {
		return fmt.Errorf("ingest %q: %w", name, err)
	}
	return nil
}
