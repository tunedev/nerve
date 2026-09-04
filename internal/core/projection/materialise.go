package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Encoder writes one table as the bytes of a columnar file.
//
// It is a port because a real second implementation is nameable: DuckDB writes
// Parquet by being run, and an in-process writer would produce the same bytes
// without a process. Neither is named here — an encoder is given a schema and
// rows and returns a file, and what engine did it is a wiring decision.
//
// It returns bytes rather than writing to a path because the tree is the system
// of record. A slice lands wherever its mount is, which may be a keyspace or an
// object store with no path an engine could open, and a rollup reading its
// slices back gets them the same way anything else on the tree does.
type Encoder interface {
	// Extension is what a file this encoder writes is called, without the dot.
	// It is what keeps the format out of the core: nothing above knows the
	// slices are Parquet.
	Extension() string

	// Encode returns the file holding rows under schema. A table with no rows
	// is a file with no rows and the same schema, not an error.
	Encode(ctx context.Context, schema Schema, rows []Row) ([]byte, error)
}

// Materialiser writes the slices of a projection onto the tree, reading the
// records they come from off the same tree.
//
// It holds no manifest. Which projections a build has is a question about where
// manifests live, and one materialiser serves every manifest handed to it.
type Materialiser struct {
	fsys    vfs.FileSystem
	encoder Encoder
	root    string
	timeout time.Duration
}

// NewMaterialiser returns the materialiser writing under root on fsys, encoding
// each slice with encoder and giving each encode timeout to finish in.
//
// root must be a rooted, clean path such as "/projections" — the mount table's
// rule for a prefix, because materialised tables are where a mount was put.
//
// The timeout is here rather than in the encoder for the reason the model
// gateway's is: an engine is a process or an endpoint that can hang, and how
// long this build waits for one is the caller's policy and not the engine's.
func NewMaterialiser(fsys vfs.FileSystem, encoder Encoder, root string, timeout time.Duration) (*Materialiser, error) {
	if fsys == nil {
		return nil, fmt.Errorf("materialiser has no filesystem: %w", fs.ErrInvalid)
	}
	if encoder == nil {
		return nil, fmt.Errorf("materialiser has no encoder: %w", fs.ErrInvalid)
	}
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("projections root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("materialiser encode timeout is %s, which bounds nothing: %w", timeout, fs.ErrInvalid)
	}
	return &Materialiser{fsys: fsys, encoder: encoder, root: root, timeout: timeout}, nil
}

// Result is where one materialise put a projection: the slice files, in the
// order the slices are cut, and the one combined table they roll up into.
type Result struct {
	Slices []string
	Rollup string
}

// Materialise writes one file per slice of m and one combined file the slices
// roll up into, and returns where they went.
//
// A manifest is validated first, so a materialiser given one that was built
// rather than decoded refuses it here instead of writing half a projection.
//
// It converges rather than accumulates. Running it again over records nothing
// has touched leaves the same files holding the same bytes, and running it over
// records that have changed leaves what those records say and nothing an
// earlier run said.
//
// Nothing is written for a projection whose source holds no records, the rollup
// included, and whatever an earlier run wrote for it is taken away. That is the
// honest answer: an empty projection is no slices, and a single empty file
// would be a slice under a key no record ever had.
func (m *Materialiser) Materialise(ctx context.Context, manifest Manifest) (Result, error) {
	if err := manifest.Valid(); err != nil {
		return Result{}, fmt.Errorf("materialise: %w", err)
	}
	rows, err := m.read(ctx, manifest)
	if err != nil {
		return Result{}, err
	}
	cut, err := manifest.slices(rows)
	if err != nil {
		return Result{}, err
	}
	return m.write(ctx, manifest, cut)
}

// read is every row the manifest's source holds, in the order the tree lists
// the records.
func (m *Materialiser) read(ctx context.Context, manifest Manifest) ([]Row, error) {
	entries, err := m.fsys.ReadDir(ctx, manifest.Source.Root)
	if err != nil {
		return nil, fmt.Errorf("materialise %q: read %s: %w", manifest.Name, manifest.Source.Root, err)
	}

	rows := make([]Row, 0, len(entries))
	for _, entry := range entries {
		record, isRecord, err := m.record(ctx, manifest, entry)
		if err != nil {
			return nil, err
		}
		if !isRecord {
			continue
		}
		row, err := manifest.row(record)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// record is the record entry holds, reporting whether it is one at all.
//
// A directory without the manifest's record file is not a record and is passed
// over: the file is what makes a directory one, so an area holding a workspace's
// resources beside something else projects the resources and ignores the rest.
func (m *Materialiser) record(ctx context.Context, manifest Manifest, entry vfs.FileInfo) (Record, bool, error) {
	if !entry.IsDir() {
		return Record{}, false, nil
	}
	at := path.Join(manifest.Source.Root, entry.Name, manifest.Source.File)
	data, err := m.fsys.Read(ctx, at)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("materialise %q: read %s: %w", manifest.Name, at, err)
	}

	// UseNumber keeps a JSON number as it was written, so an int column reading
	// a value larger than a float64 spells exactly gets the value and not the
	// nearest one.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return Record{}, false, fmt.Errorf("materialise %q: %s: %v: %w", manifest.Name, at, err, fs.ErrInvalid)
	}
	return Record{Name: entry.Name, Doc: doc}, true, nil
}

// write converges the projection on what its records now say: one file per
// slice, the table they combine into beside them, and nothing left over from a
// run whose records cut a slice these do not.
//
// Converging is the whole of it rather than writing being. A materialise that
// only wrote would leave a tree whose tables depend on which runs came before,
// so running it twice would not mean what running it once does.
func (m *Materialiser) write(ctx context.Context, manifest Manifest, cut []Slice) (Result, error) {
	dir := path.Join(m.root, manifest.Name)
	if len(cut) == 0 {
		return Result{}, m.clear(ctx, manifest, dir)
	}
	if err := m.fsys.Mkdir(ctx, dir); err != nil {
		return Result{}, fmt.Errorf("materialise %q: %w", manifest.Name, err)
	}

	schema := manifest.SliceSchema()
	written := make([]string, 0, len(cut))
	for _, slice := range cut {
		data, err := m.encode(ctx, schema, slice.Rows)
		if err != nil {
			return Result{}, fmt.Errorf("materialise %q: slice %q: %w", manifest.Name, slice.Key, err)
		}
		at := path.Join(dir, slice.Key+"."+m.encoder.Extension())
		if err := m.fsys.Write(ctx, at, data); err != nil {
			return Result{}, fmt.Errorf("materialise %q: write %s: %w", manifest.Name, at, err)
		}
		written = append(written, at)
	}
	if err := m.prune(ctx, manifest, dir, written); err != nil {
		return Result{}, err
	}

	rollup, err := m.writeRollup(ctx, manifest, cut)
	if err != nil {
		return Result{}, err
	}
	return Result{Slices: written, Rollup: rollup}, nil
}

// prune takes away everything in dir this run did not write. The directory
// holds a projection's slices and nothing else, so an entry no slice claims is
// one some earlier records cut and these records do not.
//
// It matches on what was written rather than on what a slice is called, so a
// build that changes encoder finds the files the old one wrote stale too.
func (m *Materialiser) prune(ctx context.Context, manifest Manifest, dir string, written []string) error {
	entries, err := m.fsys.ReadDir(ctx, dir)
	if err != nil {
		return fmt.Errorf("materialise %q: read %s: %w", manifest.Name, dir, err)
	}
	for _, entry := range entries {
		at := path.Join(dir, entry.Name)
		if slices.Contains(written, at) {
			continue
		}
		if err := m.fsys.RemoveAll(ctx, at); err != nil {
			return fmt.Errorf("materialise %q: remove %s: %w", manifest.Name, at, err)
		}
	}
	return nil
}

// clear takes away what a projection whose source now holds no records was left
// holding. Nothing is written for an empty projection, so the tables of one
// that has emptied have to go rather than stand as an answer no record gives
// any more.
func (m *Materialiser) clear(ctx context.Context, manifest Manifest, dir string) error {
	for _, at := range []string{dir, m.rollupAt(manifest)} {
		if err := m.fsys.RemoveAll(ctx, at); err != nil {
			return fmt.Errorf("materialise %q: remove %s: %w", manifest.Name, at, err)
		}
	}
	return nil
}

// writeRollup encodes the combined table and puts it beside the directory the
// slices are in, so a projection has one of them whatever its slices are
// called. Inside that directory it would collide with a slice whose key is the
// projection's own name.
func (m *Materialiser) writeRollup(ctx context.Context, manifest Manifest, cut []Slice) (string, error) {
	data, err := m.encode(ctx, manifest.RollupSchema(), manifest.combine(cut))
	if err != nil {
		return "", fmt.Errorf("materialise %q: rollup: %w", manifest.Name, err)
	}
	at := m.rollupAt(manifest)
	if err := m.fsys.Write(ctx, at, data); err != nil {
		return "", fmt.Errorf("materialise %q: write %s: %w", manifest.Name, at, err)
	}
	return at, nil
}

// rollupAt is where a projection's combined table goes.
func (m *Materialiser) rollupAt(manifest Manifest) string {
	return path.Join(m.root, manifest.Name+"."+m.encoder.Extension())
}

// encode is one slice through the encoder, bounded, so an engine that never
// answers is a deadline rather than a materialise that never returns.
func (m *Materialiser) encode(ctx context.Context, schema Schema, rows []Row) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	return m.encoder.Encode(bounded, schema, rows)
}
