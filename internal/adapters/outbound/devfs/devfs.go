// Package devfs is the tree's version of the host's device nodes: a sink that
// swallows whatever is written to it, and two sources that hand back as much as
// they were asked to hold.
//
// It exists to be the awkward mount. Everything else in the tree either stores
// what it is given or refuses everything, and devfs does neither: writing to
// its sink succeeds and changes nothing, while the entry beside it refuses the
// same verb. That is capability varying inside one mount, which is why Caps is
// data resolved per path rather than a type a caller can assert on — and it is
// the case a plugin has to exist for, because a descriptor nothing exercises is
// a descriptor nobody has proved.
package devfs

import (
	"context"
	"crypto/rand"

	"github.com/tunedev/nerve/internal/adapters/outbound/synth"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The names this mount answers for.
const (
	// Null swallows writes and reads back as nothing.
	Null = "null"
	// Zero hands back zero bytes.
	Zero = "zero"
	// Random hands back bytes nobody can predict.
	Random = "random"
)

// FS is one device mount. It is a synthetic tree with one hole in its
// read-only-ness: the sink takes writes, and says so.
type FS struct {
	*synth.Tree
}

// New returns a mount whose sources hand back size bytes per read. The size is
// what makes them answerable at all: the host's /dev/zero is endless, and a
// verb that returns the whole content cannot describe endless.
func New(size int) *FS {
	block := func(fill func([]byte) error) synth.Entry {
		return synth.Entry{Content: func(context.Context) ([]byte, error) {
			buffer := make([]byte, size)
			if err := fill(buffer); err != nil {
				return nil, err
			}
			return buffer, nil
		}}
	}

	return &FS{Tree: synth.NewTree(map[string]synth.Entry{
		Null:   {Content: func(context.Context) ([]byte, error) { return nil, nil }},
		Zero:   block(func([]byte) error { return nil }),
		Random: block(func(buffer []byte) error { _, err := rand.Read(buffer); return err }),
	})}
}

// Capabilities reports read-only everywhere but the sink, which takes writes.
// The descriptor set has no finer word for a thing that accepts content and
// keeps none, so the sink claims a plain read-write path: a caller reading
// ReadOnly and finding it false is not misled about the verb it is about to
// call, which is what the descriptor is for.
func (f *FS) Capabilities(name string) vfs.Caps {
	if synth.Name(name) == Null {
		return vfs.Caps{}
	}
	return f.Tree.Capabilities(name)
}

// Write swallows what the sink is given and refuses everywhere else. The
// content is not held, not counted and not passed on: a caller writing here has
// asked for it to go nowhere.
func (f *FS) Write(ctx context.Context, name string, data []byte) error {
	if synth.Name(name) == Null {
		return nil
	}
	return f.Tree.Write(ctx, name, data)
}
