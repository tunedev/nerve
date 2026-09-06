package termvec

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
)

// Kind is the name a provider config asks for this endpoint by.
const Kind = "termvec"

// DimensionSetting is how wide the vectors are. It is spelled the way a
// vectorfs mount spells it, because the two numbers have to be the same number:
// the mount sized its column from config and this endpoint fills it.
const DimensionSetting = "dimension"

// DefaultDimension is how wide the vectors are when a config names no width.
//
// There is a default because no width is the right one — buckets are a budget
// against collisions and not a property of a model — where there is no default
// model for Ollama to embed with. It is wide enough that an ordinary document's
// vocabulary mostly lands in buckets of its own and narrow enough to be cheap.
const DefaultDimension = 256

// Public names the settings this kind reads back to an operator. The width is
// the whole of what it takes and it is operational rather than secret: this
// endpoint reaches nothing, so there is no credential here to redact.
var Public = []string{DimensionSetting}

// Provider presents the bag-of-words embedder as a model endpoint, so the one
// path a vector takes is the same whether a model made it or the document did.
//
// It serves embedding and nothing else, which is what the embedded default says
// for it: an endpoint that counts words writes no sentences and extracts
// nothing, and a chain asked for either passes over it.
type Provider struct {
	llm.NoAccessors
	embedder Embedder
}

// AsEmbedder returns the embedder this endpoint is.
func (p Provider) AsEmbedder() llm.Embedder { return p.embedder }

// Factory builds the provider over the width an operator names.
//
// A setting it does not take is refused rather than ignored, because the width
// is the one number that has to agree with the mount holding these vectors. A
// misspelled key that defaulted quietly would come back as a mount refusing
// every document, and the sentence naming the wrong key never gets said.
func Factory(settings map[string]string) (llm.Provider, error) {
	if extra := slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(key string) bool { return slices.Contains(Public, key) }); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %s, and was given %s: %w", Kind, strings.Join(Public, ", "), strings.Join(extra, ", "), fs.ErrInvalid)
	}

	width := DefaultDimension
	if spelled, sized := settings[DimensionSetting]; sized {
		read, err := strconv.Atoi(spelled)
		if err != nil {
			return nil, fmt.Errorf("kind %q: setting %q is %q, want a number: %w", Kind, DimensionSetting, spelled, fs.ErrInvalid)
		}
		width = read
	}

	embedder, err := New(width)
	if err != nil {
		return nil, fmt.Errorf("kind %q: %w", Kind, err)
	}
	return Provider{embedder: embedder}, nil
}
