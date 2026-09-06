package termvec_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/termvec"
)

// built is the provider one config document names, or the reason there is none.
func built(t *testing.T, settings map[string]string) termvec.Provider {
	t.Helper()

	provider, err := termvec.Factory(settings)
	if err != nil {
		t.Fatalf("Factory(%v) = %v", settings, err)
	}

	built, ok := provider.(termvec.Provider)
	if !ok {
		t.Fatalf("Factory returned %T, want the provider", provider)
	}
	return built
}

// The embedder reaches a caller as a model endpoint, so the one path a vector
// takes does not depend on whether a model made it.
func TestTheProviderServesEmbedding(t *testing.T) {
	embedder := built(t, nil).AsEmbedder()
	if embedder == nil {
		t.Fatal("AsEmbedder = nil, want the embedder")
	}

	vectors, err := embedder.Embed(context.Background(), []string{"the pumps had not run"})
	if err != nil {
		t.Fatalf("Embed = %v", err)
	}
	if len(vectors) != 1 || len(vectors[0]) != termvec.DefaultDimension {
		t.Errorf("Embed returned %d vectors of %d, want one of %d", len(vectors), len(vectors[0]), termvec.DefaultDimension)
	}
}

// It embeds and does nothing else, so a chain asked for a summary or a fact
// passes over it and reaches the endpoint that has one.
func TestTheProviderServesNothingElse(t *testing.T) {
	provider := built(t, nil)

	if summariser := provider.AsSummariser(); summariser != nil {
		t.Errorf("AsSummariser = %v, want nil", summariser)
	}
	if extractor := provider.AsFactExtractor(); extractor != nil {
		t.Errorf("AsFactExtractor = %v, want nil", extractor)
	}
	if writer := provider.AsSQLWriter(); writer != nil {
		t.Errorf("AsSQLWriter = %v, want nil", writer)
	}
}

// The width is what a mount's dimension has to be, so config names it and the
// vectors come back that wide.
func TestTheWidthIsConfig(t *testing.T) {
	embedder := built(t, map[string]string{termvec.DimensionSetting: "16"}).AsEmbedder()

	vectors, err := embedder.Embed(context.Background(), []string{"the pumps"})
	if err != nil {
		t.Fatalf("Embed = %v", err)
	}
	if len(vectors[0]) != 16 {
		t.Errorf("Embed returned a vector of %d, want 16", len(vectors[0]))
	}
}

// A width that is not a number, and one with no bucket in it, are boot failures
// rather than an endpoint that fills an index with vectors of a width nobody
// asked for.
func TestAWidthThatIsNotAWidthStopsTheBuild(t *testing.T) {
	for _, spelled := range []string{"wide", "", "0", "-4"} {
		if _, err := termvec.Factory(map[string]string{termvec.DimensionSetting: spelled}); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Factory(dimension=%q) = %v, want %v", spelled, err, fs.ErrInvalid)
		}
	}
}

// A setting this kind does not take is refused rather than ignored: the width
// is the one number that has to agree with the mount, and a misspelled key that
// defaulted quietly would surface as a mount refusing every document.
func TestASettingThisKindDoesNotTakeIsRefused(t *testing.T) {
	if _, err := termvec.Factory(map[string]string{"dimensions": "768"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(dimensions=768) = %v, want %v", err, fs.ErrInvalid)
	}
}
