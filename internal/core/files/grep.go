package files

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Grep returns the lines of the files beneath name in s that expr matches, at
// most limit of them. A limit of zero or less asks for all of them.
//
// expr is an RE2 expression, which is what makes a pattern safe to take from
// whoever is asking: RE2 has no backtracking, so no expression makes the search
// run away over a workspace it is allowed to read.
//
// The limit is here and on none of the other walks because this is the one
// whose answer grows with the bytes in the workspace rather than with the
// number of entries: one common word matches every line of every file.
//
// Files holding a NUL byte are passed over. A line of a binary is not a line,
// and the caller asked for text.
func (t *Tree) Grep(ctx context.Context, s workspace.Scope, name, expr string, limit int) ([]Line, error) {
	want, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("grep under %q: %w", name, err)
	}
	inside, err := t.inside(s)
	if err != nil {
		return nil, fmt.Errorf("grep under %q: %w", name, err)
	}
	files, err := collect(ctx, inside, name, func(_ string, info vfs.FileInfo) bool { return !info.IsDir() })
	if err != nil {
		return nil, fmt.Errorf("grep under %q: %w", name, err)
	}

	var found []Line
	for _, file := range files {
		data, err := inside.Read(ctx, path.Join(name, file.Path))
		if err != nil {
			return nil, fmt.Errorf("grep %q: %w", file.Path, err)
		}
		if bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		for i, text := range lines(string(data)) {
			if !want.MatchString(text) {
				continue
			}
			found = append(found, Line{Path: file.Path, Number: i + 1, Text: text})
			if limit > 0 && len(found) == limit {
				return found, nil
			}
		}
	}
	return found, nil
}

// lines splits text into its lines. A trailing newline ends the last line
// rather than starting an empty one, so the count is what an editor shows.
func lines(text string) []string {
	split := strings.Split(text, "\n")
	if len(split) > 0 && split[len(split)-1] == "" {
		return split[:len(split)-1]
	}
	return split
}
