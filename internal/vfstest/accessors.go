package vfstest

import (
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// accessorCases are the optional verb sets. Each is reached through its
// accessor and skipped when the plugin answers nil, so a kind that supports
// nothing optional runs the suite and is held to nothing it refused.
func accessorCases() []testCase {
	return []testCase{
		{name: "Accessors answer the same way twice", run: accessorsAreStable},
		{name: "Truncate shortens content", when: writable, run: truncateShortens},
		{name: "Truncate pads with zero bytes", when: writable, run: truncatePads},
		{name: "Truncate to a negative size is invalid", when: writable, run: truncateNegative},
		{name: "Truncate of a directory is invalid", when: needs(writable, tree), run: truncateOfADirectory},
		{name: "Truncate of a missing path does not exist", when: writable, run: truncateOfAMissingPath},
		{name: "OpenRead streams the content", when: writable, run: openReadStreamsTheContent},
		{name: "OpenRead of a missing path does not exist", run: openReadOfAMissingPath},
		{name: "OpenRead of a directory is invalid", when: needs(writable, tree), run: openReadOfADirectory},
		{name: "OpenWrite stores what was streamed at Close", when: writable, run: openWriteStoresAtClose},
		{name: "OpenWrite under a missing parent does not exist", when: needs(writable, tree), run: openWriteUnderAMissingParent},
		{name: "Closing a write stream twice stores it once", when: needs(writable, replacing), run: closingTwiceStoresOnce},
		{name: "Symlink makes one path stand for another", when: writable, run: symlinkStandsForAnother},
		{name: "Symlink to a target that is not there is allowed", when: writable, run: symlinkToAMissingTarget},
		{name: "Readlink of an entry that is not a link is invalid", when: writable, run: readlinkOfAnEntry},
		{name: "Readlink of a missing path does not exist", run: readlinkOfAMissingPath},
	}
}

// A caller's `if s := fsys.AsStreamer(); s != nil` guard has to mean the same
// thing every time it is written. An accessor that answered a verb set once and
// nil the next would make the guard a coin toss.
func accessorsAreStable(t *testing.T, w world) {
	answers := []struct {
		name  string
		first bool
		again bool
	}{
		{name: "AsSymlinker", first: w.fsys.AsSymlinker() == nil, again: w.fsys.AsSymlinker() == nil},
		{name: "AsStreamer", first: w.fsys.AsStreamer() == nil, again: w.fsys.AsStreamer() == nil},
		{name: "AsTruncater", first: w.fsys.AsTruncater() == nil, again: w.fsys.AsTruncater() == nil},
	}

	for _, answer := range answers {
		if answer.first != answer.again {
			t.Errorf("%s() answered nil and not nil, want one answer", answer.name)
		}
	}
}

// truncater, streamer and symlinker skip a case whose verb set the plugin
// refuses. The skip is what the accessor is for: a refusal is an answer, not a
// gap in coverage.
func truncater(t *testing.T, w world) vfs.Truncater {
	t.Helper()
	cut := w.fsys.AsTruncater()
	if cut == nil {
		t.Skip("the plugin answers no truncate verb")
	}
	return cut
}

func streamer(t *testing.T, w world) vfs.Streamer {
	t.Helper()
	stream := w.fsys.AsStreamer()
	if stream == nil {
		t.Skip("the plugin answers no streaming verbs")
	}
	return stream
}

func symlinker(t *testing.T, w world) vfs.Symlinker {
	t.Helper()
	link := w.fsys.AsSymlinker()
	if link == nil {
		t.Skip("the plugin answers no link verbs")
	}
	return link
}

func truncateShortens(t *testing.T, w world) {
	cut := truncater(t, w)
	mustWrite(t, w, "notes", "abcdef")

	if err := cut.Truncate(t.Context(), w.at("notes"), 3); err != nil {
		t.Fatalf("Truncate(%q, 3) = %v", w.at("notes"), err)
	}

	if content := mustRead(t, w, "notes"); content != "abc" {
		t.Errorf("Read after Truncate = %q, want %q", content, "abc")
	}
}

func truncatePads(t *testing.T, w world) {
	cut := truncater(t, w)
	mustWrite(t, w, "notes", "ab")

	if err := cut.Truncate(t.Context(), w.at("notes"), 4); err != nil {
		t.Fatalf("Truncate(%q, 4) = %v", w.at("notes"), err)
	}

	if content := mustRead(t, w, "notes"); content != "ab\x00\x00" {
		t.Errorf("Read after Truncate = %q, want %q", content, "ab\x00\x00")
	}
}

func truncateNegative(t *testing.T, w world) {
	cut := truncater(t, w)
	mustWrite(t, w, "notes", "abc")

	if err := cut.Truncate(t.Context(), w.at("notes"), -1); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Truncate to a negative size = %v, want fs.ErrInvalid", err)
	}
}

func truncateOfADirectory(t *testing.T, w world) {
	cut := truncater(t, w)
	mustMkdir(t, w, "jobs")

	if err := cut.Truncate(t.Context(), w.at("jobs"), 0); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Truncate of a directory = %v, want fs.ErrInvalid", err)
	}
}

func truncateOfAMissingPath(t *testing.T, w world) {
	cut := truncater(t, w)

	if err := cut.Truncate(t.Context(), w.at("absent"), 0); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Truncate of a missing path = %v, want fs.ErrNotExist", err)
	}
}

func openReadStreamsTheContent(t *testing.T, w world) {
	stream := streamer(t, w)
	mustWrite(t, w, "notes", "streamed")

	reader, err := stream.OpenRead(t.Context(), w.at("notes"))
	if err != nil {
		t.Fatalf("OpenRead(%q) = %v", w.at("notes"), err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("Close(%q) = %v", w.at("notes"), err)
		}
	}()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(%q) = %v", w.at("notes"), err)
	}
	if string(content) != "streamed" {
		t.Errorf("ReadAll = %q, want %q", content, "streamed")
	}
}

func openReadOfAMissingPath(t *testing.T, w world) {
	stream := streamer(t, w)

	if _, err := stream.OpenRead(t.Context(), w.at("absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenRead of a missing path = %v, want fs.ErrNotExist", err)
	}
}

func openReadOfADirectory(t *testing.T, w world) {
	stream := streamer(t, w)
	mustMkdir(t, w, "jobs")

	if _, err := stream.OpenRead(t.Context(), w.at("jobs")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("OpenRead of a directory = %v, want fs.ErrInvalid", err)
	}
}

// The write is not complete until Close returns nil, so what is stored is what
// was streamed, and it is stored once the stream is closed.
func openWriteStoresAtClose(t *testing.T, w world) {
	stream := streamer(t, w)

	writer, err := stream.OpenWrite(t.Context(), w.at("notes"))
	if err != nil {
		t.Fatalf("OpenWrite(%q) = %v", w.at("notes"), err)
	}
	if _, err := io.WriteString(writer, "streamed"); err != nil {
		t.Fatalf("Write to the stream = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(%q) = %v", w.at("notes"), err)
	}

	if content := mustRead(t, w, "notes"); content != "streamed" {
		t.Errorf("Read after Close = %q, want %q", content, "streamed")
	}
}

// A path nothing can be written to is refused at open, so a caller finds out
// before it streams a payload into a buffer nothing will accept.
func openWriteUnderAMissingParent(t *testing.T, w world) {
	stream := streamer(t, w)

	if _, err := stream.OpenWrite(t.Context(), w.at("absent/notes")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenWrite under a missing parent = %v, want fs.ErrNotExist", err)
	}
}

// A deferred Close after an explicit one is the shape every caller writes, so
// the second must not store the buffer again over a later write.
func closingTwiceStoresOnce(t *testing.T, w world) {
	stream := streamer(t, w)

	writer, err := stream.OpenWrite(t.Context(), w.at("notes"))
	if err != nil {
		t.Fatalf("OpenWrite(%q) = %v", w.at("notes"), err)
	}
	if _, err := io.WriteString(writer, "streamed"); err != nil {
		t.Fatalf("Write to the stream = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(%q) = %v", w.at("notes"), err)
	}
	mustWrite(t, w, "notes", "later")

	if err := writer.Close(); err != nil {
		t.Errorf("Close twice = %v, want nil", err)
	}
	if content := mustRead(t, w, "notes"); content != "later" {
		t.Errorf("Read after closing twice = %q, want %q", content, "later")
	}
}

func symlinkStandsForAnother(t *testing.T, w world) {
	link := symlinker(t, w)
	mustWrite(t, w, "notes", "text")

	if err := link.Symlink(t.Context(), w.at("notes"), w.at("shortcut")); err != nil {
		t.Fatalf("Symlink(%q, %q) = %v", w.at("notes"), w.at("shortcut"), err)
	}

	target, err := link.Readlink(t.Context(), w.at("shortcut"))
	if err != nil {
		t.Fatalf("Readlink(%q) = %v", w.at("shortcut"), err)
	}
	if target != w.at("notes") {
		t.Errorf("Readlink(%q) = %q, want %q", w.at("shortcut"), target, w.at("notes"))
	}
}

// A link stands for a path rather than for what is at it, so the target need
// not exist when the link is made.
func symlinkToAMissingTarget(t *testing.T, w world) {
	link := symlinker(t, w)

	if err := link.Symlink(t.Context(), w.at("absent"), w.at("shortcut")); err != nil {
		t.Fatalf("Symlink to a missing target = %v, want nil", err)
	}

	target, err := link.Readlink(t.Context(), w.at("shortcut"))
	if err != nil {
		t.Fatalf("Readlink(%q) = %v", w.at("shortcut"), err)
	}
	if target != w.at("absent") {
		t.Errorf("Readlink(%q) = %q, want %q", w.at("shortcut"), target, w.at("absent"))
	}
}

func readlinkOfAnEntry(t *testing.T, w world) {
	link := symlinker(t, w)
	mustWrite(t, w, "notes", "text")

	if _, err := link.Readlink(t.Context(), w.at("notes")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Readlink of an entry that is not a link = %v, want fs.ErrInvalid", err)
	}
}

func readlinkOfAMissingPath(t *testing.T, w world) {
	link := symlinker(t, w)

	if _, err := link.Readlink(t.Context(), w.at("absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Readlink of a missing path = %v, want fs.ErrNotExist", err)
	}
}
