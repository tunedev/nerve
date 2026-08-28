package library

import (
	"fmt"
	"io/fs"
	"strings"
)

// The file a document is written in is a fenced frontmatter block holding
// exactly one line, then what the document says. One line because there is one
// thing every kind has to declare that is not in its body, and a format with one
// key needs no parser: a second key arrives with the ticket that needs it.
//
// The format is the same for every kind. What a skill and a blueprint declare
// about themselves is the same declaration, which is why one decoder reads both
// and the kind is only the name of the file it reads.
const (
	fence          = "---"
	descriptionKey = "description"
)

// encode renders doc as the bytes of its file.
func encode(doc Document) []byte {
	return []byte(fence + "\n" + descriptionKey + ": " + doc.Description + "\n" + fence + "\n" + doc.Body)
}

// decode reads a file back into the card it declares and the body beneath it.
// The name is the directory the file was found in, so what comes back carries
// only the description.
//
// Everything it refuses, it refuses with fs.ErrInvalid, because a file standing
// where a document belongs and not being one is a file someone has to fix
// rather than a document with an empty description. A missing description that
// read back as "" would be a document that exists and can never be found.
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

// validDescription returns fs.ErrInvalid for a description the frontmatter
// cannot hold. It is one line: a description carrying a newline would close its
// own block and read back as a different document.
func validDescription(description string) error {
	switch {
	case description == "":
		return fmt.Errorf("document has no description: %w", fs.ErrInvalid)
	case strings.Contains(description, "\n"):
		return fmt.Errorf("document description is more than one line: %w", fs.ErrInvalid)
	}
	return nil
}
