package queuefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Create makes an empty message at name, replacing the one already queued
// under it.
func (f *FS) Create(ctx context.Context, name string) error {
	return f.Write(ctx, name, nil)
}

// Mkdir has nothing to make under the queue: no directory exists apart from the
// messages under it, and saying so is more useful than refusing a verb a caller
// cannot know to skip. The mount's own two directories are not a caller's to
// make, so anywhere else this is a refusal.
func (f *FS) Mkdir(_ context.Context, name string) error {
	if spot, _ := locate(name); spot != atMessage {
		return fmt.Errorf("mkdir %q: a queue holds messages, not directories: %w", name, fs.ErrPermission)
	}
	return nil
}

// Write queues a message at name.
//
// The journal has it before the broker hears of it, and the broker not hearing
// of it is not an error: the message is durable the moment Record returns, and
// what the relay could not carry it carries next time. A write that failed
// because a broker was unreachable would be a write that lost a message to keep
// a notification, which is the wrong way round.
//
// The mode survives the write, so a mode Chmod set is a mode a later write does
// not silently reset.
func (f *FS) Write(ctx context.Context, name string, data []byte) error {
	spot, key := locate(name)
	if spot != atMessage {
		return f.refuse("write", name, spot)
	}

	mode, err := f.modeOf(ctx, key)
	if err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	if err := f.journal.Record(ctx, key, data, mode); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}

	_, _ = f.relay.Carry(ctx)
	return nil
}

// Read takes the message at name out of the queue and hands it back. This is
// the read-destructive descriptor: what one reader receives is gone for the
// next, and a second read of the same name finds nothing there.
//
// The config path is read instead of consumed, and reading it as often as you
// like is the point of it being a separate path.
func (f *FS) Read(ctx context.Context, name string) ([]byte, error) {
	spot, key := locate(name)
	switch spot {
	case atConfig:
		return f.description(), nil
	case atMessage:
		body, err := f.journal.Claim(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", name, err)
		}
		return body, nil
	case atRoot, atQueue:
		return nil, fmt.Errorf("read %q: a directory: %w", name, fs.ErrInvalid)
	default:
		return nil, fmt.Errorf("read %q: %w", name, fs.ErrNotExist)
	}
}

// modeOf is the mode a write to key queues under: the one the pending message
// already carries, or the default when nothing is queued there yet.
func (f *FS) modeOf(ctx context.Context, key string) (fs.FileMode, error) {
	entry, err := f.journal.Peek(ctx, key)
	switch {
	case err == nil:
		return entry.Mode.Perm(), nil
	case errors.Is(err, fs.ErrNotExist):
		return fileMode, nil
	default:
		return 0, err
	}
}

// refuse is what every mutating verb says about a path that is not under the
// queue. The mount's shape is fixed, so the refusal is a permission and not a
// missing entry: there is nothing a caller could create first to make it work.
func (f *FS) refuse(verb, name string, where spot) error {
	if where == outside {
		return fmt.Errorf("%s %q: a queue holds %q and %q: %w", verb, name, Jobs, Config, fs.ErrPermission)
	}
	return fmt.Errorf("%s %q: the shape of the mount: %w", verb, name, fs.ErrPermission)
}
