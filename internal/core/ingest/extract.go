package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// TextSidecar is what the extraction stage writes beside a resource: its
// content as words, whatever the format the words arrived in.
//
// It is one name rather than one per format, because what comes after
// extraction — summarising, embedding, indexing — is written against text and
// not against the format it was carried in. Nothing downstream asks what a
// document was.
const TextSidecar = "text"

// ExtractStage is the name a run that failed here reports.
const ExtractStage = "extract"

// Extraction is the stage that turns a resource's content into its text, using
// the processor registered for the type the resource declared.
//
// This is the seam a format arrives through. Adding one is a package that
// implements Processor and a line at the composition root registering it under
// its media type; nothing here, and nothing in the pipeline, names a format or
// grows an arm when a second one appears.
//
// A format this build has no processor for is left alone rather than failed. It
// is not a transient failure and the same job claimed again would reach the same
// answer, so failing it would put a job that can never succeed in front of every
// job behind it. What it leaves is no sidecar, which is what "nothing read this"
// looks like from outside — E6.6 is where a caller is told so.
//
// A processor that could not read a file of its own format is a different
// answer and does fail the run: a corrupt PDF is a document that was meant to
// become searchable and did not.
func Extraction(resources *resource.Store, registry *Registry) Stage {
	return Stage{Name: ExtractStage, Run: func(ctx context.Context, s workspace.Scope, r resource.Resource) error {
		processor, err := registry.For(r.ContentType)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}

		content, err := resources.Content(ctx, s, r.Name)
		if err != nil {
			return err
		}
		defer func() { _ = content.Close() }()

		text, err := processor.Extract(ctx, content)
		if err != nil {
			return fmt.Errorf("reading %q as %s: %w", r.Name, r.ContentType, err)
		}
		return resources.SetDerived(ctx, s, r.Name, TextSidecar, []byte(text))
	}}
}
