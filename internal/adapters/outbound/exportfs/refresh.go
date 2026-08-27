package exportfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// cache is what one export holds between materialisations: the content it last
// made, and the stamp of the inputs it made that content from.
//
// The mutex covers the whole of a refresh rather than the fields alone. Two
// readers arriving at a stale export would otherwise both materialise it, which
// for a projection is the expense the mount exists to avoid; the second waits
// and finds the first's answer already fresh.
type cache struct {
	source vfs.FileSystem
	inputs []string
	from   Materialise

	mu    sync.Mutex
	stamp string
	held  []byte
	made  time.Time
}

// content returns what the export holds, materialising it first if the inputs
// have changed since it was last made.
//
// The stamp is taken before the materialisation and not after. An input changed
// while the materialisation is reading it then leaves content newer than the
// stamp recorded beside it, and the next read finds the stamp moved and makes
// the export again — work that was not needed, in the direction that is safe.
// Stamping afterwards would record the new inputs against content that is only
// partly theirs, and no later read would ever notice.
func (c *cache) content(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	stamp, err := c.stampInputs(ctx)
	if err != nil {
		return nil, err
	}
	if c.made.IsZero() || stamp != c.stamp {
		content, err := c.from(ctx, c.source, c.inputs)
		if err != nil {
			return nil, err
		}
		c.held, c.stamp, c.made = content, stamp, time.Now()
	}
	return slices.Clone(c.held), nil
}

// changed is when the held content was last materialised, which is what the
// export reports as its ModTime. It is read after content by the tree above,
// so it describes the answer that was just given rather than the one before it.
func (c *cache) changed() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.made
}

// stampInputs describes the inputs as they are now, closely enough that a
// change to any of them changes the description.
//
// It is what Stat says — a length and a time — rather than what the inputs
// hold, because the inputs are the large side of an export and reading them to
// find out whether they need reading is the cost this plugin exists to avoid.
// A write that leaves both the length and the modification time of an input
// exactly as they were is not seen; on a store whose clock is coarse that is a
// change made inside one tick, and the answer to it is a write that says so
// rather than a stamp that reads more.
//
// An input that is not there is stamped as absent rather than refused. Removing
// what an export is derived from is a change to it like any other, and what to
// answer then is the materialisation's to say.
func (c *cache) stampInputs(ctx context.Context) (string, error) {
	var stamp strings.Builder

	for _, input := range c.inputs {
		info, err := c.source.Stat(ctx, input)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(&stamp, "%s\x00absent\x00", input)
		case err != nil:
			return "", fmt.Errorf("stamp input %q of an export: %w", input, err)
		default:
			fmt.Fprintf(&stamp, "%s\x00%d\x00%d\x00", input, info.Size, info.ModTime.UnixNano())
		}
	}
	return stamp.String(), nil
}
