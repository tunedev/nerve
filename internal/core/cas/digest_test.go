package cas_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/tunedev/nerve/internal/core/cas"
)

// Identical content is one digest, and that is the whole of what makes it
// identical as far as the store is concerned.
func TestComputeNamesContentAndNothingElse(t *testing.T) {
	first, second := []byte("a version of the truth"), []byte("a version of the truth")
	if cas.Compute(first) != cas.Compute(second) {
		t.Error("identical content computed two different digests")
	}
	if cas.Compute([]byte("one")) == cas.Compute([]byte("two")) {
		t.Error("different content computed one digest")
	}
	if got := cas.Compute(nil); got != cas.Compute([]byte{}) {
		t.Errorf("nil and empty content computed different digests: %s", got)
	}
}

func TestDigestSpellsItsAlgorithmAndHash(t *testing.T) {
	// The empty string's SHA-256, which is the one digest worth pinning: it is
	// what an empty file commits as, and a change to the algorithm shows here.
	const want = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	if got := cas.Compute(nil).String(); got != want {
		t.Errorf("Compute(nil) = %s, want %s", got, want)
	}
}

func TestDigestRoundTripsThroughItsSpelling(t *testing.T) {
	want := cas.Compute([]byte("what a ref records"))

	got, err := cas.ParseDigest(want.String())
	if err != nil {
		t.Fatalf("ParseDigest(%s): %v", want, err)
	}
	if got != want {
		t.Errorf("ParseDigest(%s) = %s", want, got)
	}
}

func TestParseDigestRefusesWhatIsNotOne(t *testing.T) {
	spellings := map[string]string{
		"no algorithm":            "e3b0c44298fc1c14",
		"empty algorithm":         ":e3b0c44298fc1c14",
		"no hash":                 "sha256:",
		"too short to lay out":    "sha256:e3",
		"an odd number of digits": "sha256:e3b",
		"not hex":                 "sha256:zzzzzzzz",
		"upper case hex":          "sha256:E3B0C442",
	}

	for name, spelling := range spellings {
		t.Run(name, func(t *testing.T) {
			got, err := cas.ParseDigest(spelling)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("ParseDigest(%q) = %s, %v, want fs.ErrInvalid", spelling, got, err)
			}
			if !strings.Contains(err.Error(), spelling) {
				t.Errorf("error %q does not name what was parsed", err)
			}
		})
	}
}

// An algorithm this build does not compute is still a spelling something
// recorded, so parsing it is not this process's decision to refuse. Content
// stored under it is simply not there.
func TestParseDigestAcceptsAnAlgorithmTheStoreDoesNotCompute(t *testing.T) {
	if _, err := cas.ParseDigest("sha512:e3b0c44298fc1c14"); err != nil {
		t.Errorf("ParseDigest of a sha512 spelling: %v", err)
	}
}

// Content larger than memory is digested by passing through, and what comes out
// is the digest the whole bytes would have computed. A second spelling of the
// hash here is a second answer to "what is this content called".
func TestComputeFromMatchesComputeOverTheSameBytes(t *testing.T) {
	content := []byte("a file too large to hold, in miniature")

	got, err := cas.ComputeFrom(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("ComputeFrom: %v", err)
	}
	if want := cas.Compute(content); got != want {
		t.Errorf("ComputeFrom = %s, want %s", got, want)
	}
}

func TestComputeFromReportsWhatTheReaderFailedWith(t *testing.T) {
	broken := errors.New("the mount went away mid-read")

	if _, err := cas.ComputeFrom(iotest.ErrReader(broken)); !errors.Is(err, broken) {
		t.Errorf("ComputeFrom over a failing reader = %v, want %v", err, broken)
	}
}

// The zero digest names nothing, and nothing is what it spells. A record that
// says nothing reads back as the zero digest rather than as an error, which is
// what lets a ref or a working copy be put back to standing nowhere.
func TestTheZeroDigestSpellsItselfAsNothing(t *testing.T) {
	var zero cas.Digest

	if !zero.IsZero() {
		t.Error("the zero digest does not report itself as zero")
	}
	if got := zero.String(); got != "" {
		t.Errorf("the zero digest spells itself %q, want nothing", got)
	}

	got, err := cas.ParseDigest("")
	if err != nil {
		t.Fatalf("ParseDigest(\"\") = %v", err)
	}
	if !got.IsZero() {
		t.Errorf("ParseDigest(\"\") = %s, want the zero digest", got)
	}
}

// A digest in a record is text, and reading one back is where a spelling that
// was never a digest is refused — before anything holds it as one.
func TestDigestRoundTripsThroughARecord(t *testing.T) {
	want := cas.Compute([]byte("what a version records"))

	record, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal %s: %v", want, err)
	}
	if string(record) != `"`+want.String()+`"` {
		t.Errorf("recorded as %s, want the spelling", record)
	}

	var got cas.Digest
	if err := json.Unmarshal(record, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", record, err)
	}
	if got != want {
		t.Errorf("read back %s, want %s", got, want)
	}

	if err := json.Unmarshal([]byte(`"not a digest"`), &got); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("reading back what is not a digest = %v, want fs.ErrInvalid", err)
	}
}
