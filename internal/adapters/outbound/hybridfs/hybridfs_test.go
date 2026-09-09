package hybridfs_test

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/hybridfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The acceptance of the ticket, stated as the case that would fail without it:
// the two rankers are read on the same corpus with wildly different scales
// behind them, and the key both of them named modestly beats the key each of
// them put first.
//
// Nothing was calibrated to make that true. The scores in the two answers are a
// similarity between nought and one and a claim count in the hundreds, and this
// case would read the same if either were replaced by anything else.
func TestAKeyBothRankersNamedBeatsAKeyEitherPutFirst(t *testing.T) {
	fusion := newFusion(t, ranking{
		vectorAt + "/pumps": {"alone-vector", "shared", "tail"},
		graphAt + "/pumps":  {"alone-graph", "shared"},
	})

	if order := fused(t, fusion, "pumps"); order[0] != "shared" {
		t.Errorf("the fused order was %v, want the key both rankers named first", order)
	}
}

// The whole of the fused answer, so that the order behind the first key is
// asserted too and not only the winner.
func TestTheFusedOrderIsEveryKeyEitherRankerNamed(t *testing.T) {
	fusion := newFusion(t, ranking{
		vectorAt + "/pumps": {"alone-vector", "shared", "tail"},
		graphAt + "/pumps":  {"alone-graph", "shared"},
	})

	want := []string{"shared", "alone-vector", "alone-graph", "tail"}
	if order := fused(t, fusion, "pumps"); !slices.Equal(order, want) {
		t.Errorf("the fused order was %v, want %v", order, want)
	}
}

// The answer says where each ranker put the key, which is the whole of what the
// score was computed from and the only way to read a fusion that looks wrong.
func TestTheAnswerSaysWhereEachRankerPutTheKey(t *testing.T) {
	fusion := newFusion(t, ranking{
		vectorAt + "/pumps": {"first", "shared"},
		graphAt + "/pumps":  {"shared"},
	})

	answer, err := fusion.Read(t.Context(), "/query/pumps")
	if err != nil {
		t.Fatalf("Read(/query/pumps) = %v", err)
	}
	if !strings.HasPrefix(string(answer), "key\tscore\tranks\n") {
		t.Errorf("the answer names its columns as %q, want a key, a score and the ranks", firstLine(answer))
	}
	if !strings.Contains(string(answer), "\tvector=2,graph=1\n") {
		t.Errorf("the answer =\n%s\nwant a row saying where each ranker put the shared key", answer)
	}
}

// Nothing in the ranking is read but the order. Two rankings whose scores are
// reversed against their order fuse exactly as they would with no scores at
// all, which is what "needs no calibration" means when it is a test.
func TestOnlyTheOrderOfARankingIsRead(t *testing.T) {
	tree := memfs.New()
	if err := tree.Mkdir(t.Context(), vectorAt); err != nil {
		t.Fatalf("Mkdir(%q) = %v", vectorAt, err)
	}
	if err := tree.Mkdir(t.Context(), graphAt); err != nil {
		t.Fatalf("Mkdir(%q) = %v", graphAt, err)
	}
	if err := tree.Write(t.Context(), vectorAt+"/pumps",
		[]byte("key\tdistance\nshared\t0.9812\nother\t0.0004\n")); err != nil {
		t.Fatalf("Write(%q) = %v", vectorAt, err)
	}
	if err := tree.Write(t.Context(), graphAt+"/pumps",
		[]byte("key\tclaims\nshared\t2\nthird\t914\n")); err != nil {
		t.Fatalf("Write(%q) = %v", graphAt, err)
	}

	fusion := hybridfs.New("fused", hybridfs.DefaultDamping, results,
		[]hybridfs.Source{{Name: vector, Path: vectorAt}, {Name: graph, Path: graphAt}},
		func() vfs.FileSystem { return tree })

	want := []string{"shared", "other", "third"}
	if order := fused(t, fusion, "pumps"); !slices.Equal(order, want) {
		t.Errorf("the fused order was %v, want %v: the counts in the second column say otherwise", order, want)
	}
}

// A ranker that names nothing is an answer and not a failure: an index over an
// empty corpus and a graph that never heard of the entity both answer, and the
// fusion is then whatever the other said.
func TestARankerThatNamesNothingLeavesTheOthersRanking(t *testing.T) {
	fusion := newFusion(t, ranking{
		vectorAt + "/pumps": {"one", "two"},
		graphAt + "/pumps":  {},
	})

	want := []string{"one", "two"}
	if order := fused(t, fusion, "pumps"); !slices.Equal(order, want) {
		t.Errorf("the fused order was %v, want %v", order, want)
	}
}

// A ranker that fails fails the read. A fused answer missing one of its
// rankings is the answer to a different question, and nothing in the answer
// would say so.
func TestARankerThatFailsFailsTheRead(t *testing.T) {
	fusion := newFusion(t, ranking{vectorAt + "/pumps": {"one"}})

	_, err := fusion.Read(t.Context(), "/query/pumps")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/query/pumps) with a ranker that could not answer = %v, want the ranker's own error", err)
	}
	if !strings.Contains(err.Error(), graph) {
		t.Errorf("Read(/query/pumps) = %v, want the name of the ranker that failed", err)
	}
}

// The answer is bounded, because it is read by something with room for a
// handful of keys.
func TestTheFusedAnswerIsBoundedByTheResultsSetting(t *testing.T) {
	keys := make([]string, 0, results+5)
	for at := range results + 5 {
		keys = append(keys, string(rune('a'+at)))
	}
	fusion := newFusion(t, ranking{
		vectorAt + "/pumps": keys,
		graphAt + "/pumps":  {},
	})

	if order := fused(t, fusion, "pumps"); len(order) != results {
		t.Errorf("the fusion answered with %d keys, want the %d the setting bounds it to", len(order), results)
	}
}

// A ranker that names a key twice has said nothing new. Counting the repeat
// would let one ranker outvote the others by repeating itself.
func TestARepeatedKeyIsPlacedOnce(t *testing.T) {
	fusion := newFusion(t, ranking{
		vectorAt + "/pumps": {"twice", "other", "twice"},
		graphAt + "/pumps":  {"other"},
	})

	answer, err := fusion.Read(t.Context(), "/query/pumps")
	if err != nil {
		t.Fatalf("Read(/query/pumps) = %v", err)
	}
	if strings.Count(string(answer), "twice\t") != 1 {
		t.Errorf("the answer =\n%s\nwant the repeated key in one row", answer)
	}
	if order := fused(t, fusion, "pumps"); order[0] != "other" {
		t.Errorf("the fused order was %v, want the key both rankers named first", order)
	}
}

// A question with nothing in it is refused rather than asked, for the reason an
// index refuses one: there is nothing to rank against.
func TestAQuestionWithNothingInItIsRefused(t *testing.T) {
	fusion := newFusion(t, ranking{})

	_, err := fusion.Read(t.Context(), "/query/ ")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Read(/query/ ) = %v, want fs.ErrInvalid", err)
	}
}

// The config path says which rankings stand behind an answer, which is the
// first thing anyone asks when one looks wrong.
func TestTheConfigNamesTheRankingsItReads(t *testing.T) {
	fusion := newFusion(t, ranking{})

	answer, err := fusion.Read(t.Context(), "/config")
	if err != nil {
		t.Fatalf("Read(/config) = %v", err)
	}

	want := "hybrid fused\ndamping 60\nresults 10\nsources vector=/index/query;graph=/graph/mentions\n"
	if string(answer) != want {
		t.Errorf("Read(/config) =\n%q\nwant\n%q", answer, want)
	}
}

// The questions hold nothing, so there is nothing to list: every name under
// them is a question rather than an entry.
func TestTheQuestionsListNothing(t *testing.T) {
	fusion := newFusion(t, ranking{vectorAt + "/pumps": {"one"}, graphAt + "/pumps": {"one"}})

	entries, err := fusion.ReadDir(t.Context(), "/query")
	if err != nil {
		t.Fatalf("ReadDir(/query) = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(/query) listed %d entries, want none", len(entries))
	}
}

// A fusion holds nothing of its own, so every mutating verb is a permission
// refusal: there is nothing a caller could create first to make it work.
func TestAFusionRefusesEveryWrite(t *testing.T) {
	fusion := newFusion(t, ranking{})

	for name, verb := range map[string]func() error{
		"write":  func() error { return fusion.Write(t.Context(), "/query/pumps", nil) },
		"create": func() error { return fusion.Create(t.Context(), "/query/pumps") },
		"mkdir":  func() error { return fusion.Mkdir(t.Context(), "/query/pumps") },
		"remove": func() error { return fusion.Remove(t.Context(), "/config") },
		"rename": func() error { return fusion.Rename(t.Context(), "/config", "/other") },
		"chmod":  func() error { return fusion.Chmod(t.Context(), "/config", 0o600) },
	} {
		if err := verb(); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%s = %v, want fs.ErrPermission", name, err)
		}
	}
}

// Describing a question costs what answering it costs, and reports the size of
// the answer, because a question has no other size to report.
func TestStatOfAQuestionIsTheSizeOfTheAnswer(t *testing.T) {
	fusion := newFusion(t, ranking{vectorAt + "/pumps": {"one"}, graphAt + "/pumps": {"one"}})

	answer, err := fusion.Read(t.Context(), "/query/pumps")
	if err != nil {
		t.Fatalf("Read(/query/pumps) = %v", err)
	}
	info, err := fusion.Stat(t.Context(), "/query/pumps")
	if err != nil {
		t.Fatalf("Stat(/query/pumps) = %v", err)
	}
	if info.Size != int64(len(answer)) {
		t.Errorf("Stat(/query/pumps).Size = %d, want %d", info.Size, len(answer))
	}
}

// firstLine is what a ranking calls its columns, for an error message.
func firstLine(answer []byte) string {
	line, _, _ := strings.Cut(string(answer), "\n")
	return line
}
