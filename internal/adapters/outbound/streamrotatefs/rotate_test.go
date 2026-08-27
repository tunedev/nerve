package streamrotatefs_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/streamrotatefs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// segment is how much of a log this package's tests let accumulate before it is
// sealed. It is small so a case can rotate a mount in a line rather than in a
// megabyte.
const segment = 4

// What the acceptance of this kind is: a caller finds out what a log rotated by
// listing the prefix it is under. Each sealed segment appears there as it is
// made, so the history of a log is entries rather than something the mount has
// to be asked about.
func TestRotationIsObservableThroughReadDir(t *testing.T) {
	fsys := streamrotatefs.New(segment, 2)
	ctx := t.Context()

	mustWrite(t, fsys, "/app", "aaaa")
	if listed := mustList(t, fsys, "/"); !slices.Equal(listed, []string{"app", "app.1"}) {
		t.Errorf("ReadDir after one rotation = %v, want [app app.1]", listed)
	}

	mustWrite(t, fsys, "/app", "bbbb")
	if listed := mustList(t, fsys, "/"); !slices.Equal(listed, []string{"app", "app.1", "app.2"}) {
		t.Errorf("ReadDir after two rotations = %v, want [app app.1 app.2]", listed)
	}

	if content := mustRead(t, fsys, "/app.1"); content != "aaaa" {
		t.Errorf("Read of the first segment = %q, want %q", content, "aaaa")
	}
	if content := mustRead(t, fsys, "/app"); content != "" {
		t.Errorf("Read of the log it rotated out of = %q, want it empty", content)
	}
	if _, err := fsys.Stat(ctx, "/app.1"); err != nil {
		t.Errorf("Stat of a sealed segment = %v, want it described", err)
	}
}

// Retention is the other half of the same listing: a segment leaves it when the
// log has kept as many as the mount allows, so what a mount discarded is what a
// caller can see it discarded.
func TestRetentionDropsTheOldestSegmentFromTheListing(t *testing.T) {
	fsys := streamrotatefs.New(segment, 2)

	for _, written := range []string{"aaaa", "bbbb", "cccc"} {
		mustWrite(t, fsys, "/app", written)
	}

	if listed := mustList(t, fsys, "/"); !slices.Equal(listed, []string{"app", "app.2", "app.3"}) {
		t.Errorf("ReadDir with two kept of three rotations = %v, want [app app.2 app.3]", listed)
	}
	if _, err := fsys.Read(t.Context(), "/app.1"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read of the segment retention dropped = %v, want fs.ErrNotExist", err)
	}
	if content := mustRead(t, fsys, "/app.3"); content != "cccc" {
		t.Errorf("Read of the newest segment = %q, want %q", content, "cccc")
	}
}

// A mount that keeps nothing still rotates. It holds one segment and no
// history, which is what bounds a log to the size of a segment.
func TestKeepingNothingLeavesOnlyTheLog(t *testing.T) {
	fsys := streamrotatefs.New(segment, 0)

	mustWrite(t, fsys, "/app", "aaaa")

	if listed := mustList(t, fsys, "/"); !slices.Equal(listed, []string{"app"}) {
		t.Errorf("ReadDir of a mount that keeps nothing = %v, want [app]", listed)
	}
}

// A segment is cut between writes and never inside one, so a record that
// arrived in one call is read back from one segment. A write larger than the
// segment size is a segment larger than the size rather than a record in two
// pieces.
func TestAWriteIsNeverSplitAcrossSegments(t *testing.T) {
	fsys := streamrotatefs.New(segment, 2)

	mustWrite(t, fsys, "/app", "a whole line")

	if content := mustRead(t, fsys, "/app.1"); content != "a whole line" {
		t.Errorf("Read of the segment a long write sealed = %q, want it whole", content)
	}
}

// What is written is added to the end of the log until a segment fills, and the
// bytes that filled it are in that segment rather than in the next one.
func TestAppendsFillOneSegmentBeforeTheNext(t *testing.T) {
	fsys := streamrotatefs.New(segment, 2)

	for _, written := range []string{"a", "b", "c", "d", "e"} {
		mustWrite(t, fsys, "/app", written)
	}

	if content := mustRead(t, fsys, "/app.1"); content != "abcd" {
		t.Errorf("Read of the filled segment = %q, want %q", content, "abcd")
	}
	if content := mustRead(t, fsys, "/app"); content != "e" {
		t.Errorf("Read of the log after it rotated = %q, want %q", content, "e")
	}
}

// A mount holds a bounded amount whatever is written to it, which is what makes
// one safe to leave running: a log occupies the segments it is allowed and no
// more.
func TestAMountStaysBounded(t *testing.T) {
	fsys := streamrotatefs.New(segment, 2)

	for range 200 {
		mustWrite(t, fsys, "/app", "aaaa")
	}

	listed := mustList(t, fsys, "/")
	if len(listed) != 3 {
		t.Errorf("ReadDir after 200 rotations = %v, want the log and two segments", listed)
	}
	for _, entry := range listed {
		if content := mustRead(t, fsys, "/"+entry); len(content) > segment {
			t.Errorf("Read(%q) = %d bytes, want no more than the segment size %d", entry, len(content), segment)
		}
	}
}

// Every name in the mount is a rotating log, and a name that looks like a
// segment is one too. A caller that wrote to it keeps what it wrote: the log
// beside it passes over the number rather than replacing what is there, which
// is what an append-only mount owes a name it did not write.
func TestSealingPassesOverANameACallerTook(t *testing.T) {
	fsys := streamrotatefs.New(segment, 2)

	mustWrite(t, fsys, "/app.1", "mi")
	mustWrite(t, fsys, "/app", "aaaa")

	if content := mustRead(t, fsys, "/app.1"); content != "mi" {
		t.Errorf("Read of the name a caller took = %q, want %q", content, "mi")
	}
	if content := mustRead(t, fsys, "/app.2"); content != "aaaa" {
		t.Errorf("Read of the segment sealed past it = %q, want %q", content, "aaaa")
	}
}

// Logs are separate. One rotating says nothing about another, and the numbers
// each seals under are its own.
func TestLogsRotateSeparately(t *testing.T) {
	fsys := streamrotatefs.New(segment, 2)

	mustWrite(t, fsys, "/services/api", "aaaa")
	mustWrite(t, fsys, "/services/worker", "b")

	if listed := mustList(t, fsys, "/services"); !slices.Equal(listed, []string{"api", "api.1", "worker"}) {
		t.Errorf("ReadDir(/services) = %v, want [api api.1 worker]", listed)
	}
}

// Rotation is a write and writes race, so what a mount holds after concurrent
// writers is what they wrote: every byte lands in some segment, and the mount
// is still bounded.
func TestConcurrentWritersRotateOneLogSafely(t *testing.T) {
	fsys := streamrotatefs.New(segment, 100)
	ctx := context.Background()

	const writers, each = 8, 25
	done := make(chan struct{})
	for range writers {
		go func() {
			defer func() { done <- struct{}{} }()
			for range each {
				if err := fsys.Write(ctx, "/app", []byte("ab")); err != nil {
					t.Errorf("Write(/app) = %v", err)
				}
			}
		}()
	}
	for range writers {
		<-done
	}

	held := 0
	for _, entry := range mustList(t, fsys, "/") {
		held += len(mustRead(t, fsys, "/"+entry))
	}
	if want := writers * each * 2; held != want {
		t.Errorf("the mount holds %d bytes, want the %d that were written", held, want)
	}
}

// The kind claims an append-only keyspace at every path, sealed segments
// included. There is no second shape here and no name that means one.
func TestCapabilitiesAreTheSameForALogAndItsSegments(t *testing.T) {
	fsys := streamrotatefs.New(segment, 2)
	mustWrite(t, fsys, "/app", "aaaa")

	want := vfs.Caps{AppendOnly: true, ObjectStore: true}
	for _, name := range []string{"/", "/app", "/app.1", "/absent"} {
		if claimed := fsys.Capabilities(name); claimed != want {
			t.Errorf("Capabilities(%q) = %+v, want %+v", name, claimed, want)
		}
	}
}

func mustWrite(t *testing.T, fsys vfs.FileSystem, name, content string) {
	t.Helper()
	if err := fsys.Write(t.Context(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
}

func mustRead(t *testing.T, fsys vfs.FileSystem, name string) string {
	t.Helper()
	content, err := fsys.Read(t.Context(), name)
	if err != nil {
		t.Fatalf("Read(%q) = %v", name, err)
	}
	return string(content)
}

// mustList is what ReadDir named, which is what a rotation assertion compares.
func mustList(t *testing.T, fsys vfs.FileSystem, name string) []string {
	t.Helper()
	entries, err := fsys.ReadDir(t.Context(), name)
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v", name, err)
	}
	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	return listed
}
