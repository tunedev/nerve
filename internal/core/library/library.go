// Package library is a workspace's collection of documents of one kind: the
// skills it knows how to do, the blueprints of what it produces.
//
// A document is a directory: one file saying what the document is for and what
// it says, and whatever files it needs beside it. The directory's name is the
// document's name, so a document is addressed the way anything else in the tree
// is and nothing has to keep a name and a location agreeing with each other.
//
// The kind is data, not a type. Skills and blueprints differ in what their file
// is called and in nothing else, so one Registry serves both and a third kind
// is a Kind value rather than a second registry.
//
// The registry stores nothing itself. It reads and writes the one FileSystem
// the mount table presents, at paths derived from the scope that owns them, so
// which storage kind a library actually sits on is a mount rather than a
// decision made here.
//
// What a document says about itself and what it says are two different reads.
// Choosing between documents needs the first; using one needs the second. List
// and Search answer with cards, Get and Bundle with the whole thing, and a
// caller escalates only where it has to.
package library

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
)

// Card is what a document says about itself before anyone reads it: enough to
// choose between documents, and not the instructions themselves.
type Card struct {
	// Name is the document's directory, one flat segment inside the scope.
	Name string
	// Description is the single line that says what the document is for. It is
	// what Search matches against, so a document without one cannot be found.
	Description string
}

// Document is a card and what stands behind it.
type Document struct {
	Card
	// Body is what the document says beneath its description.
	Body string
}

// Match is one document a matcher considers relevant, with how relevant it
// thinks it is. Scores are comparable within one Rank and mean nothing between
// two: a matcher is free to score however it likes as long as the order is
// right.
type Match struct {
	Card  Card
	Score float64
}

// File is one file beside a document, with its path relative to the document's
// directory.
type File struct {
	Path string
	Data []byte
}

// Bundle is everything a document is, in one read: the document decoded, and
// the files beside it. The file the document was decoded from is not repeated
// among them, because it is already here as the document.
type Bundle struct {
	Document Document
	Files    []File
}

// Matcher ranks documents against the task a caller is trying to do.
//
// It is a port because the second implementation is nameable: term overlap over
// the cards on the laptop today, an embedding matcher over the retrieval
// layer's provider once there is one. Which one a build uses is composition,
// and nothing in this package learns either.
//
// Rank returns only the cards it finds relevant, best first. A matcher that
// finds nothing relevant returns no matches rather than every card in score
// order: "these are the documents for this task" and "here is everything,
// ranked" are different answers, and only the first is worth putting in front
// of an agent.
type Matcher interface {
	Rank(ctx context.Context, task string, cards []Card) ([]Match, error)
}

// validName returns fs.ErrInvalid for a name no document can have. The rule is
// the scope's flat-segment rule for the same reason: a name carrying a separator
// is a document that can name a place inside another one, and a name that is "."
// or ".." is a directory rather than a document.
func validName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("name is empty: %w", fs.ErrInvalid)
	case strings.Contains(name, "/"):
		return fmt.Errorf("name %q contains a separator: %w", name, fs.ErrInvalid)
	case name == "." || name == "..":
		return fmt.Errorf("name %q names a directory, not a document: %w", name, fs.ErrInvalid)
	}
	return nil
}
