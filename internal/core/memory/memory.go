// Package memory is what a workspace keeps from its conversations: typed facts,
// each ranked by how much it is worth keeping, each naming the session it came
// out of.
//
// A memory is not a message. A transcript is what was said, in the order it was
// said, and it is the record; a memory is a claim standing on its own, in a kind
// a caller can ask for and at a priority a caller with limited room can sort by.
// The two are kept apart because everything that reads a memory — the context a
// turn is built from, the dedup that stops a restated fact becoming a second
// one — asks about the claim and never about the turn it was said in.
//
// The log is append-only for the reason a transcript is. What is derived from a
// memory is derived from the memory that was there, and a memory that can be
// rewritten underneath a derivation makes the derivation a claim about nothing.
// A memory that stops being true is superseded by a later one rather than
// edited, which is also what makes an as-of question answerable.
//
// Every memory carries the session it was extracted from. A fact about a user
// with no record of where it came from cannot be checked, cannot be reconciled
// against the turn that contradicts it, and cannot be removed when the
// conversation that produced it is deleted.
package memory

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"
)

// Priority is how much a fact is worth keeping, on a three-step scale. It is
// ordered, so a caller with room for some of what it holds keeps the larger
// values.
type Priority int

// The scale. Zero is not on it: a memory that named no priority is refused
// rather than silently ranked lowest and dropped first.
const (
	// Low is worth keeping and worth dropping first.
	Low Priority = iota + 1
	// Normal is the ordinary fact: stated plainly, expected to hold.
	Normal
	// High is what a caller keeps when it keeps one thing.
	High
)

// priorityNames is the scale as a stored memory spells it. Priorities are
// written by name rather than by number because the number is this package's
// ordering and the file outlives it: inserting a step between two of them would
// silently re-rank every memory already written.
var priorityNames = map[Priority]string{Low: "low", Normal: "normal", High: "high"}

// String is the name a stored memory carries, and "unknown" for a value off the
// scale, so a priority that should not exist reads as one rather than as a
// number in a message about something else.
func (p Priority) String() string {
	if name, known := priorityNames[p]; known {
		return name
	}
	return "unknown"
}

// MarshalJSON writes the priority by name.
func (p Priority) MarshalJSON() ([]byte, error) {
	name, known := priorityNames[p]
	if !known {
		return nil, fmt.Errorf("priority %d is not on the scale: %w", int(p), fs.ErrInvalid)
	}
	return json.Marshal(name)
}

// UnmarshalJSON reads a priority back by name, refusing any name not on the
// scale with fs.ErrInvalid. A memory ranked by something this build cannot
// order is one it would sort arbitrarily.
func (p *Priority) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("priority does not decode: %w: %w", err, fs.ErrInvalid)
	}
	for value, known := range priorityNames {
		if known == name {
			*p = value
			return nil
		}
	}
	return fmt.Errorf("priority %q is not on the scale: %w", name, fs.ErrInvalid)
}

// Fact is one thing worth remembering: what sort of thing it is, what it says,
// and how much it is worth keeping.
//
// Kind is the caller's vocabulary and this package reads none of it, for the
// reason a session's metadata keys are the caller's: what sorts of fact there
// are is decided where facts are made, and a store that understood one kind
// would have to be changed to understand a second.
type Fact struct {
	Kind     string   `json:"kind"`
	Body     string   `json:"body"`
	Priority Priority `json:"priority"`
}

// Memory is a fact as the store holds it: the claim, where it came from, and
// when it was written.
type Memory struct {
	// Seq is the position in the workspace's log, counting from one.
	Seq int
	// Fact is the claim itself.
	Fact Fact
	// Source is the session the fact was extracted from.
	Source string
	// At is when it was written.
	At time.Time
}

// held is a Memory as a file holds it. The sequence is not in it, because the
// sequence is the file's own name.
type held struct {
	Fact   Fact      `json:"fact"`
	Source string    `json:"source"`
	At     time.Time `json:"at"`
}

// encode renders a memory as the bytes of its file.
func encode(m held) ([]byte, error) {
	return json.Marshal(m)
}

// decode reads a file back into the memory it holds, refusing anything that is
// not one with fs.ErrInvalid. A memory that would not decode fails the read of
// the whole log rather than being passed over: a caller building a turn's
// context from a log missing a fact is answering with what the user was not
// told about.
func decode(data []byte) (held, error) {
	var m held
	if err := json.Unmarshal(data, &m); err != nil {
		return held{}, fmt.Errorf("memory does not decode: %w: %w", err, fs.ErrInvalid)
	}
	if err := validFact(m.Fact); err != nil {
		return held{}, err
	}
	return m, nil
}

// validFact returns fs.ErrInvalid for a fact no memory can carry. A fact with
// no kind cannot be asked for, one with no body says nothing, and one off the
// priority scale cannot be ranked against the rest.
func validFact(f Fact) error {
	switch {
	case f.Kind == "":
		return fmt.Errorf("fact names no kind: %w", fs.ErrInvalid)
	case f.Body == "":
		return fmt.Errorf("fact %q has no body: %w", f.Kind, fs.ErrInvalid)
	}
	if _, known := priorityNames[f.Priority]; !known {
		return fmt.Errorf("fact %q has priority %d, which is not on the scale: %w", f.Kind, int(f.Priority), fs.ErrInvalid)
	}
	return nil
}

// validSource returns fs.ErrInvalid for a memory that does not say where it came
// from. The rule is the session's own name rule, because that is what a source
// is: a fact whose source resolves away names a conversation nobody had.
func validSource(source string) error {
	switch {
	case source == "":
		return fmt.Errorf("memory names no source session: %w", fs.ErrInvalid)
	case strings.Contains(source, "/"):
		return fmt.Errorf("source session %q contains a separator: %w", source, fs.ErrInvalid)
	case source == "." || source == "..":
		return fmt.Errorf("source session %q names a directory, not a session: %w", source, fs.ErrInvalid)
	}
	return nil
}

// name is what the memory at seq is called. It is the sequence written plainly:
// memories are ordered by the number a name decodes to and never by the order
// the names sort in, so there is nothing for a padded name to buy.
func name(seq int) string {
	return strconv.Itoa(seq)
}

// seqOf reads a name back into the sequence it stands for, and reports whether
// the name is a memory at all — whatever else a mount put in the directory is
// not one.
//
// A name that does not round-trip is not a memory, which is how a spelling like
// "01" that decodes to a sequence already taken is refused rather than counted
// twice.
func seqOf(n string) (int, bool) {
	seq, err := strconv.Atoi(n)
	if err != nil || seq < 1 || name(seq) != n {
		return 0, false
	}
	return seq, true
}
