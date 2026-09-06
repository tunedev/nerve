package graphfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"strconv"
	"strings"
	"time"
)

// snapshot is a graph as it stood, and what was said about it when it was
// taken. It is the persisted form as well as the held one, the way a document
// is: there is one shape rather than a second for the blob.
type snapshot struct {
	Taken     time.Time
	Note      string
	Documents map[string]document
}

// take keeps the graph as it stands under label, with note beside it.
//
// It changes nothing, so it does not disturb the label a restore left standing:
// a graph set back to one snapshot and then snapshotted again still stands
// where it was put.
func (f *FS) take(ctx context.Context, label string, note []byte) error {
	if err := checkLabel(label); err != nil {
		return err
	}

	kept := snapshot{Taken: time.Now(), Note: string(note)}
	f.read(func(documents map[string]document) { kept.Documents = maps.Clone(documents) })

	body, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return f.snapshots.Keep(ctx, f.name, label, body)
}

// restore sets the graph back to what the snapshot under label holds, and
// records that it stands there.
//
// The snapshot goes through the same commit every other change does, so a
// restore the record refused is a restore that did not happen here either. The
// snapshot itself is left alone: a graph set back to one is a graph that can be
// set back to it again.
func (f *FS) restore(ctx context.Context, label string) error {
	kept, err := f.fetch(ctx, label)
	if err != nil {
		return err
	}

	return f.save(ctx, func(next *graph) error {
		next.Documents = maps.Clone(kept.Documents)
		if next.Documents == nil {
			next.Documents = map[string]document{}
		}
		next.Standing = label
		next.Restored = time.Now()
		return nil
	})
}

// described is what a snapshot's path holds: when it was taken, how many texts
// it kept and whatever note was written with it.
//
// It is a description rather than the blob. A snapshot is read to decide
// whether to go back to it, and the way back is the restore path; handing over
// the serialised graph would make the mount's own persisted form part of what a
// caller reads.
func described(kept snapshot) []byte {
	lines := "taken " + kept.Taken.UTC().Format(time.RFC3339) + "\n" +
		"texts " + strconv.Itoa(len(kept.Documents)) + "\n"
	if kept.Note != "" {
		lines += "note " + kept.Note + "\n"
	}
	return []byte(lines)
}

// standing is what the restore path holds: the snapshot the graph was set back
// to, while nothing has been written to it since. A graph standing on its own
// writes holds nothing there, which is the empty answer rather than a missing
// path.
func (f *FS) standing() []byte {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.graph.Standing == "" {
		return nil
	}
	return []byte("standing " + f.graph.Standing + "\n" +
		"restored " + f.graph.Restored.UTC().Format(time.RFC3339) + "\n")
}

// fetch reads one snapshot back, whatever the store called a label with nothing
// under it.
func (f *FS) fetch(ctx context.Context, label string) (snapshot, error) {
	if err := checkLabel(label); err != nil {
		return snapshot{}, err
	}

	body, err := f.snapshots.Fetch(ctx, f.name, label)
	if err != nil {
		return snapshot{}, err
	}

	var kept snapshot
	if err := json.Unmarshal(body, &kept); err != nil {
		return snapshot{}, fmt.Errorf("snapshot %q of graph %q is kept as something this build cannot read: %w",
			label, f.name, err)
	}
	return kept, nil
}

// checkLabel refuses a name that is not a label. A label is one element, so a
// name with a separator in it is a path into something the snapshots do not
// have.
func checkLabel(label string) error {
	if label == "" || strings.Contains(label, separator) {
		return fmt.Errorf("snapshot %q: a label is one name: %w", label, fs.ErrInvalid)
	}
	return nil
}
