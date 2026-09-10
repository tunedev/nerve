package workspace_test

import (
	"context"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/kv"
	"github.com/tunedev/nerve/internal/core/library"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/relation"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/schedule"
	"github.com/tunedev/nerve/internal/core/session"
	"github.com/tunedev/nerve/internal/core/thread"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Every store that keeps one workspace's state stands on Scope, and the
// boundary is asserted for all of them here rather than nine times in nine
// packages: it is one property, and nine copies of it drift.
//
// The world each case runs against is host-backed, which is what makes the
// question askable. A path derived down to a scope and handed to the area is
// resolved by the area, and a host disk resolves a link inside the syscall long
// after any name was derived — so a link standing at a component of the scope
// path is followed there and refused by a view, and only a real disk tells the
// two apart.

// store is one of the scoped stores, reduced to what an isolation case needs of
// it: where its mount is, something written into a scope through its own verbs,
// and everything read back out of one.
type store struct {
	root string
	// put writes body into s. It makes bob's world, and it is alice's attempt
	// to write into it.
	put func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, body string) error
	// get is everything the store reports for s, joined. A store with no
	// scoped read has none, and only the write and delete halves run.
	get func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) (string, error)
	// drop is the destructive verb, which is the loudest way to be observable
	// from another scope.
	drop func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) error
}

// held is the name every case writes under, in whichever vocabulary the store
// has for one.
const held = "cv"

// scopedStores is every type that derives where it stands from a Scope.
func scopedStores(t *testing.T) map[string]store {
	t.Helper()
	return map[string]store{
		"session":  sessionStore(t),
		"thread":   threadLog(t),
		"memory":   memoryStore(t),
		"library":  libraryRegistry(t),
		"resource": resourceStore(t),
		"relation": relationGraph(t),
		"kv":       kvSpace(t),
		"ingest":   ingestLedger(t),
		"schedule": scheduleLog(t),
	}
}

// A read through a scoped store answers with what its own scope holds, which is
// nothing, rather than with what the link on the scope path stands for.
func TestNoScopedStoreReadsAnotherScopeThroughALinkOnTheScopePath(t *testing.T) {
	for name, kind := range scopedStores(t) {
		if kind.get == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			table := worldOf(t, kind)
			linkTheScopePath(t, table, kind.root)

			got, _ := kind.get(t.Context(), table, alice)
			if strings.Contains(got, bobsSecret) {
				t.Errorf("reading alice's scope returned %q, want nothing of bob's", got)
			}
		})
	}
}

// A write through a scoped store lands in its own scope or nowhere, and never
// in the one the link stands for.
func TestNoScopedStoreWritesIntoAnotherScopeThroughALinkOnTheScopePath(t *testing.T) {
	for name, kind := range scopedStores(t) {
		t.Run(name, func(t *testing.T) {
			table := worldOf(t, kind)
			was := filesUnder(t, table, userRootIn(t, bob, kind.root))
			linkTheScopePath(t, table, kind.root)

			// The error is not asserted: what a verb answers is the area
			// speaking, and what is asserted is that bob's world is the same
			// afterwards.
			_ = kind.put(t.Context(), table, alice, "alice was here")

			requireUnchanged(t, table, kind.root, was, "a write into a scope whose user directory is a link")
		})
	}
}

// Deleting through a scoped store takes nothing of another scope's with it.
func TestNoScopedStoreDeletesFromAnotherScopeThroughALinkOnTheScopePath(t *testing.T) {
	for name, kind := range scopedStores(t) {
		if kind.drop == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			table := worldOf(t, kind)
			was := filesUnder(t, table, userRootIn(t, bob, kind.root))
			linkTheScopePath(t, table, kind.root)

			_ = kind.drop(t.Context(), table, alice)

			requireUnchanged(t, table, kind.root, was, "a delete in a scope whose user directory is a link")
		})
	}
}

// worldOf is the tree a case runs against: the store's area, host-backed, with
// bob's secret written into his workspace through the store's own verbs and
// alice holding a workspace of her own.
func worldOf(t *testing.T, kind store) vfs.FileSystem {
	t.Helper()

	table := vfs.NewMountTable()
	mustMount(t, table, kind.root, mustLocal(t))
	life, err := workspace.NewLifecycle(table, kind.root)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	for _, s := range []workspace.Scope{alice, bob} {
		mustCreate(t, life, s)
	}
	if err := kind.put(t.Context(), table, bob, bobsSecret); err != nil {
		t.Fatalf("making bob's world: %v", err)
	}
	return table
}

// linkTheScopePath puts a link to bob's user directory where alice's stood,
// which is what a host writer with access to the mount can do to any directory
// in it, and proves the link is live before a case stands on it.
//
// The proof is the positive control: what the table reads under alice's user
// root is bob's world, so a store that answers with nothing below is the store
// refusing rather than a world with nothing in it.
func linkTheScopePath(t *testing.T, table vfs.FileSystem, root string) {
	t.Helper()
	replaceWithLink(t, table, userRootIn(t, alice, root), userRootIn(t, bob, root))

	through := filesUnder(t, table, userRootIn(t, alice, root))
	bobs := filesUnder(t, table, userRootIn(t, bob, root))
	if len(bobs) == 0 {
		t.Fatalf("bob's world under %q is empty, want the store to have written something into it", root)
	}
	if !maps.Equal(through, bobs) {
		t.Fatalf("the table reads %v under alice's user root, want bob's world %v — the link is not live", through, bobs)
	}
}

// requireUnchanged is the assertion every case ends on: everything bob holds,
// as it stood before alice acted.
func requireUnchanged(t *testing.T, table vfs.FileSystem, root string, was map[string]string, after string) {
	t.Helper()
	if now := filesUnder(t, table, userRootIn(t, bob, root)); !maps.Equal(now, was) {
		t.Errorf("after %s, bob's world is %v, want %v", after, now, was)
	}
}

// filesUnder is every file beneath name, with what it holds, read through the
// table. It is how a case says "bob's world" without knowing how the store it
// is running against lays one out.
//
// The keys are relative to name, so two users' subtrees compare equal when they
// hold the same thing — which is what the positive control asks of the link.
func filesUnder(t *testing.T, fsys vfs.FileSystem, name string) map[string]string {
	t.Helper()
	found := map[string]string{}
	collect(t, fsys, name, "", found)
	return found
}

func collect(t *testing.T, fsys vfs.FileSystem, root, prefix string, found map[string]string) {
	t.Helper()
	entries, err := fsys.ReadDir(t.Context(), path.Join(root, prefix))
	if err != nil {
		return
	}
	for _, entry := range entries {
		rel := path.Join(prefix, entry.Name)
		if entry.IsDir() {
			collect(t, fsys, root, rel, found)
			continue
		}
		content, err := fsys.Read(t.Context(), path.Join(root, rel))
		if err != nil {
			t.Fatalf("Read(%q): %v", path.Join(root, rel), err)
		}
		found[rel] = string(content)
	}
}

// userRootIn is the subtree holding every workspace a user has under root,
// which is the component of a scope path standing above the workspace
// directory.
func userRootIn(t *testing.T, s workspace.Scope, root string) string {
	t.Helper()
	return mustPath(t, workspace.Scope{User: s.User}, root, "")
}

func sessionStore(t *testing.T) store {
	t.Helper()
	const root = "/sessions"
	open := func(fsys vfs.FileSystem) *session.Store {
		st, err := session.NewStore(fsys, root, time.Now)
		if err != nil {
			t.Fatalf("session.NewStore: %v", err)
		}
		return st
	}
	return store{
		root: root,
		put: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, body string) error {
			st := open(fsys)
			if err := st.Create(ctx, s, held, session.Meta{"about": "salary"}); err != nil {
				return err
			}
			return st.Append(ctx, s, held, "user", body)
		},
		get: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) (string, error) {
			transcript, err := open(fsys).Get(ctx, s, held)
			if err != nil {
				return "", err
			}
			said := make([]string, 0, len(transcript.Messages))
			for _, message := range transcript.Messages {
				said = append(said, message.Body)
			}
			return strings.Join(said, "\n"), nil
		},
		drop: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) error {
			return open(fsys).Delete(ctx, s, held)
		},
	}
}

func threadLog(t *testing.T) store {
	t.Helper()
	const root = "/threads"
	open := func(fsys vfs.FileSystem) *thread.Log {
		log, err := thread.NewLog(fsys, root)
		if err != nil {
			t.Fatalf("thread.NewLog: %v", err)
		}
		return log
	}
	return store{
		root: root,
		put: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, body string) error {
			log := open(fsys)
			if err := log.Start(ctx, s, held); err != nil {
				return err
			}
			return log.Append(ctx, s, held, []byte(body))
		},
		get: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) (string, error) {
			result, err := open(fsys).Read(ctx, s, held)
			if err != nil {
				return "", err
			}
			out := make([]string, 0, len(result.Entries))
			for _, entry := range result.Entries {
				out = append(out, string(entry.Data))
			}
			return strings.Join(out, "\n"), nil
		},
		drop: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) error {
			return open(fsys).Delete(ctx, s, held)
		},
	}
}

func memoryStore(t *testing.T) store {
	t.Helper()
	const root = "/memories"
	open := func(fsys vfs.FileSystem) *memory.Store {
		st, err := memory.NewStore(fsys, root, time.Now)
		if err != nil {
			t.Fatalf("memory.NewStore: %v", err)
		}
		return st
	}
	return store{
		root: root,
		put: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, body string) error {
			_, err := open(fsys).Add(ctx, s, held, []memory.Fact{{Kind: "salary", Body: body, Priority: memory.High}})
			return err
		},
		get: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) (string, error) {
			all, err := open(fsys).List(ctx, s)
			if err != nil {
				return "", err
			}
			bodies := make([]string, 0, len(all))
			for _, one := range all {
				bodies = append(bodies, one.Fact.Body)
			}
			return strings.Join(bodies, "\n"), nil
		},
	}
}

func libraryRegistry(t *testing.T) store {
	t.Helper()
	const root = "/skills"
	open := func(fsys vfs.FileSystem) *library.Registry {
		registry, err := library.NewRegistry(fsys, library.Skills(), root, termmatch.New())
		if err != nil {
			t.Fatalf("library.NewRegistry: %v", err)
		}
		return registry
	}
	return store{
		root: root,
		put: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, body string) error {
			doc := library.Document{Card: library.Card{Name: held, Description: "what to ask for"}, Body: body}
			return open(fsys).Create(ctx, s, doc)
		},
		get: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) (string, error) {
			doc, err := open(fsys).Get(ctx, s, held)
			if err != nil {
				return "", err
			}
			return doc.Body, nil
		},
		drop: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) error {
			return open(fsys).Delete(ctx, s, held)
		},
	}
}

func resourceStore(t *testing.T) store {
	t.Helper()
	const root = "/resources"
	signer, err := resource.NewSigner([]byte("a key nobody else has"), time.Now)
	if err != nil {
		t.Fatalf("resource.NewSigner: %v", err)
	}
	open := func(fsys vfs.FileSystem) *resource.Store {
		st, err := resource.NewStore(fsys, root, signer)
		if err != nil {
			t.Fatalf("resource.NewStore: %v", err)
		}
		return st
	}
	return store{
		root: root,
		put: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, body string) error {
			st := open(fsys)
			if _, err := st.Create(ctx, s, held, "text/plain"); err != nil {
				return err
			}
			if _, err := st.Upload(ctx, s, held, strings.NewReader(body)); err != nil {
				return err
			}
			_, err := st.Complete(ctx, s, held)
			return err
		},
		get: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) (string, error) {
			content, err := open(fsys).Content(ctx, s, held)
			if err != nil {
				return "", err
			}
			defer func() { _ = content.Close() }()
			data, err := io.ReadAll(content)
			return string(data), err
		},
		drop: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) error {
			return open(fsys).Delete(ctx, s, held)
		},
	}
}

func relationGraph(t *testing.T) store {
	t.Helper()
	const root = "/relations"
	open := func(fsys vfs.FileSystem) *relation.Graph {
		graph, err := relation.NewGraph(fsys, root)
		if err != nil {
			t.Fatalf("relation.NewGraph: %v", err)
		}
		return graph
	}
	// A graph stores no content, so the body is the name on the other end of
	// the edge: what leaks out of a graph is which resources a workspace put
	// together, and that is where it is written down.
	from := relation.Ref{Kind: "skill", Name: held}
	edge := func(body string) relation.Edge {
		return relation.Edge{From: from, Predicate: "uses", To: relation.Ref{Kind: "resource", Name: body}}
	}
	return store{
		root: root,
		put: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, body string) error {
			return open(fsys).Link(ctx, s, edge(strings.ReplaceAll(body, " ", "-")))
		},
		get: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) (string, error) {
			edges, err := open(fsys).List(ctx, s, from)
			if err != nil {
				return "", err
			}
			names := make([]string, 0, len(edges))
			for _, one := range edges {
				names = append(names, strings.ReplaceAll(one.To.Name, "-", " "))
			}
			return strings.Join(names, "\n"), nil
		},
		drop: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) error {
			return open(fsys).Unlink(ctx, s, edge(strings.ReplaceAll(bobsSecret, " ", "-")))
		},
	}
}

func kvSpace(t *testing.T) store {
	t.Helper()
	const root = "/scratch"
	open := func(fsys vfs.FileSystem) *kv.Space {
		space, err := kv.NewSpace(fsys, root)
		if err != nil {
			t.Fatalf("kv.NewSpace: %v", err)
		}
		return space
	}
	return store{
		root: root,
		put: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, body string) error {
			return open(fsys).Put(ctx, s, "drafts", held, []byte(body))
		},
		get: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) (string, error) {
			value, err := open(fsys).Get(ctx, s, "drafts", held)
			return string(value), err
		},
		drop: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) error {
			return open(fsys).Delete(ctx, s, "drafts", held)
		},
	}
}

func ingestLedger(t *testing.T) store {
	t.Helper()
	const root = "/indexing"
	open := func(fsys vfs.FileSystem) *ingest.Ledger {
		ledger, err := ingest.NewLedger(fsys, root, time.Now)
		if err != nil {
			t.Fatalf("ingest.NewLedger: %v", err)
		}
		return ledger
	}
	return store{
		root: root,
		put: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, body string) error {
			return open(fsys).Failed(ctx, s, held, "extract", body)
		},
		get: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) (string, error) {
			indexing, err := open(fsys).Get(ctx, s, held)
			if err != nil {
				return "", err
			}
			return indexing.Failure, nil
		},
	}
}

// A schedule log holds a moment and no content, and reads back across every
// scope at once rather than inside one, so it has neither a body to leak nor a
// scoped read to leak it through. What it has is two verbs that act on a note
// in a scope, and those are what the write and delete cases hold.
func scheduleLog(t *testing.T) store {
	t.Helper()
	const root = "/schedule"
	open := func(fsys vfs.FileSystem) *schedule.Log {
		log, err := schedule.NewLog(fsys, root, time.Now)
		if err != nil {
			t.Fatalf("schedule.NewLog: %v", err)
		}
		return log
	}
	return store{
		root: root,
		put: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope, _ string) error {
			return open(fsys).Begin(ctx, schedule.Job{Scope: s, Name: held})
		},
		drop: func(ctx context.Context, fsys vfs.FileSystem, s workspace.Scope) error {
			return open(fsys).End(ctx, schedule.Job{Scope: s, Name: held})
		},
	}
}

// Every store is named in the table, so a kind added to the package without a
// case here is caught rather than silently unheld.
func TestEveryScopedStoreIsNamedInTheTable(t *testing.T) {
	want := []string{"ingest", "kv", "library", "memory", "relation", "resource", "schedule", "session", "thread"}
	got := slices.Sorted(maps.Keys(scopedStores(t)))
	if !slices.Equal(got, want) {
		t.Errorf("scopedStores names %v, want %v", got, want)
	}
}
