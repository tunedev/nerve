package streamfs_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/streamfs"
)

// waitFor is how long a case waits for something another goroutine is expected
// to do straight away. It is long enough that a loaded machine does not fail
// the case, and short enough that a reader which never wakes fails it rather
// than hanging the suite.
const waitFor = 2 * time.Second

// The ticket in one case: a reader that is already following the stream
// receives what is appended to it, without reading the stream again.
func TestAReaderReceivesAnAppendMadeAfterItOpened(t *testing.T) {
	fsys := streamfs.New(streamfs.DefaultCapacity, waitFor)
	mustWrite(t, fsys, "/log", "")

	reader := mustFollow(t, fsys, "/log")
	arrived := readInBackground(t, reader, len("appended"))

	mustWrite(t, fsys, "/log", "appended")

	if received := waitForRead(t, arrived); received != "appended" {
		t.Errorf("the follower received %q, want %q", received, "appended")
	}
}

// Broadcast is what the mount claims, and this is what the claim means: two
// readers each receive every append rather than taking it from each other.
func TestEveryReaderReceivesEveryAppend(t *testing.T) {
	fsys := streamfs.New(streamfs.DefaultCapacity, waitFor)
	mustWrite(t, fsys, "/log", "")

	first, second := mustFollow(t, fsys, "/log"), mustFollow(t, fsys, "/log")
	arrivedFirst := readInBackground(t, first, len("appended"))
	arrivedSecond := readInBackground(t, second, len("appended"))

	mustWrite(t, fsys, "/log", "appended")

	if received := waitForRead(t, arrivedFirst); received != "appended" {
		t.Errorf("the first follower received %q, want %q", received, "appended")
	}
	if received := waitForRead(t, arrivedSecond); received != "appended" {
		t.Errorf("the second follower received %q, want %q", received, "appended")
	}
}

// A reader waiting for the next append is waiting on the caller's deadline as
// much as on the stream: the context reaches through the follow.
func TestAReaderEndsWhenTheCallerIsDone(t *testing.T) {
	fsys := streamfs.New(streamfs.DefaultCapacity, time.Hour)
	mustWrite(t, fsys, "/log", "written")

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	reader := mustFollowWith(t, ctx, fsys, "/log")

	content, err := io.ReadAll(reader)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ReadAll(/log) = %v, want context.DeadlineExceeded", err)
	}
	if string(content) != "written" {
		t.Errorf("ReadAll(/log) = %q, want %q", content, "written")
	}
}

// The wait is bounded by the mount as well as by the caller, so a reader of a
// stream nobody is writing to ends instead of holding a connection open until
// something else times out.
func TestAReaderEndsWhenNothingIsAppended(t *testing.T) {
	fsys := streamfs.New(streamfs.DefaultCapacity, 20*time.Millisecond)
	mustWrite(t, fsys, "/log", "written")

	reader := mustFollow(t, fsys, "/log")

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(/log) = %v", err)
	}
	if string(content) != "written" {
		t.Errorf("ReadAll(/log) = %q, want %q", content, "written")
	}
}

// The ring is bounded, so a write past the capacity drops the oldest bytes
// rather than growing.
func TestWritePastTheCapacityKeepsTheNewestBytes(t *testing.T) {
	fsys := streamfs.New(8, 0)
	mustWrite(t, fsys, "/log", "abcdef")
	mustWrite(t, fsys, "/log", "ghij")

	if content := mustRead(t, fsys, "/log"); content != "cdefghij" {
		t.Errorf("Read(/log) = %q, want %q", content, "cdefghij")
	}
}

// A reader the ring overtook is told so. Handing it what is left would report a
// gap in a log as an absence in one.
func TestAReaderTheRingOvertookFails(t *testing.T) {
	fsys := streamfs.New(8, waitFor)
	mustWrite(t, fsys, "/log", "abcdefgh")

	reader := mustFollow(t, fsys, "/log")
	mustWrite(t, fsys, "/log", "12345678")

	if _, err := io.ReadAll(reader); !errors.Is(err, streamfs.ErrOverrun) {
		t.Errorf("ReadAll after the ring wrapped past the reader = %v, want streamfs.ErrOverrun", err)
	}
}

// A streamed write adds to the end for the reason a whole one does: the mount
// is append-only, and OpenWrite is the same verb in pieces.
func TestAStreamedWriteAppends(t *testing.T) {
	fsys := streamfs.New(streamfs.DefaultCapacity, 0)
	mustWrite(t, fsys, "/log", "first ")

	writer, err := fsys.AsStreamer().OpenWrite(t.Context(), "/log")
	if err != nil {
		t.Fatalf("OpenWrite(/log) = %v", err)
	}
	if _, err := io.WriteString(writer, "second"); err != nil {
		t.Fatalf("Write to the stream = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(/log) = %v", err)
	}

	if content := mustRead(t, fsys, "/log"); content != "first second" {
		t.Errorf("Read(/log) = %q, want %q", content, "first second")
	}
}

// What is streamed is appended as it arrives rather than at Close, because a
// reader following the stream is what the mount is for.
func TestAStreamedWriteIsReceivedBeforeItIsClosed(t *testing.T) {
	fsys := streamfs.New(streamfs.DefaultCapacity, waitFor)
	mustWrite(t, fsys, "/log", "")

	reader := mustFollow(t, fsys, "/log")
	arrived := readInBackground(t, reader, len("streamed"))

	writer, err := fsys.AsStreamer().OpenWrite(t.Context(), "/log")
	if err != nil {
		t.Fatalf("OpenWrite(/log) = %v", err)
	}
	defer writer.Close()
	if _, err := io.WriteString(writer, "streamed"); err != nil {
		t.Fatalf("Write to the stream = %v", err)
	}

	if received := waitForRead(t, arrived); received != "streamed" {
		t.Errorf("the follower received %q before Close, want %q", received, "streamed")
	}
}

// Create is not a truncation here. The mount is append-only, so a stream that
// is there keeps what has been written to it.
func TestCreateLeavesAStreamThatIsThere(t *testing.T) {
	fsys := streamfs.New(streamfs.DefaultCapacity, 0)
	mustWrite(t, fsys, "/log", "kept")

	if err := fsys.Create(t.Context(), "/log"); err != nil {
		t.Fatalf("Create(/log) = %v", err)
	}

	if content := mustRead(t, fsys, "/log"); content != "kept" {
		t.Errorf("Read(/log) after Create = %q, want %q", content, "kept")
	}
}

func mustWrite(t *testing.T, fsys *streamfs.FS, name, content string) {
	t.Helper()
	if err := fsys.Write(t.Context(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
}

func mustRead(t *testing.T, fsys *streamfs.FS, name string) string {
	t.Helper()
	content, err := fsys.Read(t.Context(), name)
	if err != nil {
		t.Fatalf("Read(%q) = %v", name, err)
	}
	return string(content)
}

func mustFollow(t *testing.T, fsys *streamfs.FS, name string) io.ReadCloser {
	t.Helper()
	return mustFollowWith(t, t.Context(), fsys, name)
}

func mustFollowWith(t *testing.T, ctx context.Context, fsys *streamfs.FS, name string) io.ReadCloser {
	t.Helper()
	reader, err := fsys.AsStreamer().OpenRead(ctx, name)
	if err != nil {
		t.Fatalf("OpenRead(%q) = %v", name, err)
	}
	t.Cleanup(func() { reader.Close() })
	return reader
}

// readInBackground has a follower read count bytes while the case goes on to
// append them. The read is the assertion: it is waiting when the append is
// made, which is what makes it an observation rather than a second read.
func readInBackground(t *testing.T, reader io.Reader, count int) <-chan string {
	t.Helper()
	received := make(chan string, 1)
	go func() {
		buffer := make([]byte, count)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			received <- "read failed: " + err.Error()
			return
		}
		received <- string(buffer)
	}()
	return received
}

func waitForRead(t *testing.T, received <-chan string) string {
	t.Helper()
	select {
	case content := <-received:
		return content
	case <-time.After(waitFor):
		t.Fatal("the follower did not receive the append")
		return ""
	}
}
