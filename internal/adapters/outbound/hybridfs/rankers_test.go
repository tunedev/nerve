package hybridfs_test

import (
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/hybridfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The two paths the fusions in these cases read their rankings at, and the
// names they report them under.
const (
	vectorAt = "/index/query"
	graphAt  = "/graph/mentions"
	vector   = "vector"
	graph    = "graph"
)

// results is how many keys the fusions in these cases answer with, large enough
// that a case sees the whole fused order.
const results = 10

// ranking is what each source answers, by question: the keys it names, best
// first. A case writes the rankings it wants fused and asserts the order that
// comes back, which is the whole of what this plugin does.
type ranking map[string][]string

// newFusion is a fusion over two sources whose answers a case wrote, held in a
// memfs so that the sources are ordinary paths in an ordinary tree. Nothing in
// the plugin knows what kind of mount answered, which is what makes fusing a
// new sort of index config rather than code.
func newFusion(t *testing.T, answers ranking, sources ...hybridfs.Source) *hybridfs.FS {
	t.Helper()

	if len(sources) == 0 {
		sources = []hybridfs.Source{{Name: vector, Path: vectorAt}, {Name: graph, Path: graphAt}}
	}

	tree := memfs.New()
	for _, source := range sources {
		if err := tree.Mkdir(t.Context(), source.Path); err != nil {
			t.Fatalf("Mkdir(%q) = %v", source.Path, err)
		}
	}
	for at, keys := range answers {
		if err := tree.Write(t.Context(), at, []byte(spelled(keys))); err != nil {
			t.Fatalf("Write(%q) = %v", at, err)
		}
	}

	return hybridfs.New("fused", hybridfs.DefaultDamping, results, sources,
		func() vfs.FileSystem { return tree })
}

// spelled is a ranking as a ranker writes one: a heading line naming its
// columns, then one row per result whose first field is the key. The second
// column is a number this package never reads, which is the point.
func spelled(keys []string) string {
	report := strings.Builder{}
	report.WriteString("key\tscore\n")
	for at, key := range keys {
		report.WriteString(key)
		report.WriteByte('\t')
		report.WriteString(strings.Repeat("9", at+1))
		report.WriteByte('\n')
	}
	return report.String()
}

// fused is the keys a fusion answered with, in order, without the scores a case
// has no business asserting.
func fused(t *testing.T, fusion *hybridfs.FS, question string) []string {
	t.Helper()

	answer, err := fusion.Read(t.Context(), "/query/"+question)
	if err != nil {
		t.Fatalf("Read(/query/%s) = %v", question, err)
	}

	var keys []string
	for _, row := range strings.Split(strings.TrimRight(string(answer), "\n"), "\n")[1:] {
		key, _, _ := strings.Cut(row, "\t")
		keys = append(keys, key)
	}
	return keys
}
