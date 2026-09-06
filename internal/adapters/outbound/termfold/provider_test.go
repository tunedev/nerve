package termfold_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/termfold"
	"github.com/tunedev/nerve/internal/core/llm"
)

// The endpoint serves consolidation and nothing else, so a chain asked for a
// summary or a vector passes over it.
func TestTheEndpointServesConsolidationAlone(t *testing.T) {
	provider, err := termfold.Factory(nil)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	if provider.AsFactConsolidator() == nil {
		t.Error("AsFactConsolidator = nil, want the consolidator this endpoint is")
	}
	if provider.AsSummariser() != nil || provider.AsEmbedder() != nil || provider.AsFactExtractor() != nil {
		t.Error("the endpoint answered an accessor it does not serve")
	}
}

// A config that names a threshold gets it, and one that names nothing gets the
// default rather than an endpoint that merges everything.
func TestTheThresholdComesFromConfig(t *testing.T) {
	provider, err := termfold.Factory(map[string]string{termfold.ShareSetting: "0.9"})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	facts := []llm.Fact{
		{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		{Kind: "preference", Body: "prefers tabs in Go", Priority: llm.Normal},
	}

	narratives, err := provider.AsFactConsolidator().Consolidate(t.Context(), facts)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(narratives) != 0 {
		t.Errorf("Consolidate at 0.9 = %+v, want nothing: the config asked for near-identical claims", narratives)
	}

	byDefault, err := termfold.Factory(nil)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	if narratives, err := byDefault.AsFactConsolidator().Consolidate(t.Context(), facts); err != nil || len(narratives) != 1 {
		t.Errorf("Consolidate at the default = %+v, %v, want the one narrative", narratives, err)
	}
}

// A threshold that is not a number is refused rather than quietly falling back
// to the default: a typo that merged everything would be read as a workspace
// that had one long preference.
func TestAThresholdThatIsNotANumberIsRefused(t *testing.T) {
	if _, err := termfold.Factory(map[string]string{termfold.ShareSetting: "most"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory with a threshold of %q = %v, want fs.ErrInvalid", "most", err)
	}
}
