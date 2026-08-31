package textproc_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/textproc"
)

func TestTextIsItsOwnExtraction(t *testing.T) {
	got, err := textproc.New().Extract(t.Context(), strings.NewReader("a first draft\nand a second line\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if want := "a first draft\nand a second line\n"; got != want {
		t.Errorf("Extract = %q, want %q", got, want)
	}
}

// A resource whose content is only partly there is a read that failed, and the
// stage above has to be told rather than handed the part that arrived.
func TestATruncatedReadIsReported(t *testing.T) {
	cut := errors.New("the mount stopped answering")

	_, err := textproc.New().Extract(t.Context(), &stopsAfter{at: 5, err: cut})

	if !errors.Is(err, cut) {
		t.Errorf("Extract = %v, want %v", err, cut)
	}
}

// stopsAfter hands over at bytes and then fails, which is a stream that went
// away mid-file.
type stopsAfter struct {
	at   int
	err  error
	sent int
}

func (s *stopsAfter) Read(p []byte) (int, error) {
	if s.sent >= s.at {
		return 0, s.err
	}
	n := copy(p, strings.Repeat("a", s.at-s.sent))
	s.sent += n
	return n, nil
}
