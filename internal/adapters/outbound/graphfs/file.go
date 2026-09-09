package graphfs

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/llm"
)

// Create makes an empty text at name, replacing what was there, or takes a
// snapshot with no note beside it.
//
// An empty text names nothing, so it costs no model call and puts no entity in
// the graph. That is not a special case bolted on: there is nothing in it to
// read.
func (f *FS) Create(ctx context.Context, name string) error {
	return f.Write(ctx, name, nil)
}

// Mkdir has nothing to make under the texts: no directory exists apart from the
// texts under it, and saying so is more useful than refusing a verb a caller
// cannot know to skip. The mount's own paths are not a caller's to make,
// so anywhere else this is a refusal.
func (f *FS) Mkdir(_ context.Context, name string) error {
	if spot, _ := locate(name); spot != atDoc {
		return fmt.Errorf("mkdir %q: a graph holds texts, not directories: %w", name, fs.ErrPermission)
	}
	return nil
}

// Write puts a text at name and folds what it names into the graph.
//
// The extraction happens before the graph is touched, so an endpoint that will
// not answer is a write that did not happen. The alternative is a graph that
// quietly stops learning while every write reports success.
//
// The mode survives the write, so a mode Chmod set is a mode a later write does
// not silently reset.
//
// Two other paths take a write, and both are actions rather than content: a
// label under the snapshots keeps the graph as it stands, with the body as its
// note, and the restore path sets the graph back to the label written to it.
func (f *FS) Write(ctx context.Context, name string, data []byte) error {
	spot, key := locate(name)
	switch spot {
	case atDoc:
		return f.writeText(ctx, name, key, data)
	case atSnapshot:
		if err := f.take(ctx, key, data); err != nil {
			return fmt.Errorf("write %q: %w", name, err)
		}
		return nil
	case atRestore:
		if err := f.restore(ctx, strings.TrimSpace(string(data))); err != nil {
			return fmt.Errorf("write %q: %w", name, err)
		}
		return nil
	default:
		return f.refuse("write", name, spot)
	}
}

// writeText is the write that puts a text in the graph: the extraction, and the
// one commit that takes both the text and what it named.
func (f *FS) writeText(ctx context.Context, name, key string, data []byte) error {
	read, err := f.readingOf(ctx, data)
	if err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}

	held, there := f.held(key)
	mode := fs.FileMode(fileMode)
	if there {
		mode = held.Mode.Perm()
	}

	written := document{Body: bytes.Clone(data), Mode: mode, Written: time.Now(), Read: read}
	if err := f.commit(ctx, func(documents map[string]document) error {
		documents[key] = written
		return nil
	}); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	return nil
}

// Read hands back what is at name.
//
// Under the texts it is the text, unchanged and as often as it is asked for.
// Under the expansions it is what the graph says about the entity of that name,
// and under the mentions it is which texts said it, ranked. All three are the
// whole point of the mount: a query reached through the same verb that reads a
// file.
func (f *FS) Read(ctx context.Context, name string) ([]byte, error) {
	spot, rest := locate(name)
	switch spot {
	case atConfig:
		return f.description(), nil
	case atDoc:
		held, there := f.held(rest)
		if !there {
			return nil, fmt.Errorf("read %q: %w", name, fs.ErrNotExist)
		}
		return bytes.Clone(held.Body), nil
	case atEntity:
		return f.expand(rest), nil
	case atMentioned:
		return f.mentions(rest), nil
	case atSnapshot:
		kept, err := f.fetch(ctx, rest)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", name, err)
		}
		return described(kept), nil
	case atRestore:
		return f.standing(), nil
	case atRoot, atCorpus, atNeighbours, atMentions, atSnapshots:
		return nil, fmt.Errorf("read %q: a directory: %w", name, fs.ErrInvalid)
	default:
		return nil, fmt.Errorf("read %q: %w", name, fs.ErrNotExist)
	}
}

// readingOf is what an extractor made of a text, or nothing when there is no
// text.
//
// The endpoint is not probed at boot and is reached only here, on a write.
// Making a mount refuse to come up because an endpoint was down would defeat
// the gateway, whose whole point is that endpoints come and go behind it.
func (f *FS) readingOf(ctx context.Context, text []byte) (llm.Extraction, error) {
	if len(text) == 0 {
		return llm.Extraction{}, nil
	}
	return f.extract.ExtractEntities(ctx, string(text))
}
