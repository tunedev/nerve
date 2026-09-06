package localfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// openScope opens the subtree at as a root of its own, refusing a link at any
// component of the path. os.Root follows a link that stays inside it, so
// opening the whole path in one call hands out whatever the link stands for —
// another user's directory, whenever anything with write access to the mount
// planted one. The host offers no no-follow open of a path, so the walk is one:
// a component at a time, each described without following a link.
func openScope(root *os.Root, at string) (*os.Root, error) { return descend(root, at, false) }

// makeScope makes the subtree at, and every directory above it that is not
// there, under the same no-follow walk: a subtree is made where the mount holds
// its path, never where a link on that path points.
func makeScope(root *os.Root, at string) error {
	made, err := descend(root, at, true)
	if err != nil {
		return err
	}
	return made.Close()
}

// descend walks at one component at a time, holding a root on each and giving
// back the one at the end. The mount's own root is duplicated rather than handed
// back, so a caller closes what it was given whatever at was, and closing it
// never closes the mount. creating says a component that is not there is made
// rather than refused.
func descend(root *os.Root, at string, creating bool) (*os.Root, error) {
	held, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for _, step := range elements(at) {
		next, err := enter(held, step, creating)
		if err != nil {
			_ = held.Close()
			return nil, err
		}
		if err := direct(held, step, next); err != nil {
			_ = next.Close()
			_ = held.Close()
			return nil, err
		}
		_ = held.Close()
		held = next
	}
	return held, nil
}

// enter opens the directory named step inside held, making it first when the
// walk is creating one and it is not there.
func enter(held *os.Root, step string, creating bool) (*os.Root, error) {
	opened, err := held.OpenRoot(step)
	if err == nil || !creating || !errors.Is(err, fs.ErrNotExist) {
		return opened, err
	}
	if err := held.Mkdir(step, dirPerm); err != nil {
		return nil, err
	}
	return held.OpenRoot(step)
}

// direct reports whether opening step landed on the entry named there rather
// than on what a link at it stands for. The entry described without following a
// link and the directory the open landed on are the same file only when there
// was no link to follow, and both answers come from handles already held, so
// this is a no-follow open rather than a guard a planter can race: a link swung
// into place either way makes the two disagree and the walk stop.
func direct(held *os.Root, step string, opened *os.Root) error {
	named, err := held.Lstat(step)
	if err != nil {
		return err
	}
	landed, err := opened.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(named, landed) {
		return fmt.Errorf("%q on the subtree path is a link: %w", step, fs.ErrPermission)
	}
	return nil
}
