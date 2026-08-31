// Package textproc reads the formats that are already text. There is nothing to
// extract: the bytes are the words, and what this package does is say so under
// the media types that mean it.
//
// It is a package rather than a case in the extraction stage for the reason PDF
// is one. A format nothing has to parse is the cheapest possible test of the
// claim E6.3 makes — if the plain-text reader had to be a branch somewhere, the
// seam would not be a seam.
package textproc

import (
	"context"
	"io"
)

// MediaTypes are the types this package reads: the ones whose bytes are already
// the document's words.
var MediaTypes = []string{"text/markdown", "text/plain"}

// Processor is the reader this package registers. It holds nothing, so one
// value serves every resource.
type Processor struct{}

// New returns the plain-text reader.
func New() Processor { return Processor{} }

// Extract returns content as it stands.
//
// The bytes are taken as they arrive rather than transcoded. What charset a
// resource declared is a parameter on its content type, and the type is what
// chose this processor; a resource that was not UTF-8 arrives here as the bytes
// it was uploaded as, which is what it will read back as too.
func (Processor) Extract(_ context.Context, content io.Reader) (string, error) {
	read, err := io.ReadAll(content)
	if err != nil {
		return "", err
	}
	return string(read), nil
}
