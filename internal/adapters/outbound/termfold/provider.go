package termfold

import (
	"fmt"
	"io/fs"
	"strconv"

	"github.com/tunedev/nerve/internal/core/llm"
)

// Kind is the name a provider config asks for this endpoint by.
const Kind = "termfold"

// ShareSetting names how much vocabulary two facts have to share to be one
// claim. It is the one thing a config can say to this endpoint, and it is a
// setting because how alike two sentences have to be before a workspace stops
// saying both is an operator's judgement rather than this package's.
const ShareSetting = "share"

// DefaultShare is what two facts share when a config names nothing: two words
// in five. It is enough that a claim and the same claim narrowed merge, and
// little enough that two claims sharing only the word the category is about do
// not.
const DefaultShare = 0.4

// Public names the settings this kind reads back to an operator. The threshold
// is operational rather than secret, and this endpoint holds no credential, so
// there is none here to redact.
var Public = []string{ShareSetting}

// Provider presents the term consolidator as a model endpoint, so the one path
// a narrative takes is the same whether a model wrote the sentence or the
// workspace's own claims were joined into it.
//
// It serves consolidation and nothing else, which is what the embedded default
// says for it: an endpoint that counts words summarises nothing, and a chain
// asked for a summary passes over it.
type Provider struct {
	llm.NoAccessors
	consolidator Consolidator
}

// AsFactConsolidator returns the consolidator this endpoint is.
func (p Provider) AsFactConsolidator() llm.FactConsolidator { return p.consolidator }

// Factory builds the provider. A config that names no threshold gets the
// default; one that names something that is not a number is refused rather than
// falling back to it, because a typo that quietly merges everything is a
// workspace that answers with one paragraph.
func Factory(settings map[string]string) (llm.Provider, error) {
	share := DefaultShare
	if named := settings[ShareSetting]; named != "" {
		parsed, err := strconv.ParseFloat(named, 64)
		if err != nil {
			return nil, fmt.Errorf("%s %q is not a number: %w: %w", ShareSetting, named, err, fs.ErrInvalid)
		}
		share = parsed
	}
	consolidator, err := New(share)
	if err != nil {
		return nil, err
	}
	return Provider{consolidator: consolidator}, nil
}
