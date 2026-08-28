package vfstest

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"testing"
)

// collisionRounds is how many times the key-versus-prefix listing is asked. A
// listing folded through a map is right by luck about nine times in ten, so one
// round proves nothing and enough of them leave the wrong rule nowhere to hide.
const collisionRounds = 20

// descriptorCases hold a plugin to what it claims, in both directions. Most
// exist because a descriptor is set, and assert the meaning of that descriptor
// rather than the plain read-write tree the cases elsewhere assume. Two exist
// because a descriptor is not set, and hold a plugin to the silence.
func descriptorCases() []testCase {
	return []testCase{
		{name: "Capabilities answers the same thing twice", run: capabilitiesAreStable},
		{name: "A mount that did not claim read-destructive survives a second read", when: needs(writable, repeatable), run: undeclaredReadDestructive},
		{name: "A mount that did not claim append-only replaces rather than adds", when: needs(writable, replacing), run: undeclaredAppendOnly},
		{name: "Read-only refuses every mutating verb", when: readOnly, run: readOnlyRefusesMutation},
		{name: "Append-only adds to the end", when: needs(appendOnly, writable, repeatable), run: appendOnlyAddsToTheEnd},
		{name: "Read-destructive consumes what it hands back", when: needs(readDestructive, writable, competing), run: readDestructiveConsumes},
		{name: "Object store has nothing for Mkdir to make", when: needs(objectStore, writable), run: objectStoreMakesNoDirectory},
		{name: "Object store names are prefixes", when: needs(objectStore, writable), run: objectStoreNamesArePrefixes},
		{name: "Object store lists what is under a prefix", when: needs(objectStore, writable), run: objectStoreListsAPrefix},
		{name: "Object store lists a key over a prefix of the same name", when: needs(objectStore, writable, repeatable), run: objectStoreResolvesACollision},
		{name: "Object store removes a key with a prefix of the same name under it", when: needs(objectStore, writable), run: objectStoreRemovesACollision},
	}
}

// Capabilities is not I/O: it answers from the shape a mount already knows, so
// asking twice cannot give two answers, and a path that is not there is still a
// path the mount can describe.
func capabilitiesAreStable(t *testing.T, w world) {
	for _, name := range []string{w.root, w.at("notes"), w.at("absent/deeper")} {
		first, second := w.fsys.Capabilities(name), w.fsys.Capabilities(name)
		if first != second {
			t.Errorf("Capabilities(%q) = %+v then %+v, want one answer", name, first, second)
		}
	}
}

// Every mutating verb fails with fs.ErrPermission, including the ones reached
// through an accessor: a read-only mount that answers a streamer must refuse
// through it too, or the refusal is one call away from being bypassed.
func readOnlyRefusesMutation(t *testing.T, w world) {
	refusals := map[string]error{
		"Create":    w.fsys.Create(t.Context(), w.at("notes")),
		"Mkdir":     w.fsys.Mkdir(t.Context(), w.at("jobs")),
		"Write":     w.fsys.Write(t.Context(), w.at("notes"), []byte("x")),
		"Remove":    w.fsys.Remove(t.Context(), w.at("notes")),
		"RemoveAll": w.fsys.RemoveAll(t.Context(), w.at("notes")),
		"Rename":    w.fsys.Rename(t.Context(), w.at("notes"), w.at("elsewhere")),
		"Chmod":     w.fsys.Chmod(t.Context(), w.at("notes"), 0o600),
	}
	if cut := w.fsys.AsTruncater(); cut != nil {
		refusals["Truncate"] = cut.Truncate(t.Context(), w.at("notes"), 0)
	}
	if stream := w.fsys.AsStreamer(); stream != nil {
		_, err := stream.OpenWrite(t.Context(), w.at("notes"))
		refusals["OpenWrite"] = err
	}

	for _, verb := range slices.Sorted(maps.Keys(refusals)) {
		if !errors.Is(refusals[verb], fs.ErrPermission) {
			t.Errorf("%s on a read-only mount = %v, want fs.ErrPermission", verb, refusals[verb])
		}
	}
}

// The two cases below are the only place the suite asks whether a claim is
// true rather than what it means, and they are load-bearing rather than tidy.
// Under-declaring is the easy way to pass: claim nothing and every case takes
// the ordinary branch. WithRetry then reads ReadDestructive and AppendOnly to
// decide whether a failed Read or Write may be made again, so a mount that
// consumes or accumulates while claiming neither gets its calls repeated —
// an entry lost or bytes stored twice, from a green run.
//
// These are the two descriptors worth policing because they are the two the
// retry layer acts on. The rest of a plugin's claims cannot be checked from
// outside the port and are not attempted here.

// A second read answers the same bytes. A mount that consumes what it hands
// back has to claim ReadDestructive, and one that does is not asked this.
func undeclaredReadDestructive(t *testing.T, w world) {
	mustWrite(t, w, "notes", "stored")

	if first := mustRead(t, w, "notes"); first != "stored" {
		t.Fatalf("the first Read = %q, want %q", first, "stored")
	}

	// The second read is asked directly rather than through mustRead: a mount
	// that consumed the entry answers with an error, and that is the failure
	// this case exists to name.
	second, err := w.fsys.Read(t.Context(), w.at("notes"))
	if err != nil || string(second) != "stored" {
		t.Errorf("the second Read = %q, %v, want %q: a mount that consumes what it reads must claim ReadDestructive", second, err, "stored")
	}
}

// A second write stands in place of the first rather than after it. Both writes
// are the same length, so what fails here is accumulation and nothing else: a
// mount that adds has to claim AppendOnly, and one that does is not asked this.
func undeclaredAppendOnly(t *testing.T, w world) {
	mustWrite(t, w, "notes", "first")
	mustWrite(t, w, "notes", "later")

	if content := mustRead(t, w, "notes"); content != "later" {
		t.Errorf("Read after a second Write = %q, want %q: a mount that adds rather than replaces must claim AppendOnly", content, "later")
	}
}

// What is already there is never replaced or shortened: a second write is added
// to the end of the first.
func appendOnlyAddsToTheEnd(t *testing.T, w world) {
	mustWrite(t, w, "log", "first ")
	mustWrite(t, w, "log", "second")

	if content := mustRead(t, w, "log"); content != "first second" {
		t.Errorf("Read after two writes = %q, want %q", content, "first second")
	}
}

// What one reader receives is gone for the next. Whether the second read finds
// nothing or fails is the plugin's to decide; that it does not hand back the
// same content again is not.
func readDestructiveConsumes(t *testing.T, w world) {
	mustWrite(t, w, "job", "work")

	if first := mustRead(t, w, "job"); first != "work" {
		t.Fatalf("the first Read = %q, want %q", first, "work")
	}

	second, err := w.fsys.Read(t.Context(), w.at("job"))
	if err == nil && string(second) == "work" {
		t.Errorf("the second Read = %q, want it consumed by the first", second)
	}
}

// No directory exists apart from the entries under it, so Mkdir has nothing to
// make: it does not fail, and it does not leave an entry behind.
func objectStoreMakesNoDirectory(t *testing.T, w world) {
	if err := w.fsys.Mkdir(t.Context(), w.at("jobs")); err != nil {
		t.Fatalf("Mkdir on an object store = %v, want nil", err)
	}

	if _, err := w.fsys.Stat(t.Context(), w.at("jobs")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of a prefix with nothing under it = %v, want fs.ErrNotExist", err)
	}
}

// A name is a prefix, not a path through directories, so nothing has to be made
// before something is written under one.
func objectStoreNamesArePrefixes(t *testing.T, w world) {
	mustWrite(t, w, "jobs/queued/first", "work")

	if content := mustRead(t, w, "jobs/queued/first"); content != "work" {
		t.Errorf("Read under a prefix that was never made = %q, want %q", content, "work")
	}
}

func objectStoreListsAPrefix(t *testing.T, w world) {
	mustWrite(t, w, "jobs/audit", "log")
	mustWrite(t, w, "jobs/config", "settings")

	entries, err := w.fsys.ReadDir(t.Context(), w.at("jobs"))
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v", w.at("jobs"), err)
	}

	want := []string{"audit", "config"}
	if listed := names(entries); !slices.Equal(listed, want) {
		t.Errorf("ReadDir(%q) = %v, want %v", w.at("jobs"), listed, want)
	}
}

// A key and a prefix can share a name, which no tree allows: both are stored,
// both are read back, and a listing has to say which of the two the shared name
// is. The key wins, because a key describes itself where a prefix is only ever
// described by what sits beneath it.
//
// The rounds are the assertion. A listing folded through a map answers from
// whichever key it saw last, so a wrong rule still agrees with the right one
// most runs, and one listing is not enough to tell them apart.
//
// Both are read back and then listed again, so the case needs a mount that
// answers the same read twice. A read-destructive one has nothing left to list
// by the time the rounds start, and the rule it would be asked about is one it
// never claimed.
//
// A store that hides the deeper key while the shorter one is there satisfies
// this by a different route: it answers with the key because it never offers
// the prefix, so the fold rule is never reached. The case is still right about
// what a listing has to say; it is not evidence that a particular store's
// listing was ever asked to choose.
func objectStoreResolvesACollision(t *testing.T, w world) {
	mustWrite(t, w, "sessions/abc", "the session")
	mustWrite(t, w, "sessions/abc/state", "running")

	if content := mustRead(t, w, "sessions/abc"); content != "the session" {
		t.Errorf("Read of the key beside a prefix of its name = %q, want %q", content, "the session")
	}
	if content := mustRead(t, w, "sessions/abc/state"); content != "running" {
		t.Errorf("Read under the prefix beside a key of its name = %q, want %q", content, "running")
	}

	for round := range collisionRounds {
		entries, err := w.fsys.ReadDir(t.Context(), w.at("sessions"))
		if err != nil {
			t.Fatalf("ReadDir(%q) = %v", w.at("sessions"), err)
		}
		if listed := names(entries); !slices.Equal(listed, []string{"abc"}) {
			t.Fatalf("ReadDir(%q) on round %d = %v, want %v", w.at("sessions"), round, listed, []string{"abc"})
		}
		if entries[0].IsDir() || entries[0].Size != int64(len("the session")) {
			t.Fatalf("ReadDir(%q) on round %d = %+v, want the key rather than the prefix", w.at("sessions"), round, entries[0])
		}
	}
}

// RemoveAll of the shared name takes both the key and everything under the
// prefix. A store is allowed to hide the deeper key from a listing while the
// shorter one is there, so a RemoveAll that deletes one listing's worth leaves
// data in the bucket; what the verb promises is that nothing under the path is
// left, however many listings that takes.
//
// Stat is the question, not a listing: the listing is the thing that lies here,
// and a key it declines to offer is still a key the store answers for.
func objectStoreRemovesACollision(t *testing.T, w world) {
	mustWrite(t, w, "sessions/abc", "the session")
	mustWrite(t, w, "sessions/abc/state", "running")

	if err := w.fsys.RemoveAll(t.Context(), w.at("sessions/abc")); err != nil {
		t.Fatalf("RemoveAll(%q) = %v", w.at("sessions/abc"), err)
	}

	for _, name := range []string{"sessions/abc", "sessions/abc/state"} {
		if _, err := w.fsys.Stat(t.Context(), w.at(name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) after RemoveAll of the shared name = %v, want fs.ErrNotExist", w.at(name), err)
		}
	}
}
