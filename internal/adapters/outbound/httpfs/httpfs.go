// Package httpfs mounts an HTTP origin as a read-only subtree. A path under the
// mount is a path on the origin: reading one is a GET, describing one is a HEAD,
// and nothing else is offered because nothing else is what an origin promises.
//
// It claims the object-store descriptor as well as read-only, which is the
// honest reading of a URL space: names are prefixes rather than a tree, there is
// no directory apart from the things under one, and an origin has no listing to
// answer ReadDir with. Claiming a tree would make a caller expect a walk that
// cannot be done.
//
// Every payload is bounded. The origin is somebody else's, so how much it will
// send is not this process's decision to leave open, and a read that would go
// past the bound fails rather than filling memory with it.
package httpfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// entryMode is what an origin's paths report. The bits are read-only because
// the descriptor is.
const entryMode = 0o444

// FS is one HTTP origin answering the port.
//
// No timeout is set on the client. Every call is made on the caller's context,
// and the registry has already bounded that: a second timeout here would be a
// second layer deciding how long a call may take.
type FS struct {
	base   *url.URL
	client *http.Client
	max    int64
}

// New returns a mount over base, handing back at most max bytes per read. It
// resolves the URL here so a misspelled origin is a boot failure rather than
// the first read's.
func New(base string, max int64, client *http.Client) (*FS, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("origin %q: %w", base, fs.ErrInvalid)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("origin %q: want an http or https URL: %w", base, fs.ErrInvalid)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("origin %q: want a host: %w", base, fs.ErrInvalid)
	}
	if max <= 0 {
		return nil, fmt.Errorf("origin %q: a read bounded to %d bytes hands back nothing: %w", base, max, fs.ErrInvalid)
	}
	if client == nil {
		client = &http.Client{}
	}
	return &FS{base: parsed, client: client, max: max}, nil
}

// CloseIdleConnections drops the connections the client is holding open. A
// mount is built once and lives as long as the process, so this is a test's
// business rather than an operator's.
func (f *FS) CloseIdleConnections() { f.client.CloseIdleConnections() }

// Capabilities reports a read-only object store at every path: an origin is
// somebody else's to write, and its names are prefixes rather than a tree.
func (f *FS) Capabilities(string) vfs.Caps {
	return vfs.Caps{ReadOnly: true, ObjectStore: true}
}

// Read returns what the origin serves at path, up to the mount's bound.
func (f *FS) Read(ctx context.Context, name string) ([]byte, error) {
	body, err := f.get(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	content, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w: %w", name, err, vfs.ErrTransient)
	}
	return content, nil
}

// Stat describes path from what a HEAD says about it, without fetching it.
func (f *FS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	response, err := f.do(ctx, http.MethodHead, name)
	if err != nil {
		return vfs.FileInfo{}, err
	}
	defer drain(response)

	return f.describe(name, response), nil
}

// ReadDir refuses. An origin serves what it is asked for and lists nothing, so
// there is no answer to give — which is what the object-store descriptor warns
// a caller about before it asks.
func (f *FS) ReadDir(_ context.Context, name string) ([]vfs.FileInfo, error) {
	return nil, fmt.Errorf("read_dir %q: an HTTP origin has no listing: %w", name, fs.ErrInvalid)
}

// AsStreamer answers the streaming verbs, because a response body already is a
// stream: reading one in pieces is what the origin was going to do anyway.
func (f *FS) AsStreamer() vfs.Streamer { return f }

// AsSymlinker reports that an origin has no link verbs.
func (f *FS) AsSymlinker() vfs.Symlinker { return nil }

// AsTruncater reports that an origin has no truncate verb.
func (f *FS) AsTruncater() vfs.Truncater { return nil }

// OpenRead returns what the origin serves at path as a stream, bounded the same
// way a whole read is. The caller closes it.
func (f *FS) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	return f.get(ctx, name)
}

// OpenWrite refuses: the mount is read-only, and a stream is the one way a
// refusal could be walked around.
func (f *FS) OpenWrite(_ context.Context, name string) (io.WriteCloser, error) {
	return nil, f.refuse("open_write", name)
}

// Create refuses: the origin is somebody else's.
func (f *FS) Create(_ context.Context, name string) error { return f.refuse("create", name) }

// Mkdir refuses: names on an origin are prefixes, and there is nothing to make.
func (f *FS) Mkdir(_ context.Context, name string) error { return f.refuse("mkdir", name) }

// Remove refuses.
func (f *FS) Remove(_ context.Context, name string) error { return f.refuse("remove", name) }

// RemoveAll refuses.
func (f *FS) RemoveAll(_ context.Context, name string) error { return f.refuse("remove_all", name) }

// Write refuses.
func (f *FS) Write(_ context.Context, name string, _ []byte) error { return f.refuse("write", name) }

// Rename refuses.
func (f *FS) Rename(_ context.Context, oldPath, _ string) error { return f.refuse("rename", oldPath) }

// Chmod refuses.
func (f *FS) Chmod(_ context.Context, name string, _ fs.FileMode) error {
	return f.refuse("chmod", name)
}

func (f *FS) refuse(verb, name string) error {
	return fmt.Errorf("%s %q on a read-only HTTP origin: %w", verb, name, fs.ErrPermission)
}

// get fetches path and hands back a body already bounded by the mount's limit,
// so no caller has to remember to bound it.
func (f *FS) get(ctx context.Context, name string) (io.ReadCloser, error) {
	response, err := f.do(ctx, http.MethodGet, name)
	if err != nil {
		return nil, err
	}
	return bounded(response, f.max, name), nil
}

// do makes one request and turns the origin's status codes into the vocabulary
// the port above speaks.
func (f *FS) do(ctx context.Context, method, name string) (*http.Response, error) {
	target := *f.base
	target.Path = path.Join(f.base.Path, path.Clean("/"+strings.TrimPrefix(name, "/")))

	request, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", method, name, err)
	}

	response, err := f.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w: %w", method, name, err, vfs.ErrTransient)
	}
	if response.StatusCode >= http.StatusBadRequest {
		defer drain(response)
		return nil, statusError(method, name, response)
	}
	return response, nil
}

// describe is what the port says about a path the origin answered a HEAD for.
// An origin that says nothing about length or age leaves the zero value, which
// is the honest answer: it did not say.
func (f *FS) describe(name string, response *http.Response) vfs.FileInfo {
	size := response.ContentLength
	if size < 0 {
		size = 0
	}

	var changed time.Time
	if when, err := http.ParseTime(response.Header.Get("Last-Modified")); err == nil {
		changed = when
	}
	return vfs.FileInfo{Name: entryName(name), Size: size, Mode: entryMode, ModTime: changed}
}

// entryName is the final element of a path, which is what the port reports as a
// name.
func entryName(name string) string {
	clean := path.Clean("/" + strings.TrimPrefix(name, "/"))
	if clean == "/" {
		return "/"
	}
	return path.Base(clean)
}

// statusError translates one refusal. The transient mark goes on here, at the
// plugin's edge, rather than leaving the classifier to learn a wire's
// vocabulary.
func statusError(method, name string, response *http.Response) error {
	where := fmt.Sprintf("%s %q: %s", method, name, response.Status)

	switch {
	case response.StatusCode == http.StatusNotFound, response.StatusCode == http.StatusGone:
		return fmt.Errorf("%s: %w", where, fs.ErrNotExist)
	case response.StatusCode == http.StatusForbidden, response.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%s: %w", where, fs.ErrPermission)
	case response.StatusCode == http.StatusTooManyRequests, response.StatusCode >= http.StatusInternalServerError:
		return fmt.Errorf("%s: %w", where, vfs.ErrTransient)
	default:
		return fmt.Errorf("%s: %w", where, fs.ErrInvalid)
	}
}

// bounded caps what a body will hand back. It reads one byte past the bound so
// that going past it is an error rather than a silent truncation: a caller
// given the first megabyte of a larger file has no way to tell.
func bounded(response *http.Response, max int64, name string) io.ReadCloser {
	return &boundedBody{
		reader: io.LimitReader(response.Body, max+1),
		closer: response.Body,
		max:    max,
		name:   name,
	}
}

type boundedBody struct {
	reader io.Reader
	closer io.Closer
	max    int64
	name   string
	read   int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += int64(n)
	if b.read > b.max {
		return n, fmt.Errorf("read %q: the origin sent more than the %d bytes this mount is bounded to: %w", b.name, b.max, fs.ErrInvalid)
	}
	return n, err
}

func (b *boundedBody) Close() error { return b.closer.Close() }

// drain reads what is left of a response and closes it, so the connection goes
// back to the pool instead of being dropped.
func drain(response *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	_ = response.Body.Close()
}
