package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// sha256Algorithm is the one hash the store computes. The algorithm is part of
// every digest's spelling anyway, so a second one arrives as another constant
// and a wider Compute rather than as a migration of everything already stored.
const sha256Algorithm = "sha256"

// Digest names content by the hash of its own bytes. It is the only handle the
// store answers to: a caller that has one has said what it wants without
// knowing where anything lives.
//
// Its fields are unexported so that Compute and ParseDigest are the only ways
// to get one. A digest assembled by hand could name a path the store then has
// to defend itself against, and there is nothing a caller could put in those
// fields that it did not get from content or from a recorded spelling.
//
// The zero value names nothing. It is comparable, so identical content compares
// equal and a digest is usable as a map key.
type Digest struct {
	algorithm string
	hex       string
}

// Compute returns the digest of data.
func Compute(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest{algorithm: sha256Algorithm, hex: hex.EncodeToString(sum[:])}
}

// ComputeFrom returns the digest of everything r yields, without holding it.
// It is what a caller with content larger than memory hashes through, and it
// answers what Compute would have over the same bytes.
func ComputeFrom(r io.Reader) (Digest, error) {
	sum := sha256.New()
	if _, err := io.Copy(sum, r); err != nil {
		return Digest{}, fmt.Errorf("compute digest: %w", err)
	}
	return Digest{algorithm: sha256Algorithm, hex: hex.EncodeToString(sum.Sum(nil))}, nil
}

// IsZero reports whether d names nothing. It is what a caller holding a digest
// that may have come from an unset record asks before using it, and what tells
// encoding/json to leave such a field out.
func (d Digest) IsZero() bool { return d == Digest{} }

// String is the canonical spelling, "<algorithm>:<hex>". It is what a ref or a
// manifest records, and what ParseDigest reads back. The zero digest spells
// itself as nothing, because a record saying nothing is what it means.
func (d Digest) String() string {
	if d.IsZero() {
		return ""
	}
	return d.algorithm + ":" + d.hex
}

// MarshalText writes the spelling, so a digest in a record is the text every
// build that reads the record can still read.
func (d Digest) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText reads a spelling back, and refuses one that is not a digest.
// That refusal is the point: a record is where a digest that was never computed
// gets in, so it is where the check belongs rather than at the first use.
func (d *Digest) UnmarshalText(spelling []byte) error {
	parsed, err := ParseDigest(string(spelling))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// ParseDigest reads back what String wrote, the zero digest included. It
// accepts any algorithm name, because a spelling recorded before this process
// started is not this process's to reject; content stored under an algorithm
// the store does not compute is simply not there.
func ParseDigest(spelling string) (Digest, error) {
	if spelling == "" {
		return Digest{}, nil
	}
	algorithm, digits, found := strings.Cut(spelling, ":")
	if !found || algorithm == "" {
		return Digest{}, fmt.Errorf("parse digest %q: want %q: %w", spelling, "<algorithm>:<hex>", fs.ErrInvalid)
	}
	// Four is the shortest hash the layout can hold: two digits name the
	// fanout directory and what is left has to name the object inside it.
	if len(digits) < 4 || len(digits)%2 != 0 {
		return Digest{}, fmt.Errorf("parse digest %q: want an even %d or more hex digits, got %d: %w", spelling, 4, len(digits), fs.ErrInvalid)
	}
	if _, err := hex.DecodeString(digits); err != nil || strings.ToLower(digits) != digits {
		return Digest{}, fmt.Errorf("parse digest %q: want lower-case hex: %w", spelling, fs.ErrInvalid)
	}
	return Digest{algorithm: algorithm, hex: digits}, nil
}
