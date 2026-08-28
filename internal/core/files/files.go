// Package files is a workspace's own directory tree, as the agent working
// inside it walks one: list a directory, stat an entry, make a directory, copy,
// move, delete, link, and find things by name, by path or by what they contain.
//
// It is passthrough. Nothing here stores anything or decides what a file means
// — every verb is the one the FileSystem port already has, or a walk over the
// ones it has. What the package adds is the scope: a caller reaches its own
// workspace and has no spelling for anything else.
//
// The scope is a workspace.View rather than a derived path, which is what
// separates this from the other core packages over the same port. Three of
// these verbs take two paths from the caller — copy, rename, symlink — and a
// symlink target is the one path nothing beneath the core dispatches. A view
// derives every path it is handed, both ends included, so containment here is
// the shape of the package rather than a rule each verb has to remember.
//
// The shell names in the ticket map onto the port's vocabulary: ls is List,
// tree is Walk, mv is Rename, rm is Delete and rm -r is DeleteAll.
package files

import (
	"fmt"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Entry is one thing standing in the tree, named by where it stands relative to
// the directory that was walked rather than by its own final element. A listing
// of one directory answers with vfs.FileInfo, which already names the entry;
// a walk crosses directories, so the path is what the caller needs back.
type Entry struct {
	// Path is where the entry stands, relative to the directory walked.
	Path string
	// Info is what the mount says about it.
	Info vfs.FileInfo
}

// Line is one matching line of one file: where the file stands relative to the
// directory searched, which line it was, and what the line says.
type Line struct {
	// Path is where the file stands, relative to the directory searched.
	Path string
	// Number is the 1-based position of the line in the file.
	Number int
	// Text is the line, without its terminator.
	Text string
}

// pattern is a shell pattern that has been checked. Checking once before the
// walk is what makes a syntax error fail the call: matched lazily, a pattern
// nobody can parse answers "nothing found" over an empty tree and "syntax
// error" over a full one.
type pattern string

// compile returns p checked, or path.ErrBadPattern wrapped with the pattern
// that could not be parsed.
func compile(p string) (pattern, error) {
	if _, err := path.Match(p, ""); err != nil {
		return "", fmt.Errorf("pattern %q: %w", p, err)
	}
	return pattern(p), nil
}

// matches reports whether name is one of the names p stands for. The error
// path.Match returns is the syntax error compile already refused.
func (p pattern) matches(name string) bool {
	ok, _ := path.Match(string(p), name)
	return ok
}
