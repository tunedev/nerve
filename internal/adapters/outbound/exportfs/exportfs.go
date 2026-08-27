// Package exportfs serves materialised exports: names whose content is derived
// from other paths rather than stored under them. A projection sliced out of a
// workspace, a manifest gathered from the documents it lists, a report rolled
// up from a day of logs — each is expensive to make and cheap to read, so it is
// made once and held.
//
// Held content goes stale. The whole of this plugin is the part that notices:
// every read stamps the export's inputs, compares that stamp with the one the
// held content was made from, and materialises again before answering when the
// two differ. A caller never has to ask whether what it is reading is current,
// and no writer has to remember to say that it is not.
//
// The mount is read-only and flat. An export's name is the plugin's, because
// its content is the plugin's: there is nothing for a caller to write, and
// writing an input is how an export is changed.
package exportfs

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/tunedev/nerve/internal/adapters/outbound/synth"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Materialise makes the content of one export out of the inputs it was named
// with, in the order it named them.
//
// It is handed the source rather than the inputs' bytes, because an export
// exists where the inputs are larger than what it produces: a materialisation
// that reads a day of logs to answer with a line should not have the day of
// logs assembled for it first.
type Materialise func(ctx context.Context, source vfs.FileSystem, inputs []string) ([]byte, error)

// Export is one derived name: what it is computed from, and what computes it.
type Export struct {
	// Name is the entry the mount answers for, a single element under the root.
	Name string

	// Inputs are the paths in the source whose content the export is derived
	// from, in the order the materialisation wants them. They are also what is
	// stamped: a change to any of them makes the held content stale.
	Inputs []string

	// From makes the content. It is called at the first read and again after
	// any input changes, never otherwise.
	From Materialise
}

// New returns a mount over exports, materialised out of source. Every export is
// checked here rather than at the first read, because an export naming nothing,
// or naming the same entry twice, is a config a process should not boot on.
func New(source vfs.FileSystem, exports []Export) (*synth.Tree, error) {
	if len(exports) == 0 {
		return nil, fmt.Errorf("an export mount with no exports answers for nothing: %w", fs.ErrInvalid)
	}

	entries := make(map[string]synth.Entry, len(exports))
	for _, export := range exports {
		if err := check(export, entries); err != nil {
			return nil, err
		}

		held := &cache{source: source, inputs: export.Inputs, from: export.From}
		entries[export.Name] = synth.Entry{Content: held.content, Changed: held.changed}
	}
	return synth.NewTree(entries), nil
}

// check holds one export to what a name and a derivation have to be, against
// the exports already taken.
func check(export Export, taken map[string]synth.Entry) error {
	switch {
	case export.Name == "":
		return fmt.Errorf("an export with no name: %w", fs.ErrInvalid)
	case strings.Contains(export.Name, "/"):
		return fmt.Errorf("export %q names a path, and an export mount is flat: %w", export.Name, fs.ErrInvalid)
	case len(export.Inputs) == 0:
		return fmt.Errorf("export %q is derived from nothing, so nothing would ever make it stale: %w", export.Name, fs.ErrInvalid)
	case export.From == nil:
		return fmt.Errorf("export %q names inputs and nothing that makes it: %w", export.Name, fs.ErrInvalid)
	}

	if _, already := taken[export.Name]; already {
		return fmt.Errorf("export %q is declared twice: %w", export.Name, fs.ErrExist)
	}
	return nil
}
