package vfs

import (
	"context"
	"io"
	"io/fs"
)

// Call names one call through one mount: which subtree it went to, which kind
// answers there, which verb was asked and which path it named. It is what a
// recorder is handed, and it is the whole of what the core says about a call —
// how long it took is the recorder's to measure, because only the recorder
// knows what it is measuring against.
//
// Verb is the port's own method name, so what an operator reads back is what
// the caller wrote. A verb that names two paths reports the one it starts from;
// the other is where the call is going, and a mount that is slow is slow at the
// path it is working through.
type Call struct {
	// Mount is the prefix the plugin answers for, in the tree's vocabulary.
	Mount string
	// Kind is the registered name of what answers there.
	Kind string
	// Verb is the port method the caller asked for.
	Verb string
	// Path is the path within the mount, in the plugin's vocabulary. Mount and
	// Path joined are the path the caller named: the table strips the prefix
	// before a plugin sees it, and putting it back is what makes one mount's
	// report legible beside another's.
	Path string
}

// Recorder is told that a call is starting and, through the function it hands
// back, what the call returned. It is the port behind traces, metrics and logs:
// the core says what happened, and what that becomes — a span, a histogram, a
// line — is the adapter's business.
//
// The context it returns is what the call runs on, which is how a recorder that
// opens a span gets that span carried into whatever the plugin talks to. A
// recorder that carries nothing returns the context it was given.
type Recorder interface {
	// Begin starts recording call and returns the context the call runs on
	// together with the function that ends it. The function is called exactly
	// once, with what the call returned.
	Begin(ctx context.Context, call Call) (context.Context, Done)
}

// Done ends the call a Recorder began. A nil error is a call that worked.
type Done func(err error)

// NopRecorder records nothing and is the default a registry holds, so a build
// that exports no telemetry runs the same code path as one that does. Wiring
// that is only present in production is wiring nobody tests.
type NopRecorder struct{}

// Begin hands back the caller's own context and a function that does nothing.
func (NopRecorder) Begin(ctx context.Context, _ Call) (context.Context, Done) {
	return ctx, func(error) {}
}

// WithTelemetry returns fsys with every verb reported to recorder as the call
// it is: the mount it went to, the kind that answered, the verb, the path, and
// what came back.
//
// mount and kind are fixed per wrapper because they are fixed per mount. They
// come from the spec the plugin was built from, which is the only place both
// are known — a plugin knows neither where it was mounted nor what name a
// config asks for it by.
//
// It goes on outermost, over the retry, so what it times is what the caller
// waited for. Under the retry it would time one attempt, and a call that took
// three seconds across three attempts would read as three fast calls with the
// waiting nowhere — which is exactly the question this exists to answer.
//
// It goes under the mount table rather than over it, unlike WithWriteObserver,
// because what is being measured is one mount. Over the table there is one
// timer for the whole tree and no way to say which mount spent it, which is the
// first thing anyone asks.
//
// The streaming verbs are recorded at the open and not beyond it, which is the
// split WithTimeout already makes: an open is a discrete call and a transfer is
// not, so the two get separate lifetimes. A transfer that runs for a minute is
// a healthy download rather than a slow call, and timing it as one would put it
// in the same series as a Stat that hung.
func WithTelemetry(fsys FileSystem, recorder Recorder, mount, kind string) FileSystem {
	return telemetered{fsys: fsys, recorder: recorder, mount: mount, kind: kind}
}

// telemetered is one plugin under one recorder. It stores nothing about a call:
// what is reported is the verb's own arguments, and how long it took is
// measured by whoever the report goes to.
type telemetered struct {
	fsys     FileSystem
	recorder Recorder
	mount    string
	kind     string
}

// begin reports the start of one verb on one path and hands back the context
// the call runs on together with the function that ends it.
func (t telemetered) begin(ctx context.Context, verb, path string) (context.Context, Done) {
	return t.recorder.Begin(ctx, Call{Mount: t.mount, Kind: t.kind, Verb: verb, Path: path})
}

func (t telemetered) Create(ctx context.Context, path string) error {
	ctx, done := t.begin(ctx, "Create", path)
	err := t.fsys.Create(ctx, path)
	done(err)
	return err
}

func (t telemetered) Mkdir(ctx context.Context, path string) error {
	ctx, done := t.begin(ctx, "Mkdir", path)
	err := t.fsys.Mkdir(ctx, path)
	done(err)
	return err
}

func (t telemetered) Remove(ctx context.Context, path string) error {
	ctx, done := t.begin(ctx, "Remove", path)
	err := t.fsys.Remove(ctx, path)
	done(err)
	return err
}

func (t telemetered) RemoveAll(ctx context.Context, path string) error {
	ctx, done := t.begin(ctx, "RemoveAll", path)
	err := t.fsys.RemoveAll(ctx, path)
	done(err)
	return err
}

func (t telemetered) Read(ctx context.Context, path string) ([]byte, error) {
	ctx, done := t.begin(ctx, "Read", path)
	data, err := t.fsys.Read(ctx, path)
	done(err)
	return data, err
}

func (t telemetered) Write(ctx context.Context, path string, data []byte) error {
	ctx, done := t.begin(ctx, "Write", path)
	err := t.fsys.Write(ctx, path, data)
	done(err)
	return err
}

func (t telemetered) ReadDir(ctx context.Context, path string) ([]FileInfo, error) {
	ctx, done := t.begin(ctx, "ReadDir", path)
	entries, err := t.fsys.ReadDir(ctx, path)
	done(err)
	return entries, err
}

func (t telemetered) Stat(ctx context.Context, path string) (FileInfo, error) {
	ctx, done := t.begin(ctx, "Stat", path)
	info, err := t.fsys.Stat(ctx, path)
	done(err)
	return info, err
}

func (t telemetered) Rename(ctx context.Context, oldPath, newPath string) error {
	ctx, done := t.begin(ctx, "Rename", oldPath)
	err := t.fsys.Rename(ctx, oldPath, newPath)
	done(err)
	return err
}

func (t telemetered) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	ctx, done := t.begin(ctx, "Chmod", path)
	err := t.fsys.Chmod(ctx, path, mode)
	done(err)
	return err
}

// Capabilities is not I/O and takes no context. There is no call to report.
func (t telemetered) Capabilities(path string) Caps { return t.fsys.Capabilities(path) }

// AsSymlinker returns the plugin's link verbs recorded, or nil if it has none.
// A recorder says nothing about what a plugin can do: what it refused
// unrecorded it refuses recorded, so the caller's nil guard keeps telling the
// truth.
func (t telemetered) AsSymlinker() Symlinker {
	link := t.fsys.AsSymlinker()
	if link == nil {
		return nil
	}
	return telemeteredSymlinker{link: link, telemetered: t}
}

// AsStreamer returns the plugin's streaming verbs with the opens recorded, or
// nil if it has none. What the opens return is not recorded; see WithTelemetry.
func (t telemetered) AsStreamer() Streamer {
	stream := t.fsys.AsStreamer()
	if stream == nil {
		return nil
	}
	return telemeteredStreamer{stream: stream, telemetered: t}
}

// AsTruncater returns the plugin's truncate verb recorded, or nil if it has
// none.
func (t telemetered) AsTruncater() Truncater {
	cut := t.fsys.AsTruncater()
	if cut == nil {
		return nil
	}
	return telemeteredTruncater{cut: cut, telemetered: t}
}

// AsSubber returns the plugin's subtrees reported under the same mount and
// kind, or nil if it has none. Narrowing onto a subtree does not make a second
// mount, so the labels are the ones the mount was given.
func (t telemetered) AsSubber() Subber {
	return SubOf(t.fsys, func(beneath FileSystem) FileSystem {
		return telemetered{fsys: beneath, recorder: t.recorder, mount: t.mount, kind: t.kind}
	})
}

type telemeteredSymlinker struct {
	link Symlinker
	telemetered
}

func (s telemeteredSymlinker) Symlink(ctx context.Context, oldPath, newPath string) error {
	ctx, done := s.begin(ctx, "Symlink", oldPath)
	err := s.link.Symlink(ctx, oldPath, newPath)
	done(err)
	return err
}

func (s telemeteredSymlinker) Readlink(ctx context.Context, path string) (string, error) {
	ctx, done := s.begin(ctx, "Readlink", path)
	target, err := s.link.Readlink(ctx, path)
	done(err)
	return target, err
}

func (s telemeteredSymlinker) Lstat(ctx context.Context, path string) (FileInfo, error) {
	ctx, done := s.begin(ctx, "Lstat", path)
	info, err := s.link.Lstat(ctx, path)
	done(err)
	return info, err
}

type telemeteredTruncater struct {
	cut Truncater
	telemetered
}

func (c telemeteredTruncater) Truncate(ctx context.Context, path string, size int64) error {
	ctx, done := c.begin(ctx, "Truncate", path)
	err := c.cut.Truncate(ctx, path, size)
	done(err)
	return err
}

// telemeteredStreamer records the two opens and leaves the transfers alone.
type telemeteredStreamer struct {
	stream Streamer
	telemetered
}

func (s telemeteredStreamer) OpenRead(ctx context.Context, path string) (io.ReadCloser, error) {
	ctx, done := s.begin(ctx, "OpenRead", path)
	stream, err := s.stream.OpenRead(ctx, path)
	done(err)
	return stream, err
}

func (s telemeteredStreamer) OpenWrite(ctx context.Context, path string) (io.WriteCloser, error) {
	ctx, done := s.begin(ctx, "OpenWrite", path)
	stream, err := s.stream.OpenWrite(ctx, path)
	done(err)
	return stream, err
}

// Recorders reports every call to each of recorders, which is how one process
// both exports a call and counts it without a second wrapper on the way to
// every plugin. The registry holds one recorder, and this is the one it holds
// when there is more than one thing to tell.
//
// The context threads through them in order, so a recorder that opens a span
// hands that span to the recorders after it and to the call itself. Their Done
// functions run in reverse, so what one recorder began inside another ends
// inside it too.
func Recorders(recorders ...Recorder) Recorder { return fanout(recorders) }

// fanout is a recorder over recorders. It is a slice rather than a type with
// two fields because the composition is the same for two as for any number.
type fanout []Recorder

// Begin starts the call on every recorder and returns the context the last of
// them made, which carries whatever the ones before it put there.
func (f fanout) Begin(ctx context.Context, call Call) (context.Context, Done) {
	ends := make([]Done, len(f))
	for i, recorder := range f {
		ctx, ends[i] = recorder.Begin(ctx, call)
	}

	return ctx, func(err error) {
		for i := len(ends) - 1; i >= 0; i-- {
			ends[i](err)
		}
	}
}
