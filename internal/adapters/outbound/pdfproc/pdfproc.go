// Package pdfproc reads PDF, which is the format that makes the seam worth
// having: nothing about it looks like plain text, and none of what it takes to
// read one appears anywhere else in the process.
//
// The parsing is github.com/ledongthuc/pdf, a fork of rsc.io/pdf with plain
// text extraction on top. It is pure Go with no dependencies of its own, which
// is what a laptop with nothing installed needs. Which library reads a PDF is
// this package's business and nobody else's: a second implementation — rsc.io/pdf
// with the layout heuristics written here, or pdfcpu — is a change inside this
// file.
//
// A PDF is read whole rather than streamed. The format's cross-reference table
// is at the end of the file and names offsets into the rest of it, so there is
// no reading one from front to back; the parser wants random access and this
// package gives it the bytes it was handed.
package pdfproc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/ledongthuc/pdf"
)

// MediaType is the type this package reads.
const MediaType = "application/pdf"

// Processor is the reader this package registers. It holds nothing, so one
// value serves every resource.
type Processor struct{}

// New returns the PDF reader.
func New() Processor { return Processor{} }

// Extract returns the words of every page, in page order, one page's text per
// line.
//
// A file that is not a PDF, or is one that this parser cannot follow, comes
// back as an error wrapping fs.ErrInvalid: the resource declared a format its
// content is not, and no amount of running the job again changes that.
//
// The parser reports some of what it cannot follow by panicking, which is the
// shape rsc.io/pdf chose and the fork kept. Recovering here is what keeps one
// malformed upload from ending the worker that claimed it, and it happens at
// this boundary because this is the only place that knows the panic is a parse
// failure rather than a bug.
func (Processor) Extract(_ context.Context, content io.Reader) (text string, err error) {
	defer func() {
		if refused := recover(); refused != nil {
			text, err = "", fmt.Errorf("this is not a PDF this build can read: %v: %w", refused, fs.ErrInvalid)
		}
	}()

	whole, err := io.ReadAll(content)
	if err != nil {
		return "", err
	}
	doc, err := pdf.NewReader(bytes.NewReader(whole), int64(len(whole)))
	if err != nil {
		return "", fmt.Errorf("this is not a PDF this build can read: %w: %w", err, fs.ErrInvalid)
	}

	var pages []string
	fonts := make(map[string]*pdf.Font)
	for number := 1; number <= doc.NumPage(); number++ {
		page := doc.Page(number)
		for _, name := range page.Fonts() {
			if _, cached := fonts[name]; !cached {
				font := page.Font(name)
				fonts[name] = &font
			}
		}
		words, err := page.GetPlainText(fonts)
		if err != nil {
			return "", fmt.Errorf("page %d: %w: %w", number, err, fs.ErrInvalid)
		}
		pages = append(pages, words)
	}
	return strings.Join(pages, "\n"), nil
}
