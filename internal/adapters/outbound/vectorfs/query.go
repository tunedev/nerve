package vectorfs

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// heading names the columns an answer carries, so what a reader gets is
// self-describing rather than a column of numbers in an order it has to know.
const heading = "key\tscore\n"

// scoreDigits is how much of a score is worth reading back. Scores order an
// answer and mean nothing between two answers, so the digits are there to be
// compared with each other and not to be arithmetic.
const scoreDigits = 4

// answer ranks the corpus against question and reports the closest documents,
// closest first.
//
// The question is embedded by the same endpoint the documents were, which is
// the condition that makes the comparison mean anything: two vectors are
// comparable only when one model made both. A chain that failed over to a
// different embedding model would produce numbers that sort and answer nothing,
// which is why the gateway's doc names one model at two endpoints as the
// operator's line to hold.
//
// A corpus with nothing in it answers with the heading and no rows. That is an
// answer — the question was asked and nothing matched — rather than a missing
// path, which is what a caller would have to distinguish an empty index from.
func (f *FS) answer(ctx context.Context, question string) ([]byte, error) {
	vector, err := f.vectorOf(ctx, []byte(question))
	if err != nil {
		return nil, err
	}
	if vector == nil {
		return nil, fmt.Errorf("a question with nothing in it: %w", fs.ErrInvalid)
	}

	hits, err := f.index.Nearest(ctx, vector, f.neighbours)
	if err != nil {
		return nil, err
	}

	report := strings.Builder{}
	report.WriteString(heading)
	for _, hit := range hits {
		report.WriteString(hit.Key)
		report.WriteByte('\t')
		report.WriteString(strconv.FormatFloat(hit.Score, 'f', scoreDigits, 64))
		report.WriteByte('\n')
	}
	return []byte(report.String()), nil
}

// vectorOf embeds text, or answers with no vector when there is no text.
//
// The width is checked here, at the plugin's edge, because the two places that
// know a width cannot check each other: the column was declared from mount
// config and the model reports nothing about itself. A mismatch is a
// misconfiguration — the model behind the gateway is not the model the mount
// was sized for — so it is fs.ErrInvalid naming both numbers, which is a
// sentence an operator can act on. Letting it through would be a Postgres error
// on the insert, later and about a column.
//
// The mount is not probed at boot. Making a mount refuse to come up because a
// model endpoint is down would defeat the gateway, whose whole point is that
// endpoints come and go behind it.
func (f *FS) vectorOf(ctx context.Context, text []byte) ([]float32, error) {
	if len(text) == 0 {
		return nil, nil
	}

	vectors, err := f.embed.Embed(ctx, []string{string(text)})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("one text embedded to %d vectors: %w", len(vectors), fs.ErrInvalid)
	}
	if len(vectors[0]) != f.dimension {
		return nil, fmt.Errorf("index %q holds vectors of %d and was handed one of %d: %w",
			f.name, f.dimension, len(vectors[0]), fs.ErrInvalid)
	}
	return vectors[0], nil
}
