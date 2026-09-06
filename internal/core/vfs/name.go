package vfs

import (
	"fmt"
	"io/fs"
	"strings"
)

// ValidSegment returns fs.ErrInvalid for a name that cannot stand as one path
// segment. A name carrying a separator names a place inside another one, and
// "." or ".." is a spelling that resolves away and lands somewhere nobody
// asked for; an empty name lands on the directory holding it.
//
// kind is what the caller calls the thing, so the error reads in the caller's
// vocabulary rather than the rule's.
func ValidSegment(kind, name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%s is empty: %w", kind, fs.ErrInvalid)
	case strings.Contains(name, "/"):
		return fmt.Errorf("%s %q contains a separator: %w", kind, name, fs.ErrInvalid)
	case name == "." || name == "..":
		return fmt.Errorf("%s %q names a directory: %w", kind, name, fs.ErrInvalid)
	}
	return nil
}
