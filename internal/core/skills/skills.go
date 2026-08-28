// Package skills is the registry of what an agent knows how to do.
//
// A skill is a directory: one document saying what the skill is for and how to
// do it, and whatever files it needs beside it. The directory's name is the
// skill's name, so a skill is addressed the way anything else in the tree is
// and nothing has to keep a name and a location agreeing with each other.
//
// The registry stores nothing itself. It reads and writes the one FileSystem
// the mount table presents, at paths derived from the scope that owns them, so
// which storage kind a skill library actually sits on is a mount rather than a
// decision made here.
//
// What a skill says about itself and what it says are two different reads.
// Choosing between skills needs the first; running one needs the second. List
// and Search answer with cards, Get and Bundle with the whole thing, and a
// caller escalates only where it has to.
package skills

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
)

// Card is what a skill says about itself before anyone reads it: enough to
// choose between skills, and not the instructions themselves.
type Card struct {
	// Name is the skill's directory, one flat segment inside the scope.
	Name string
	// Description is the single line that says what the skill is for. It is
	// what Search matches against, so a skill without one cannot be found.
	Description string
}

// Skill is a card and the instructions behind it.
type Skill struct {
	Card
	// Body is the document beneath the description: how to do the thing.
	Body string
}

// Match is one skill a matcher considers relevant, with how relevant it thinks
// it is. Scores are comparable within one Rank and mean nothing between two:
// a matcher is free to score however it likes as long as the order is right.
type Match struct {
	Card  Card
	Score float64
}

// File is one file beside a skill's document, with its path relative to the
// skill's directory.
type File struct {
	Path string
	Data []byte
}

// Bundle is everything a skill is, in one read: the document decoded, and the
// files beside it. The document is not repeated among the files, because it is
// already here as the skill.
type Bundle struct {
	Skill Skill
	Files []File
}

// Matcher ranks skills against the task a caller is trying to do.
//
// It is a port because the second implementation is nameable: term overlap over
// the cards on the laptop today, an embedding matcher over the retrieval
// layer's provider once there is one. Which one a build uses is composition,
// and nothing in this package learns either.
//
// Rank returns only the cards it finds relevant, best first. A matcher that
// finds nothing relevant returns no matches rather than every card in score
// order: "these are the skills for this task" and "here is everything, ranked"
// are different answers, and only the first is worth putting in front of an
// agent.
type Matcher interface {
	Rank(ctx context.Context, task string, cards []Card) ([]Match, error)
}

// validName returns fs.ErrInvalid for a name no skill can have. The rule is the
// scope's flat-segment rule for the same reason: a name carrying a separator is
// a skill that can name a place inside another one, and a name that is "." or
// ".." is a directory rather than a skill.
func validName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("skill name is empty: %w", fs.ErrInvalid)
	case strings.Contains(name, "/"):
		return fmt.Errorf("skill name %q contains a separator: %w", name, fs.ErrInvalid)
	case name == "." || name == "..":
		return fmt.Errorf("skill name %q names a directory, not a skill: %w", name, fs.ErrInvalid)
	}
	return nil
}
