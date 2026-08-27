package exportfs

import (
	"context"
	"fmt"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Join is the one materialisation a config can ask for by name: the inputs, in
// the order the export named them, one after another.
//
// It is here so that an export mount is a line of config rather than a build.
// A materialisation that does more than gather — a projection sliced with a
// query engine, a document rendered from a template — is a Materialise handed
// to New at composition, where the code that does it already lives.
func Join(ctx context.Context, source vfs.FileSystem, inputs []string) ([]byte, error) {
	var joined []byte

	for _, input := range inputs {
		content, err := source.Read(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("materialise an export from %q: %w", input, err)
		}
		joined = append(joined, content...)
	}
	return joined, nil
}
