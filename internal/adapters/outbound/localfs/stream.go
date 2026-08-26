package localfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// AsStreamer reports that a host disk can be moved in pieces, which is the
// point of putting one behind the port: an entry larger than memory is read and
// written without ever being held whole.
func (f *FS) AsStreamer() vfs.Streamer { return f }

// AsTruncater reports that an entry can be cut to a length in place.
func (f *FS) AsTruncater() vfs.Truncater { return f }

// Truncate sets the length of name's content, padding with zero bytes when size
// is past the end.
func (f *FS) Truncate(_ context.Context, name string, size int64) error {
	if size < 0 {
		return fmt.Errorf("truncate %q to %d: a length is not negative: %w", name, size, fs.ErrInvalid)
	}
	if err := os.Truncate(f.host(name), size); err != nil {
		return fmt.Errorf("truncate %q: %w", name, invalid(err))
	}
	return nil
}

// OpenRead returns the content of name as a stream. A directory opens on a host
// disk and only fails at the first read, so it is refused here instead: a
// caller finds out from the verb it called.
func (f *FS) OpenRead(_ context.Context, name string) (io.ReadCloser, error) {
	file, err := os.Open(f.host(name))
	if err != nil {
		return nil, fmt.Errorf("open_read %q: %w", name, invalid(err))
	}

	found, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("open_read %q: %w", name, invalid(err))
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
	host := f.host(name)
	if found, err := os.Lstat(host); err == nil && found.IsDir() {
		return nil, fmt.Errorf("open_write %q: a directory: %w", name, fs.ErrInvalid)
	}

	temp, err := os.CreateTemp(filepath.Dir(host), ".nerve-write-*")
	if err != nil {
		return nil, fmt.Errorf("open_write %q: %w", name, invalid(err))
	}
	return &pending{file: temp, name: name, target: host}, nil
}

// pending is a streamed write in progress: a file the caller is filling, and
// the path it takes the place of when the caller closes it.
type pending struct {
	file   *os.File
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
	if err := os.Rename(p.file.Name(), p.target); err != nil {
		return p.abandon(err)
	}
	return nil
}

// mode is what the entry ends up with: the mode it already had, since Chmod is
// the only verb that sets one, and what a new entry is made with otherwise.
func (p *pending) mode() fs.FileMode {
	found, err := os.Stat(p.target)
	if err != nil {
		return filePerm
	}
	return found.Mode().Perm()
}

func (p *pending) abandon(cause error) error {
	_ = os.Remove(p.file.Name())
	return fmt.Errorf("close %q: %w", p.name, invalid(cause))
}
