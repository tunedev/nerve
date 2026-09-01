package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// The sidecars a document's levels are written under. They are the rungs a
// caller escalates up — the shortest true thing about a document, then a
// summary of it, then a long one — and the words extraction left stand under
// TextSidecar at the top of the same ladder.
//
// Each is its own file rather than one file holding three, because a caller
// that wanted the abstract reads the abstract: a retrieval layer asking for the
// cheapest answer should not pay for the dearest one to get it.
const (
	AbstractSidecar = "abstract"
	OverviewSidecar = "overview"
	DetailSidecar   = "detail"
)

// SummariseStage is the name a run that failed here reports.
const SummariseStage = "summarise"

// Level is one rung: the sidecar a summary is written under and how much of the
// document that rung is allowed to be.
//
// A budget in sentences is what both kinds of summariser take — the one that
// picks sentences out of the document and the one that writes new ones — and it
// is the only thing a level says. What makes an abstract an abstract is its
// length; everything else about it is the summariser's answer.
type Level struct {
	Kind      string
	Sentences int
}

// Levels is the ladder a build has unless composition passes another: L0 the
// abstract, L1 the overview, L2 the detail.
//
// It is a value handed to the stage rather than a list the stage knows, so a
// fourth rung is a line at the composition root and a different ladder is
// configuration. Nothing here counts three.
var Levels = []Level{
	{Kind: AbstractSidecar, Sentences: 1},
	{Kind: OverviewSidecar, Sentences: 5},
	{Kind: DetailSidecar, Sentences: 20},
}

// Summariser says what a document is about in at most the sentences it is
// given.
//
// It is a port because the second implementation is nameable: sentences picked
// out of the document by term weight on the laptop today, a model behind the
// retrieval layer's gateway once there is one. Which one a build has is
// composition, and neither the stage nor the pipeline learns either.
//
// The budget is a ceiling and not a quota. A document with less to say than the
// rung allows says less, which is why a short document's three levels are
// allowed to be the same words.
type Summariser interface {
	Summarise(ctx context.Context, text string, sentences int) (string, error)
}

// Summarisation is the stage that writes a document's levels, summarising the
// words extraction left rather than the bytes they were carried in.
//
// It reads the text sidecar for the reason it writes sidecars: the format is
// over by the time this runs, and a stage that read the content would be a
// second thing in the pipeline that has to know how to parse a PDF.
//
// A document with no text sidecar is one nothing could read, and it is left
// alone rather than failed — the same answer extraction gave it, for the same
// reason: no run of the same job would ever reach a different one. A summariser
// that could not answer is the opposite case and does fail the run, because the
// document was meant to have an abstract and does not.
func Summarisation(resources *resource.Store, summariser Summariser, levels ...Level) Stage {
	return Stage{Name: SummariseStage, Run: func(ctx context.Context, s workspace.Scope, r resource.Resource) error {
		text, err := resources.Derived(ctx, s, r.Name, TextSidecar)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}

		for _, level := range levels {
			summary, err := summariser.Summarise(ctx, string(text), level.Sentences)
			if err != nil {
				return fmt.Errorf("summarising %q to %d sentences: %w", r.Name, level.Sentences, err)
			}
			if err := resources.SetDerived(ctx, s, r.Name, level.Kind, []byte(summary)); err != nil {
				return err
			}
		}
		return nil
	}}
}
