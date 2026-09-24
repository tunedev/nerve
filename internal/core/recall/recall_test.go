package recall_test

import (
	"errors"
	"io/fs"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/hybridfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/recall"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Where each area stands in the table the cases build.
const (
	memoriesAt = "/memories"
	corpusAt   = "/corpus"
	rankedAt   = "/ranked"
	fusionAt   = "/retrieval"
)

var (
	ada = workspace.Scope{User: "ada", Workspace: "hunt"}
	bob = workspace.Scope{User: "bob", Workspace: "hunt"}
)

var (
	london = memory.Fact{Kind: "identity", Body: "lives in London", Priority: memory.Low}
	tabs   = memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal}
	never  = memory.Fact{Kind: "instruction", Body: "never force push", Priority: memory.High}
)

// world is a table holding a memory log, a corpus, and a fusion over one
// ranking whose answers a case writes, so a case says which keys a question
// ranks and asserts what the turn's context was built from.
type world struct {
	table    *vfs.MountTable
	memories *memory.Store
	routes   *recall.Routes
}

func newWorld(t *testing.T) world {
	t.Helper()

	table := vfs.NewMountTable()
	fusion := hybridfs.New("retrieval", hybridfs.DefaultDamping, 10,
		[]hybridfs.Source{{Name: "ranked", Path: rankedAt}},
		func() vfs.FileSystem { return table })
	for at, fsys := range map[string]vfs.FileSystem{
		memoriesAt: memfs.New(), corpusAt: memfs.New(), rankedAt: memfs.New(), fusionAt: fusion,
	} {
		if err := table.Mount(at, fsys); err != nil {
			t.Fatalf("Mount(%q) = %v", at, err)
		}
	}

	epoch := time.Date(2026, time.September, 24, 9, 0, 0, 0, time.UTC)
	memories, err := memory.NewStore(table, memoriesAt, func() time.Time { return epoch })
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	routes, err := recall.NewRoutes(memories, table, fusionAt, corpusAt)
	if err != nil {
		t.Fatalf("NewRoutes = %v", err)
	}
	return world{table: table, memories: memories, routes: routes}
}

// document puts text into the corpus under key, where key is the whole key the
// rankers name.
func (w world) document(t *testing.T, key, text string) {
	t.Helper()
	dir := corpusAt
	for _, part := range strings.Split(path.Dir(key), "/") {
		dir = path.Join(dir, part)
		if err := w.table.Mkdir(t.Context(), dir); err != nil && !errors.Is(err, fs.ErrExist) {
			t.Fatalf("Mkdir(%q) = %v", dir, err)
		}
	}
	if err := w.table.Write(t.Context(), corpusAt+"/"+key, []byte(text)); err != nil {
		t.Fatalf("Write(%q) = %v", key, err)
	}
}

// ranks makes question answer with keys, best first, in the shape every ranker
// answers: a heading, then one row per key.
func (w world) ranks(t *testing.T, question string, keys ...string) {
	t.Helper()
	rows := []string{"key\tscore"}
	for _, key := range keys {
		rows = append(rows, key+"\t1")
	}
	if err := w.table.Write(t.Context(), rankedAt+"/"+question, []byte(strings.Join(rows, "\n")+"\n")); err != nil {
		t.Fatalf("Write(ranking %q) = %v", question, err)
	}
}

func (w world) remember(t *testing.T, s workspace.Scope, facts ...memory.Fact) []memory.Memory {
	t.Helper()
	written, err := w.memories.Add(t.Context(), s, "chat", facts)
	if err != nil {
		t.Fatalf("Add = %v", err)
	}
	return written
}

// The acceptance: what a workspace remembers and what retrieval found for the
// turn both come back, the document as the body that was put in.
func TestATurnsContextHoldsItsMemoriesAndTheDocumentsRetrievedForIt(t *testing.T) {
	w := newWorld(t)
	w.remember(t, ada, tabs)
	w.document(t, "ada/hunt/cv", "Ada led the Postgres migration.")
	w.ranks(t, "what did I do with Postgres", "ada/hunt/cv")

	got, err := w.routes.Context(t.Context(), ada, "what did I do with Postgres", 0)
	if err != nil {
		t.Fatalf("Context = %v", err)
	}

	if len(got.Memories) != 1 || got.Memories[0].Fact != tabs {
		t.Errorf("Memories = %+v, want the one fact remembered", got.Memories)
	}
	want := []recall.Document{{Key: "cv", Body: "Ada led the Postgres migration."}}
	if len(got.Documents) != 1 || got.Documents[0] != want[0] {
		t.Errorf("Documents = %+v, want %+v", got.Documents, want)
	}
}

// A ranking is over the whole index, so it can name another workspace's
// document. Such a key is passed over and its body is never read.
func TestAKeyOutsideTheScopeIsNeverRead(t *testing.T) {
	w := newWorld(t)
	w.document(t, "bob/hunt/salary", "Bob expects 90k.")
	w.document(t, "ada/hunt/cv", "Ada led the Postgres migration.")
	w.document(t, "ada/hunt-old/cv", "An older workspace of Ada's.")
	w.ranks(t, "salary", "bob/hunt/salary", "ada/hunt/../../bob/hunt/salary", "ada/hunt-old/cv", "ada/hunt/cv")

	got, err := w.routes.Context(t.Context(), ada, "salary", 0)
	if err != nil {
		t.Fatalf("Context = %v", err)
	}
	if len(got.Documents) != 1 || got.Documents[0].Key != "cv" || got.Documents[0].Body != "Ada led the Postgres migration." {
		t.Errorf("Documents = %+v, want only ada/hunt's cv", got.Documents)
	}

	theirs, err := w.routes.Context(t.Context(), bob, "salary", 0)
	if err != nil {
		t.Fatalf("Context(bob) = %v", err)
	}
	if len(theirs.Documents) != 1 || theirs.Documents[0].Key != "salary" {
		t.Errorf("Documents(bob) = %+v, want only bob's salary", theirs.Documents)
	}
}

// The profile is what the workspace believes, most worth keeping first, cut to
// the room the caller has. A superseded memory is not believed and is not in
// it; another workspace's memories never are.
func TestTheProfileLeadsWithWhatMattersMost(t *testing.T) {
	w := newWorld(t)
	w.remember(t, bob, never)
	written := w.remember(t, ada, london, tabs, never)
	berlin := memory.Fact{Kind: "identity", Body: "lives in Berlin", Priority: memory.Low}
	if _, err := w.memories.Supersede(t.Context(), ada, "chat", []int{written[0].Seq}, []memory.Fact{berlin}); err != nil {
		t.Fatalf("Supersede = %v", err)
	}
	w.ranks(t, "hello")

	got, err := w.routes.Context(t.Context(), ada, "hello", 0)
	if err != nil {
		t.Fatalf("Context = %v", err)
	}
	if bodies := bodiesOf(got.Memories); bodies != "never force push|prefers tabs|lives in Berlin" {
		t.Errorf("Memories = %s, want highest priority first and nothing superseded", bodies)
	}

	cut, err := w.routes.Context(t.Context(), ada, "hello", 2)
	if err != nil {
		t.Fatalf("Context(limit 2) = %v", err)
	}
	if bodies := bodiesOf(cut.Memories); bodies != "never force push|prefers tabs" {
		t.Errorf("Memories(limit 2) = %s, want the two worth most", bodies)
	}
}

// A turn is asked as a question under the fusion, so a separator in it would
// name a place rather than ask something. Its words are asked; the separators
// are not.
func TestATurnWithASeparatorIsAskedAsItsWords(t *testing.T) {
	w := newWorld(t)
	w.document(t, "ada/hunt/pipeline", "The CI runs on every push.")
	w.ranks(t, "how does CI CD work", "ada/hunt/pipeline")

	got, err := w.routes.Context(t.Context(), ada, "how does CI/CD work", 0)
	if err != nil {
		t.Fatalf("Context = %v", err)
	}
	if len(got.Documents) != 1 || got.Documents[0].Key != "pipeline" {
		t.Errorf("Documents = %+v, want the pipeline document", got.Documents)
	}
}

// A turn that asks nothing is refused rather than answered with the whole
// profile and no retrieval, which would read as a turn nothing matched.
func TestATurnThatAsksNothingIsRefused(t *testing.T) {
	w := newWorld(t)
	for _, turn := range []string{"", "   ", "/", ".."} {
		if _, err := w.routes.Context(t.Context(), ada, turn, 0); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Context(%q) = %v, want fs.ErrInvalid", turn, err)
		}
	}
}

func bodiesOf(memories []memory.Memory) string {
	bodies := make([]string, 0, len(memories))
	for _, m := range memories {
		bodies = append(bodies, m.Fact.Body)
	}
	return strings.Join(bodies, "|")
}
