package projection_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/projection"
)

// The document an operator writes. It is the whole format: everything the two
// schemas are made of is in here, and nothing else has to be written anywhere.
const document = `{
  "name": "uploads",
  "source": {"root": "/resources", "file": "meta.json"},
  "columns": [
    {"name": "resource",     "type": "string", "from": "name"},
    {"name": "content_type", "type": "string", "from": "field", "field": "content_type"},
    {"name": "size",         "type": "int",    "from": "field", "field": "size"},
    {"name": "complete",     "type": "bool",   "from": "field", "field": "complete"}
  ],
  "slice_by": "content_type",
  "rollup": {
    "group_by": ["content_type"],
    "measures": [
      {"name": "resources", "op": "count"},
      {"name": "bytes",     "op": "sum", "column": "size"},
      {"name": "largest",   "op": "max", "column": "size"}
    ]
  }
}`

// A manifest read from its document is the manifest, and both schemas derive
// from it as they do from one written in Go.
func TestDecodeReadsTheDeclaredProjection(t *testing.T) {
	m, err := projection.Decode([]byte(document))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m.Name != "uploads" || m.Source.Root != "/resources" || m.Source.File != "meta.json" {
		t.Fatalf("Decode = %+v, want the uploads projection over /resources", m)
	}
	if got, want := m.SliceSchema(), uploads().SliceSchema(); !slices.Equal(got, want) {
		t.Fatalf("SliceSchema = %v, want %v", got, want)
	}
	if got, want := m.RollupSchema(), uploads().RollupSchema(); !slices.Equal(got, want) {
		t.Fatalf("RollupSchema = %v, want %v", got, want)
	}
}

// A rollup nobody wrote decodes to the union of the slices, so the section is
// optional in the document rather than a stanza saying nothing.
func TestDecodeWithoutARollupSection(t *testing.T) {
	const slicesOnly = `{
	  "name": "uploads",
	  "source": {"root": "/resources", "file": "meta.json"},
	  "columns": [{"name": "content_type", "type": "string", "from": "field", "field": "content_type"}],
	  "slice_by": "content_type"
	}`

	m, err := projection.Decode([]byte(slicesOnly))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got, want := m.RollupSchema(), m.SliceSchema(); !slices.Equal(got, want) {
		t.Fatalf("RollupSchema = %v, want the slice schema %v", got, want)
	}
}

// Bytes that are not a manifest, and a manifest that declares nothing
// materialisable, are refused the same way: whoever holds one from Decode holds
// one whose schemas derive.
func TestDecodeRefusesWhatCannotBeMaterialised(t *testing.T) {
	cases := map[string]string{
		"not a document":        `columns: []`,
		"a document of nothing": `{}`,
		"slicing by an undeclared column": `{
		  "name": "uploads",
		  "source": {"root": "/resources", "file": "meta.json"},
		  "columns": [{"name": "size", "type": "int", "from": "field", "field": "size"}],
		  "slice_by": "content_type"
		}`,
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := projection.Decode([]byte(data)); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("Decode = %v, want fs.ErrInvalid", err)
			}
		})
	}
}
