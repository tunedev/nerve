package nounlink_test

import (
	"context"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/nounlink"
	"github.com/tunedev/nerve/internal/core/llm"
)

// read is one extraction, for a test that cares about what came back rather
// than about how it asked.
func read(t *testing.T, text string) llm.Extraction {
	t.Helper()

	found, err := nounlink.New().ExtractEntities(context.Background(), text)
	if err != nil {
		t.Fatalf("ExtractEntities(%q) = %v", text, err)
	}
	return found
}

// names is what an extraction called the things it found.
func names(found llm.Extraction) []string {
	said := make([]string, len(found.Entities))
	for i, entity := range found.Entities {
		said[i] = entity.Name
	}
	return said
}

// A capitalised word names something, and a run of them names one thing. What
// comes back is typed and not guessed at: a capital says a word names
// something and says nothing about what.
func TestACapitalisedRunIsOneEntity(t *testing.T) {
	found := read(t, "The pipeline writes to Object Store and reads Postgres.")

	if want := []string{"Object Store", "Postgres"}; !slices.Equal(names(found), want) {
		t.Errorf("entities = %v, want %v", names(found), want)
	}
	for _, entity := range found.Entities {
		if entity.Kind != nounlink.Mention {
			t.Errorf("entity %q is of kind %q, want %q", entity.Name, entity.Kind, nounlink.Mention)
		}
	}
}

// Punctuation between two capitals says they are two names. Without that the
// list would come back as one entity nobody wrote.
func TestPunctuationSeparatesTwoNames(t *testing.T) {
	found := read(t, "The stack runs Postgres, RabbitMQ and MinIO.")

	if want := []string{"Postgres", "RabbitMQ", "MinIO"}; !slices.Equal(names(found), want) {
		t.Errorf("entities = %v, want %v", names(found), want)
	}
}

// A sentence's first word is capitalised because a sentence began. The word
// that only ever opens sentences is dropped, and the name that happens to open
// one is kept.
func TestASentenceOpenerIsNotAName(t *testing.T) {
	found := read(t, "The pumps had not run. Nerve indexes them anyway.")

	if want := []string{"Nerve"}; !slices.Equal(names(found), want) {
		t.Errorf("entities = %v, want %v", names(found), want)
	}
}

// A name said twice is one entity. An extraction is what a document says, not
// a count of how often it says it.
func TestANameSaidTwiceIsOneEntity(t *testing.T) {
	found := read(t, "Nerve mounts Postgres. Nerve also mounts MinIO.")

	if want := []string{"Nerve", "Postgres", "MinIO"}; !slices.Equal(names(found), want) {
		t.Errorf("entities = %v, want %v", names(found), want)
	}
}

// A phrase standing between two names is the claim the sentence made about
// them, and it is directed: the sentence said one thing uses the other, not
// that they use each other.
func TestAPhraseBetweenTwoNamesIsARelation(t *testing.T) {
	found := read(t, "Nerve uses Postgres.")

	want := llm.Relation{
		From:      llm.Entity{Kind: nounlink.Mention, Name: "Nerve"},
		Predicate: "uses",
		To:        llm.Entity{Kind: nounlink.Mention, Name: "Postgres"},
	}
	if len(found.Relations) != 1 || found.Relations[0] != want {
		t.Errorf("relations = %+v, want %+v", found.Relations, want)
	}
}

// An article between the phrase and the name is not part of the claim, so
// every predicate is spelled once rather than once with an article and once
// without.
func TestAnArticleIsNotPartOfTheClaim(t *testing.T) {
	with, without := read(t, "Nerve uses a Postgres."), read(t, "Nerve uses Postgres.")

	if !slices.Equal(with.Relations, without.Relations) {
		t.Errorf("relations = %+v, want the same as %+v", with.Relations, without.Relations)
	}
}

// Every entity a relation names is in the entities too, so a caller builds its
// nodes from one field and its edges from the other without reconciling them.
func TestARelationsEndsAreEntities(t *testing.T) {
	found := read(t, "Nerve uses Postgres.")

	for _, relation := range found.Relations {
		if !slices.Contains(found.Entities, relation.From) || !slices.Contains(found.Entities, relation.To) {
			t.Errorf("relation %+v names something that is not in %v", relation, found.Entities)
		}
	}
}

// A name is read inside one sentence, and a heading is a sentence that never
// finishes. Without that the first word of what follows a title is read as part
// of the title.
func TestAHeadingEndsTheNameInIt(t *testing.T) {
	found := read(t, "Nerve Architecture\n\nPostgres holds the rows.")

	if want := []string{"Nerve Architecture", "Postgres"}; !slices.Equal(names(found), want) {
		t.Errorf("entities = %v, want %v", names(found), want)
	}
}

// A relation is read inside one sentence. Two names the document wrote either
// side of a break are two things it named in a row, and an edge between them
// would be an assertion nobody made.
func TestARelationIsNotReadAcrossASentence(t *testing.T) {
	found := read(t, "Nerve uses\n\nPostgres holds the rows.")

	if len(found.Entities) != 2 {
		t.Fatalf("entities = %v, want both names", names(found))
	}
	if len(found.Relations) != 0 {
		t.Errorf("relations = %+v, want none: the claim spans a break", found.Relations)
	}
}

// Words between two names that assert nothing this build knows leave the two
// names unrelated. A predicate invented from whatever stood there would be an
// edge a caller has to distrust.
func TestAnUnknownPhraseAssertsNothing(t *testing.T) {
	found := read(t, "Nerve wandered past Postgres.")

	if len(found.Entities) != 2 {
		t.Fatalf("entities = %v, want both names", names(found))
	}
	if len(found.Relations) != 0 {
		t.Errorf("relations = %+v, want none", found.Relations)
	}
}

// A text that names nothing extracts to nothing, which is an answer: most of a
// document is prose about things it named elsewhere.
func TestATextThatNamesNothingExtractsNothing(t *testing.T) {
	found := read(t, "the pumps had not run since march.")

	if len(found.Entities) != 0 || len(found.Relations) != 0 {
		t.Errorf("extraction = %+v, want nothing", found)
	}
}

// A possessive is the name with something said about it, so the graph gets one
// node rather than two spellings of one.
func TestAPossessiveIsTheName(t *testing.T) {
	found := read(t, "Nerve mounts Postgres. Postgres's rows are the record.")

	if want := []string{"Nerve", "Postgres"}; !slices.Equal(names(found), want) {
		t.Errorf("entities = %v, want %v", names(found), want)
	}
}

// The provider serves entity extraction and nothing else, so a chain asked for
// a summary or a vector passes over this endpoint.
func TestTheProviderServesEntityExtractionAlone(t *testing.T) {
	provider, err := nounlink.Factory(nil)
	if err != nil {
		t.Fatalf("Factory(nil) = %v", err)
	}

	if provider.AsEntityExtractor() == nil {
		t.Error("AsEntityExtractor = nil, want the extractor")
	}
	if verb := provider.AsSummariser(); verb != nil {
		t.Errorf("AsSummariser = %v, want nil", verb)
	}
	if verb := provider.AsEmbedder(); verb != nil {
		t.Errorf("AsEmbedder = %v, want nil", verb)
	}
	if verb := provider.AsFactExtractor(); verb != nil {
		t.Errorf("AsFactExtractor = %v, want nil", verb)
	}
}
