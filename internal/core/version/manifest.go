package version

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"time"

	"github.com/tunedev/nerve/internal/core/cas"
)

// entry is one path in a version and the object holding what was there. The
// digest is recorded as its spelling, because a version outlives the build that
// wrote it and text is what a later one can still read; it is a cas.Digest in
// memory, so the spelling is checked where the record is decoded and every
// reader after that holds a digest rather than a hope.
type entry struct {
	Path   string     `json:"path"`
	Digest cas.Digest `json:"digest"`
}

// manifest is what a version is: the version it followed, why it was made,
// when, and the content of every path at that moment. Entries are sorted by
// path, which is what lets one walk over two of them answer a diff.
//
// It is stored as an object like any other, so a version's ID is the digest of
// this. Two commits differ as manifests differ: same tree, same parent, same
// message and same instant is one version, and identical content under two
// paths is one object however many versions name it.
type manifest struct {
	Parent  ID        `json:"parent,omitzero"`
	Message string    `json:"message,omitempty"`
	Time    time.Time `json:"time"`
	Entries []entry   `json:"entries"`
}

// encode is what goes into the object store. The field order is the struct's
// and the entries are sorted, so the same version encodes to the same bytes and
// therefore to the same ID.
func (m manifest) encode() ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode version: %w", err)
	}
	return data, nil
}

// decodeManifest reads back what encode wrote. Bytes that are not a manifest
// are fs.ErrInvalid: the object store already proved they are the bytes that
// were asked for, so what is wrong with them is their shape.
func decodeManifest(data []byte) (manifest, error) {
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, fmt.Errorf("decode version: %v: %w", err, fs.ErrInvalid)
	}
	return m, nil
}
