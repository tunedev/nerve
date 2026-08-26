package localfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path"
	"strconv"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// AsStreamer reports that a host disk can be moved in pieces, which is the
// point of putting one behind the port: an entry larger than memory is read and
// written without ever being held whole.
func (f *FS) AsStreamer() vfs.Streamer { return f }

// AsTruncater reports that an entry can be cut to a length in place.
func (f *FS) AsTruncater() vfs.Truncater { return f }

// Truncate sets the length of name's content, padding with zero bytes when size
// is past the end. It cuts the entry through an open handle, because a length
// is set on a path only by naming it a second time, and every name here is
// resolved once by the root.
func (f *FS) Truncate(_ context.Context, name string, size int64) error {
	if size < 0 {
		return fmt.Errorf("truncate %q to %d: a length is not negative: %w", name, size, fs.ErrInvalid)
	}

	file, err := f.root.OpenFile(rel(name), os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("truncate %q: %w", name, sentinel(err))
	}
	defer func() { _ = file.Close() }()

	if err := file.Truncate(size); err != nil {
		return fmt.Errorf("truncate %q: %w", name, sentinel(err))
	}
	return nil
}

// OpenRead returns the content of name as a stream. A directory opens on a host
// disk and only fails at the first read, so it is refused here instead: a
// caller finds out from the verb it called.
func (f *FS) OpenRead(_ context.Context, name string) (io.ReadCloser, error) {
	file, err := f.root.Open(rel(name))
	if err != nil {
		return nil, fmt.Errorf("open_read %q: %w", name, sentinel(err))
	}

	found, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("open_read %q: %w", name, sentinel(err))
	}
	if found.IsDir() {
		_ = file.Close()
		return nil, fmt.Errorf("open_read %q: a directory: %w", name, fs.ErrInvalid)
	}
	return file, nil
}

// OpenWrite streams into a file beside the one named and moves it into place at
// Close, so the write is complete exactly when Close returns nil and a reader
// alongside sees the old content or the new one and never half of either.
//
// Whether the entry can be written at all is answered here rather than at
// Close, so a caller learns about a missing parent before it streams a payload
// nothing will accept.
func (f *FS) OpenWrite(_ context.Context, name string) (io.WriteCloser, error) {
	target := rel(name)
	if found, err := f.root.Lstat(target); err == nil && found.IsDir() {
		return nil, fmt.Errorf("open_write %q: a directory: %w", name, fs.ErrInvalid)
	}

	file, temp, err := f.createTemp(path.Dir(path.Join("/", name)))
	if err != nil {
		return nil, fmt.Errorf("open_write %q: %w", name, sentinel(err))
	}
	return &pending{fsys: f, file: file, temp: temp, name: name, target: target}, nil
}

// createTemp makes an entry under dir that nothing else holds, which is where a
// streamed write is collected until Close moves it. os.Root has no CreateTemp
// of its own, and reaching around the root for one would be a hole in the
// containment every other verb goes through, so the name is drawn here and the
// host settles the race by refusing to create one twice.
func (f *FS) createTemp(dir string) (*os.File, string, error) {
	for range 1000 {
		temp := rel(path.Join(dir, ".nerve-write-"+strconv.FormatUint(rand.Uint64(), 36)))
		file, err := f.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_RDWR, filePerm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return file, temp, nil
	}
	return nil, "", fmt.Errorf("no unused name under %q: %w", dir, fs.ErrExist)
}

// pending is a streamed write in progress: a file the caller is filling, and
// the path it takes the place of when the caller closes it.
type pending struct {
	fsys   *FS
	file   *os.File
	temp   string
	name   string
	target string
	closed bool
}

func (p *pending) Write(data []byte) (int, error) {
	if p.closed {
		return 0, fmt.Errorf("write %q: the stream is closed: %w", p.name, fs.ErrClosed)
	}
	return p.file.Write(data)
}

// Close moves what was streamed into place. Closing twice moves nothing the
// second time, so a deferred Close after an explicit one is not a second write.
// Anything that fails leaves the entry as it was and takes the half-written
// file with it.
func (p *pending) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true

	if err := p.file.Chmod(p.mode()); err != nil {
		return p.abandon(err)
	}
	if err := p.file.Close(); err != nil {
		return p.abandon(err)
	}
	if err := p.fsys.root.Rename(p.temp, p.target); err != nil {
		return p.abandon(err)
	}
	return nil
}

// mode is what the entry ends up with: the mode it already had, since Chmod is
// the only verb that sets one, and what a new entry is made with otherwise.
func (p *pending) mode() fs.FileMode {
	found, err := p.fsys.root.Stat(p.target)
	if err != nil {
		return filePerm
	}
	return found.Mode().Perm()
}

func (p *pending) abandon(cause error) error {
	_ = p.fsys.root.Remove(p.temp)
	return fmt.Errorf("close %q: %w", p.name, sentinel(cause))
}
