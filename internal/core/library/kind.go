package library

import (
	"fmt"
	"io/fs"
)

// Kind is what makes a directory a document of one sort rather than another:
// the file it must hold, and what one of them is called when something has to
// be said about it.
//
// It is a value a caller hands the registry, so the kinds below are the two
// there are today rather than the two there can be. A caller with a third sort
// of document constructs a Kind for it and gets the same eight verbs.
type Kind struct {
	// Name is what one document of this kind is called, in errors and in
	// anything above that has to name what it is holding.
	Name string
	// Filename is the file every document of this kind has. Whatever else sits
	// in the directory, this is the file that makes the directory one of these.
	Filename string
}

// Skills is the kind holding what an agent knows how to do.
func Skills() Kind { return Kind{Name: "skill", Filename: "SKILL.md"} }

// Blueprints is the kind holding the shape of what an agent produces: what the
// thing is made of and how it is put together, with its examples beside it.
func Blueprints() Kind { return Kind{Name: "blueprint", Filename: "BLUEPRINT.md"} }

// valid returns fs.ErrInvalid for a kind no registry can serve. The filename is
// one flat segment for the reason a document's name is: a kind whose file is
// "docs/SKILL.md" describes a document that is two directories deep, and List
// would walk past every one of them.
func (k Kind) valid() error {
	if k.Name == "" {
		return fmt.Errorf("kind has no name: %w", fs.ErrInvalid)
	}
	if err := validName(k.Filename); err != nil {
		return fmt.Errorf("kind %q: %w", k.Name, err)
	}
	return nil
}
