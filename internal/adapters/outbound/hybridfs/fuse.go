package hybridfs

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

// heading names the columns a fused answer carries. The ranks column is what
// makes the fusion readable: it says which rankers named the key and where each
// of them put it, which is the whole of what the score was computed from.
const heading = "key\tscore\tranks\n"

// scoreDigits is how much of a fused score is worth reading back. A score
// orders one answer and means nothing between two, so the digits are there to
// be compared with each other and not to be arithmetic.
const scoreDigits = 6

// The spelling of the ranks column: one ranker's placing, and what separates
// two of them.
const (
	betweenRanks = ","
	atRank       = "="
)

// standing is one key's place in the fused order.
type standing struct {
	key   string
	score float64
	// ranks is where each ranker that named the key put it, in the order the
	// mount lists its sources, so two rows read side by side line up.
	ranks []string
	// best is the best rank any ranker gave the key, and found is the first
	// source that named it. Both are tie-breaks, and they are there so that two
	// runs over one pair of rankings answer identically.
	best  int
	found int
}

// fuse asks every ranker the question and folds their answers into one order.
//
// A ranker that fails fails the read. A fused answer missing one of its
// rankings is the answer to a different question, and nothing in a ranking says
// which rankers stood behind it, so a caller could not tell the two apart.
// Degrading here would trade a visible failure for an invisible one.
//
// A ranker that names nothing is not a failure: an index over an empty corpus
// and a graph that has never heard of the entity both answer, and the fusion is
// then whatever the others said.
func (f *FS) fuse(ctx context.Context, question string) ([]byte, error) {
	if strings.TrimSpace(question) == "" {
		return nil, fmt.Errorf("a question with nothing in it: %w", fs.ErrInvalid)
	}

	held := map[string]*standing{}
	for at, source := range f.sources {
		keys, err := f.rank(ctx, source, question)
		if err != nil {
			return nil, fmt.Errorf("ranker %q: %w", source.Name, err)
		}
		f.place(held, at, source, keys)
	}

	order := make([]*standing, 0, len(held))
	for _, one := range held {
		order = append(order, one)
	}
	slices.SortFunc(order, ranked)
	if len(order) > f.results {
		order = order[:f.results]
	}

	report := strings.Builder{}
	report.WriteString(heading)
	for _, one := range order {
		report.WriteString(one.key)
		report.WriteByte('\t')
		report.WriteString(strconv.FormatFloat(one.score, 'f', scoreDigits, 64))
		report.WriteByte('\t')
		report.WriteString(strings.Join(one.ranks, betweenRanks))
		report.WriteByte('\n')
	}
	return []byte(report.String()), nil
}

// place folds one ranker's answer into the standings. Ranks count from one, so
// the constant damps a real first place rather than a zeroth one.
//
// A key one ranker names twice is placed where it first appeared. A ranking is
// an order over keys, so a repeat says nothing new, and counting it twice would
// let a ranker outvote the others by repeating itself.
func (f *FS) place(held map[string]*standing, at int, source Source, keys []string) {
	for position, key := range keys {
		rank := position + 1
		one, already := held[key]
		if !already {
			one = &standing{key: key, best: rank, found: at}
			held[key] = one
		}
		if placedBy(one, source) {
			continue
		}
		one.score += 1 / float64(f.damping+rank)
		one.ranks = append(one.ranks, source.Name+atRank+strconv.Itoa(rank))
		if rank < one.best {
			one.best = rank
		}
	}
}

// placedBy reports whether a source has already placed a key, which is what
// makes a ranker's repeat of its own row cost nothing.
func placedBy(one *standing, source Source) bool {
	return slices.ContainsFunc(one.ranks, func(spelled string) bool {
		return strings.HasPrefix(spelled, source.Name+atRank)
	})
}

// ranked orders two keys: the better standing first, then the better rank any
// one ranker gave it, then the source that named it, then the key.
func ranked(a, b *standing) int {
	switch {
	case a.score > b.score:
		return -1
	case a.score < b.score:
		return 1
	case a.best != b.best:
		return a.best - b.best
	case a.found != b.found:
		return a.found - b.found
	default:
		return strings.Compare(a.key, b.key)
	}
}

// rank reads one source's answer to the question and takes the keys out of it,
// in the order it gave them.
func (f *FS) rank(ctx context.Context, source Source, question string) ([]string, error) {
	answer, err := f.tree().Read(ctx, path.Join(source.Path, question))
	if err != nil {
		return nil, err
	}
	return keysOf(answer), nil
}

// keysOf reads a ranking: a heading line naming the columns, then one row per
// result whose first tab-separated field is the key.
//
// The heading is dropped rather than checked. What its columns are called is
// each ranker's own business — an index calls its second column a score and a
// graph calls its second column a count — and a fusion that read only rankers
// whose columns it recognised would need editing to fuse a new kind of index.
// What it needs from every ranker is the same two things: the order, and the
// key in it.
func keysOf(answer []byte) []string {
	rows := strings.Split(strings.TrimRight(string(answer), "\n"), "\n")
	if len(rows) <= 1 {
		return nil
	}

	keys := make([]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		key, _, _ := strings.Cut(row, "\t")
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}
