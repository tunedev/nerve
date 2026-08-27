package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// String is the canonical spelling, "<algorithm>:<hex>". It is what a ref or a
// manifest records, and what ParseDigest reads back.
func (d Digest) String() string { return d.algorithm + ":" + d.hex }

// ParseDigest reads back what String wrote. It accepts any algorithm name,
// because a spelling recorded before this process started is not this process's
// to reject; content stored under an algorithm the store does not compute is
// simply not there.
func ParseDigest(spelling string) (Digest, error) {
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
