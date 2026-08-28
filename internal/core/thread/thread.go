// Package thread is a workspace's record of what its agent runs produced: the
// output of a run, written down as the run makes it, and readable long after
// the connection that asked for the run has gone.
//
// That is the whole reason the package exists. An agent run is slow and its
// output arrives in pieces, so a caller that took the pieces off a connection
// and held them would lose the run when the connection dropped. Here every
// piece lands in the store as it is produced, and the caller that reads it back
// is under no obligation to be the caller that asked.
//
// A thread is one run. It is started once, appended to while it works, and
// ended by saying either that it finished or why it did not. Those two endings
// are separate verbs rather than a status a caller passes, because a run that
// stopped and a run that stopped working are different things to have found.
//
// Reading a thread answers with the run and its output in one value, and never
// with the output alone. A reader that reconnected has no other way to tell
// output that stopped from output that is still coming, and would take the
// first for the second.
//
// The log stores nothing itself. It reads and writes the one FileSystem the
// mount table presents, at paths derived from the scope that owns them, so
// whether a run's output actually sits on a disk, in a keyspace or in an object
// store is a mount rather than a decision made here. Both shapes work
// unchanged: a thread is a directory on a tree and a prefix in a keyspace, and
// nothing below depends on which.
package thread

import (
	"fmt"
	"io/fs"
	"strings"
)

// Entry is one piece of output a run appended, with its position in the run.
// Sequence numbers start at one and have no gaps: the log assigns them, and a
// caller uses them to say how much of a thread it has already seen.
type Entry struct {
	Seq  int
	Data []byte
}

// Result is a run and everything it has produced so far. It is what Read
// answers with, and the two fields arrive together on purpose — see the package
// comment.
type Result struct {
	Run     Run
	Entries []Entry
}

// validName returns fs.ErrInvalid for a name no thread can have. The rule is
// the scope's flat-segment rule for the same reason: a name carrying a
// separator names a run inside another one, and "." or ".." is a spelling that
// resolves away and lands on a thread nobody started.
func validName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("thread name is empty: %w", fs.ErrInvalid)
	case strings.Contains(name, "/"):
		return fmt.Errorf("thread name %q contains a separator: %w", name, fs.ErrInvalid)
	case name == "." || name == "..":
		return fmt.Errorf("thread name %q names a directory, not a thread: %w", name, fs.ErrInvalid)
	}
	return nil
}
