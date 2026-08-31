package pdfproc_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/pdfproc"
)

// The acceptance for this package: a PDF is not text, and what comes back is
// the words rather than the file.
func TestAPDFComesBackAsItsWords(t *testing.T) {
	got, err := pdfproc.New().Extract(t.Context(), bytes.NewReader(document(t, "a first draft")))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(got, "a first draft") {
		t.Errorf("Extract = %q, want the page's words in it", got)
	}
	if strings.Contains(got, "%PDF") || strings.Contains(got, "endstream") {
		t.Errorf("Extract = %q, want the words rather than the file", got)
	}
}

// Pages stay apart, so the last word of one is not read as part of the first
// word of the next.
func TestEveryPageIsRead(t *testing.T) {
	got, err := pdfproc.New().Extract(t.Context(), bytes.NewReader(document(t, "a first draft", "a second page")))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(got, "a first draft") || !strings.Contains(got, "a second page") {
		t.Errorf("Extract = %q, want both pages", got)
	}
	if strings.Contains(got, "drafta second") {
		t.Errorf("Extract = %q, want the pages kept apart", got)
	}
}

// A resource that declared PDF and is not one is a document that will never be
// readable, and the refusal says which sort of failure that is.
func TestSomethingThatIsNotAPDFIsRefused(t *testing.T) {
	for what, content := range map[string][]byte{
		"plain text":     []byte("a first draft, in no format at all"),
		"nothing at all": {},
		"a header only":  []byte("%PDF-1.4\n"),
		"a truncated file": bytes.ReplaceAll(document(t, "a first draft"),
			[]byte("startxref"), []byte("startxrfe")),
	} {
		t.Run(what, func(t *testing.T) {
			_, err := pdfproc.New().Extract(t.Context(), bytes.NewReader(content))
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Extract = %v, want fs.ErrInvalid", err)
			}
		})
	}
}

// A cross-reference table pointing at the wrong bytes is where the parser
// panics rather than returning, which is the failure this package recovers at
// its own boundary: one malformed upload must not end the worker that claimed
// it, and without the recover these cases end the test binary instead.
func TestAPDFTheParserPanicsOnComesBackAsAnError(t *testing.T) {
	whole := document(t, "a first draft")
	for what, broken := range map[string][]byte{
		"an offset naming the wrong bytes": bytes.Replace(whole,
			[]byte("\n0000000009 00000 n \n"), []byte("\n0000000123 00000 n \n"), 1),
		"an object header that is not one": bytes.Replace(whole,
			[]byte("1 0 obj"), []byte("1 0 obk"), 1),
	} {
		t.Run(what, func(t *testing.T) {
			_, err := pdfproc.New().Extract(t.Context(), bytes.NewReader(broken))
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Extract = %v, want fs.ErrInvalid", err)
			}
		})
	}
}

// document is a PDF holding one page per line of words, written out by hand
// because a fixture checked in as bytes is a fixture nobody can read. The
// cross-reference table names byte offsets into the file, which is why the
// objects are laid out first and measured afterwards.
func document(t *testing.T, pages ...string) []byte {
	t.Helper()

	const font = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
	objects := []string{"", "", font}

	kids := make([]string, 0, len(pages))
	for _, words := range pages {
		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", words)
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
		content := len(objects)
		objects = append(objects, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>",
			content))
		kids = append(kids, fmt.Sprintf("%d 0 R", len(objects)))
	}
	objects[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages))

	var file bytes.Buffer
	file.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = file.Len()
		fmt.Fprintf(&file, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}

	startxref := file.Len()
	fmt.Fprintf(&file, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, at := range offsets {
		fmt.Fprintf(&file, "%010d 00000 n \n", at)
	}
	fmt.Fprintf(&file, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, startxref)
	return file.Bytes()
}
