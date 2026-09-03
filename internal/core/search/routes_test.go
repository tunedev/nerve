package search_test

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/core/search"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Three areas, three shapes. Memories are flat files, documents are the words
// an ingestion left, and a skill is a directory with its file inside it. What
// the routes are asked is the same call every time; only the prefix changes.
const (
	memories  = "/memories"
	documents = "/documents"
	skills    = "/skills"
	queue     = "/queue"
)

var (
	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	there = workspace.Scope{User: "grace", Workspace: "hunt"}

	shelf = map[string]map[string]string{
		memories: {
			"salary.md":   "Ada will not take a role under ninety thousand pounds.",
			"location.md": "Ada will not relocate to London and wants to stay remote.",
			"notice.md":   "Notice period: three months.\nThe current employer will not shorten it.\n",
		},
		documents: {
			"cv.txt":     "Ada Lovelace. Backend engineer. Go, Postgres, distributed systems.",
			"advert.txt": "Senior platform engineer wanted. Kubernetes, Terraform, on call.",
		},
		skills: {
			"tailor-cv/SKILL.md":   "Rewrite a CV to answer one job advert.",
			"draft-email/SKILL.md": "Write a short follow-up email to a recruiter.",
		},
	}
)

func newRoutes(t *testing.T) (*search.Routes, *vfs.MountTable) {
	t.Helper()

	table := vfs.NewMountTable()
	for _, prefix := range []string{memories, documents, skills} {
		if err := table.Mount(prefix, memfs.New()); err != nil {
			t.Fatalf("Mount(%q): %v", prefix, err)
		}
	}
	routes, err := search.NewRoutes(table, termmatch.NewRanker())
	if err != nil {
		t.Fatalf("NewRoutes: %v", err)
	}
	for prefix, entries := range shelf {
		for name, content := range entries {
			write(t, table, here, prefix, name, content)
		}
	}
	return routes, table
}

func write(t *testing.T, tree vfs.FileSystem, s workspace.Scope, prefix, name, content string) {
	t.Helper()

	full, err := s.Path(prefix, name)
	if err != nil {
		t.Fatalf("Path(%q, %q): %v", prefix, name, err)
	}
	if err := tree.Mkdir(t.Context(), path.Dir(full)); err != nil {
		t.Fatalf("Mkdir(%q): %v", path.Dir(full), err)
	}
	if err := tree.Write(t.Context(), full, []byte(content)); err != nil {
		t.Fatalf("Write(%q): %v", full, err)
	}
}

func paths(hits []search.Hit) []string {
	found := make([]string, len(hits))
	for i, hit := range hits {
		found[i] = hit.Path
	}
	return found
}

// The acceptance: one Find, asked in the caller's own words, serves memories,
// documents and skills. Nothing but the prefix differs between these calls, and
// nothing in the routes knows what any of the three areas holds.
func TestOneFindServesEveryPrefix(t *testing.T) {
	routes, _ := newRoutes(t)

	for _, tc := range []struct {
		prefix string
		query  string
		want   string
	}{
		{memories, "will she relocate to London", "location.md"},
		{documents, "an engineer who knows Postgres and Go", "cv.txt"},
		{skills, "rewrite my CV for this advert", "tailor-cv/SKILL.md"},
	} {
		hits, err := routes.Find(t.Context(), here, tc.prefix, tc.query, 0)
		if err != nil {
			t.Fatalf("Find(%q, %q): %v", tc.prefix, tc.query, err)
		}
		if len(hits) == 0 {
			t.Fatalf("Find(%q, %q) found nothing", tc.prefix, tc.query)
		}
		if hits[0].Path != tc.want {
			t.Fatalf("Find(%q, %q) = %v, want %q first", tc.prefix, tc.query, paths(hits), tc.want)
		}
	}
}

// A ranking is only worth having if it puts things in order, and a limit is
// only worth having if what it cuts is the tail.
func TestFindOrdersAndLimits(t *testing.T) {
	routes, _ := newRoutes(t)

	hits, err := routes.Find(t.Context(), here, memories, "Ada will not relocate", 0)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("Find = %v, want more than one memory", paths(hits))
	}
	if !slices.IsSortedFunc(hits, func(a, b search.Hit) int { return int(b.Score*1e6) - int(a.Score*1e6) }) {
		t.Fatalf("Find = %+v, want scores descending", hits)
	}

	limited, err := routes.Find(t.Context(), here, memories, "Ada will not relocate", 1)
	if err != nil {
		t.Fatalf("Find limited: %v", err)
	}
	if want := hits[:1]; !slices.Equal(paths(limited), paths(want)) {
		t.Fatalf("Find limited = %v, want %v", paths(limited), paths(want))
	}
}

// A route answers about the scope it was asked for and in that scope's own
// vocabulary: the user and the workspace are the layout speaking, and a caller
// inside a scope is never told the layout.
func TestARouteSeesOneScopeAndNamesNoOther(t *testing.T) {
	routes, table := newRoutes(t)
	write(t, table, there, memories, "salary.md", "Grace will not take a role under two hundred thousand pounds.")

	hits, err := routes.Find(t.Context(), here, memories, "how much will she take for a role", 0)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, hit := range hits {
		if hit.Path != path.Clean(hit.Path) || path.IsAbs(hit.Path) {
			t.Fatalf("Find = %q, want a path inside the prefix", hit.Path)
		}
	}

	mine, err := routes.Retrieve(t.Context(), here, memories, "salary.md")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := string(mine); got != shelf[memories]["salary.md"] {
		t.Fatalf("Retrieve = %q, want this scope's memory", got)
	}
}

// Retrieve is the escalation: Find answers with paths so that a caller pays for
// the bodies it actually wants, and a path it was handed is one it can name.
func TestRetrieveReturnsTheBodyBehindAHit(t *testing.T) {
	routes, _ := newRoutes(t)

	hits, err := routes.Find(t.Context(), here, skills, "rewrite my CV for this advert", 1)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Find = %v, want one hit", paths(hits))
	}

	body, err := routes.Retrieve(t.Context(), here, skills, hits[0].Path)
	if err != nil {
		t.Fatalf("Retrieve(%q): %v", hits[0].Path, err)
	}
	if got, want := string(body), shelf[skills][hits[0].Path]; got != want {
		t.Fatalf("Retrieve(%q) = %q, want %q", hits[0].Path, got, want)
	}
}

// Glob matches the whole path under the prefix, which is what makes a skill's
// directory part of its name rather than something a caller has to strip.
func TestGlobMatchesPathsAndNotOnlyNames(t *testing.T) {
	routes, _ := newRoutes(t)

	for _, tc := range []struct {
		prefix  string
		pattern string
		want    []string
	}{
		{memories, "*.md", []string{"location.md", "notice.md", "salary.md"}},
		{skills, "*.md", nil},
		{skills, "*/*.md", []string{"draft-email/SKILL.md", "tailor-cv/SKILL.md"}},
		{documents, "*.txt", []string{"advert.txt", "cv.txt"}},
	} {
		found, err := routes.Glob(t.Context(), here, tc.prefix, tc.pattern)
		if err != nil {
			t.Fatalf("Glob(%q, %q): %v", tc.prefix, tc.pattern, err)
		}
		if !slices.Equal(found, tc.want) {
			t.Fatalf("Glob(%q, %q) = %v, want %v", tc.prefix, tc.pattern, found, tc.want)
		}
	}
}

// Grep answers in path order and then in line order, numbered the way a reader
// counts lines rather than the way a slice does.
func TestGrepNumbersTheLinesItMatched(t *testing.T) {
	routes, _ := newRoutes(t)

	found, err := routes.Grep(t.Context(), here, memories, "will not")
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	want := []search.Line{
		{Path: "location.md", Number: 1, Text: shelf[memories]["location.md"]},
		{Path: "notice.md", Number: 2, Text: "The current employer will not shorten it."},
		{Path: "salary.md", Number: 1, Text: shelf[memories]["salary.md"]},
	}
	if !slices.Equal(found, want) {
		t.Fatalf("Grep = %+v, want %+v", found, want)
	}
}

// consuming is a mount where reading takes what it read, which is what a queue
// job is. Read really does consume here, so a route that read a path its mount
// called read-destructive leaves the evidence behind rather than merely
// disagreeing with a descriptor.
type consuming struct{ vfs.FileSystem }

func (consuming) Capabilities(string) vfs.Caps { return vfs.Caps{ReadDestructive: true} }

func (c consuming) Read(ctx context.Context, name string) ([]byte, error) {
	data, err := c.FileSystem.Read(ctx, name)
	if err != nil {
		return nil, err
	}
	return data, c.Remove(ctx, name)
}

func newQueue(t *testing.T) (*search.Routes, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(queue, consuming{memfs.New()}); err != nil {
		t.Fatalf("Mount(%q): %v", queue, err)
	}
	routes, err := search.NewRoutes(table, termmatch.NewRanker())
	if err != nil {
		t.Fatalf("NewRoutes: %v", err)
	}
	write(t, table, here, queue, "next", "index the CV Ada uploaded this morning")
	return routes, table
}

// The routes believe what a mount says about itself. Searching a queue by
// reading it would answer the question once and destroy the subject, so the
// three routes that read refuse, and the job is still there afterwards.
func TestTheRoutesThatReadRefuseAMountThatIsConsumedByReading(t *testing.T) {
	routes, table := newQueue(t)

	if _, err := routes.Grep(t.Context(), here, queue, "CV"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Grep = %v, want fs.ErrInvalid", err)
	}
	if _, err := routes.Find(t.Context(), here, queue, "index the CV", 0); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Find = %v, want fs.ErrInvalid", err)
	}
	if _, err := routes.Retrieve(t.Context(), here, queue, "next"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Retrieve = %v, want fs.ErrInvalid", err)
	}

	full, err := here.Path(queue, "next")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if _, err := table.Stat(t.Context(), full); err != nil {
		t.Fatalf("Stat(%q) after searching: %v, want the job still there", full, err)
	}
}

// Glob reads nothing, so the one route a queue can answer is the one that only
// needs names.
func TestGlobServesAMountThatIsConsumedByReading(t *testing.T) {
	routes, _ := newQueue(t)

	found, err := routes.Glob(t.Context(), here, queue, "*")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if want := []string{"next"}; !slices.Equal(found, want) {
		t.Fatalf("Glob = %v, want %v", found, want)
	}
}

// A caller that mistyped a pattern and a prefix that holds nothing are
// different answers, and a route that matched nothing would give them the same
// one.
func TestAPatternThatIsNotOneIsRefused(t *testing.T) {
	routes, _ := newRoutes(t)

	if _, err := routes.Glob(t.Context(), here, memories, "[unclosed"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Glob = %v, want fs.ErrInvalid", err)
	}
	if _, err := routes.Grep(t.Context(), here, memories, "(unclosed"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Grep = %v, want fs.ErrInvalid", err)
	}
}

// A scope that has never written under a prefix and a prefix that holds nothing
// are the same answer to "what is here", and neither is a failure.
func TestAPrefixHoldingNothingAnswersWithNothing(t *testing.T) {
	routes, _ := newRoutes(t)

	hits, err := routes.Find(t.Context(), there, memories, "will she relocate", 0)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Find = %v, want nothing", paths(hits))
	}
	found, err := routes.Glob(t.Context(), there, memories, "*")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("Glob = %v, want nothing", found)
	}
	lines, err := routes.Grep(t.Context(), there, memories, "will")
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("Grep = %+v, want nothing", lines)
	}
}

// A prefix that is not a mount table prefix is refused where it is named rather
// than where it is walked.
func TestAPrefixThatIsNotOneIsRefused(t *testing.T) {
	routes, _ := newRoutes(t)

	if _, err := routes.Find(t.Context(), here, "memories/../etc", "anything", 0); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Find = %v, want fs.ErrInvalid", err)
	}
}

// Routes that cannot rank are refused at composition rather than allowed to
// become a Find that panics the first time an agent asks a question.
func TestRoutesWithNothingToRankWithAreRefused(t *testing.T) {
	if _, err := search.NewRoutes(vfs.NewMountTable(), nil); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("NewRoutes with no ranker = %v, want fs.ErrInvalid", err)
	}
	if _, err := search.NewRoutes(nil, termmatch.NewRanker()); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("NewRoutes with no tree = %v, want fs.ErrInvalid", err)
	}
}
