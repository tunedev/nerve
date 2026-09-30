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
//
// A memory a workspace has stopped keeping is archived rather than removed: it
// moves to a directory beside the log, keeping the sequence it was written
// under, and the verb that names the archive reads it back. Moved aside is not
// deleted, and a sequence, once handed out, is never handed out again, so the
// log and the archive can never disagree about which memory is which.
//
// Because nothing is edited, every memory has a validity interval: it was
// believed from the moment it was written until the moment something replaced
// it, and a memory nothing has replaced is believed still. The interval is
// derived rather than stored — a memory that supersedes another names it, and
// the named memory stops being believed when the naming one starts. Storing an
// end on the memory itself would mean writing to it later, which is the one
// thing the log does not do.
package memory

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strconv"
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
//
// Subject names the one question a fact answers — where the user lives, what
// they are called — and is empty for a fact that answers none. It is the
// caller's vocabulary too. Within a kind a question has one answer at a time,
// so a fact naming a subject replaces the memory that answered it before.
type Fact struct {
	Kind     string   `json:"kind"`
	Subject  string   `json:"subject,omitempty"`
	Body     string   `json:"body"`
	Priority Priority `json:"priority"`
}

// Memory is a fact as the store holds it: the claim, where it came from, when
// it was written, and how long it was believed.
type Memory struct {
	// Seq is the position in the workspace's log, counting from one.
	Seq int
	// Fact is the claim itself.
	Fact Fact
	// Source is the session the fact was extracted from.
	Source string
	// At is when it was written, and the moment it started being believed.
	At time.Time
	// Until is when the first memory replacing this one was written, and the
	// moment it stopped being believed. Zero while nothing has replaced it.
	Until time.Time
	// Supersedes is the sequence of every memory this one replaced, ascending.
	Supersedes []int
}

// Believed reports whether the memory was the workspace's belief at t. The
// interval is half-open: the memory that replaced this one is believed at the
// instant it was written, and this one is not.
func (m Memory) Believed(t time.Time) bool {
	if t.Before(m.At) {
		return false
	}
	return m.Until.IsZero() || t.Before(m.Until)
}

// held is a Memory as a file holds it. The sequence is not in it, because the
// sequence is the file's own name, and neither is the end of the interval,
// because the memory that ends it is the one that records it.
type held struct {
	Fact       Fact      `json:"fact"`
	Source     string    `json:"source"`
	At         time.Time `json:"at"`
	Supersedes []int     `json:"supersedes,omitempty"`
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
	if err := validSupersedes(m.Supersedes); err != nil {
		return held{}, err
	}
	return m, nil
}

// validSupersedes returns fs.ErrInvalid for a supersession no memory can carry.
// A memory replacing nothing says so by naming nothing, a sequence off the log
// names no memory, and naming one twice would close its interval against
// itself.
func validSupersedes(replaced []int) error {
	seen := make(map[int]bool, len(replaced))
	for _, seq := range replaced {
		switch {
		case seq < 1:
			return fmt.Errorf("memory supersedes %d, which is not a sequence: %w", seq, fs.ErrInvalid)
		case seen[seq]:
			return fmt.Errorf("memory supersedes %d twice: %w", seq, fs.ErrInvalid)
		}
		seen[seq] = true
	}
	return nil
}

// validFacts returns the first refusal validFact makes of the batch.
func validFacts(facts []Fact) error {
	for _, f := range facts {
		if err := validFact(f); err != nil {
			return err
		}
	}
	return nil
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
