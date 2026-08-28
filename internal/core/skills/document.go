package skills

import (
	"fmt"
	"io/fs"
	"strings"
)

// Document is the one file every skill has. Whatever else sits in a skill's
// directory, this is the file that makes the directory a skill.
const Document = "SKILL.md"

// The document is a fenced frontmatter block holding exactly one line, then the
// instructions. One line because there is one thing a skill has to declare that
// is not in its body, and a format with one key needs no parser: a second key
// arrives with the ticket that needs it.
const (
	fence          = "---"
	descriptionKey = "description"
)

// encode renders sk as the bytes of its document.
func encode(sk Skill) []byte {
	return []byte(fence + "\n" + descriptionKey + ": " + sk.Description + "\n" + fence + "\n" + sk.Body)
}

// decode reads a document back into the card it declares and the body beneath
// it. The name is the directory the document was found in, so what comes back
// carries only the description.
//
// Everything it refuses, it refuses with fs.ErrInvalid, because a file in the
// skills tree that is not a document is a file someone has to fix rather than a
// skill with an empty description. A missing description that read back as ""
// would be a skill that exists and can never be found.
func decode(data []byte) (Card, string, error) {
	rest, ok := strings.CutPrefix(string(data), fence+"\n")
	if !ok {
		return Card{}, "", fmt.Errorf("document does not open with a frontmatter fence: %w", fs.ErrInvalid)
	}

	head, body, ok := strings.Cut(rest, "\n"+fence+"\n")
	if !ok {
		return Card{}, "", fmt.Errorf("document's frontmatter block is not closed: %w", fs.ErrInvalid)
	}

	description, ok := strings.CutPrefix(head, descriptionKey+": ")
	if !ok || description == "" {
		return Card{}, "", fmt.Errorf("frontmatter is not a single %q line: %w", descriptionKey, fs.ErrInvalid)
	}
	return Card{Description: description}, body, nil
}

// validDescription returns fs.ErrInvalid for a description the document cannot
// hold. It is one line: a description carrying a newline would close its own
// frontmatter block and read back as a different skill.
func validDescription(description string) error {
	switch {
	case description == "":
		return fmt.Errorf("skill has no description: %w", fs.ErrInvalid)
	case strings.Contains(description, "\n"):
		return fmt.Errorf("skill description is more than one line: %w", fs.ErrInvalid)
	}
	return nil
}
