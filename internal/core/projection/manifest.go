package projection

import (
	"encoding/json"
	"fmt"
	"io/fs"
)

// Decode reads a manifest document and returns the projection it declares,
// refusing one that declares nothing materialisable.
//
// Bytes that are not a manifest and a manifest that does not validate are both
// fs.ErrInvalid, and deliberately so: a caller holding a Manifest from here
// holds one whose schemas derive, so nothing downstream has to check again.
func Decode(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("decode projection manifest: %v: %w", err, fs.ErrInvalid)
	}
	if err := m.Valid(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
