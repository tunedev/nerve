package termsum_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
)

// report is a document about one thing, written the way documents are: the
// sentence that says what happened is neither the first nor the longest, and
// the words the whole thing turns on come back over and over.
const report = `The office closed on Wednesday and stayed closed for a week.
It had rained since Tuesday, which nobody thought unusual.
Somebody had left a window open on the second floor.
The flood came through the basement, not the window.
The pumps that keep the basement dry had not run since March.
Maintenance on the pumps was scheduled twice and cancelled twice.
The flood defences depend on the pumps, and the pumps depend on maintenance nobody funded.
A cleaner reported water on the Friday before.
Nobody read the report.
The insurers asked what the pumps had cost to maintain.
The answer was nothing, because the maintenance never happened.
The building reopened once the pumps were replaced.`

func summarise(t *testing.T, text string, sentences int) []string {
	t.Helper()

	got, err := termsum.New().Summarise(t.Context(), text, sentences)
	if err != nil {
		t.Fatalf("Summarise(%d): %v", sentences, err)
	}
	if got == "" {
		return nil
	}
	return strings.Split(got, "\n")
}

// The acceptance: the abstract is what the document is about, which is not the
// same as what it opens with. A summariser that took the first sentence would
// answer with the office closing and never mention the pumps.
func TestTheAbstractIsTheSentenceTheDocumentIsAbout(t *testing.T) {
	got := summarise(t, report, 1)

	want := "The flood defences depend on the pumps, and the pumps depend on maintenance nobody funded."
	if len(got) != 1 || got[0] != want {
		t.Errorf("abstract = %q, want %q", got, want)
	}
}

// Escalating a level only adds. What the overview says the abstract said too,
// which is what makes the ladder an escalation rather than three opinions.
func TestEscalatingALevelOnlyAdds(t *testing.T) {
	abstract := summarise(t, report, 1)
	overview := summarise(t, report, 5)

	if len(overview) != 5 {
		t.Fatalf("overview = %d sentences, want 5", len(overview))
	}
	for _, sentence := range abstract {
		if !slices.Contains(overview, sentence) {
			t.Errorf("overview dropped %q, which the abstract said", sentence)
		}
	}
}

// A summary is the document's own sentences, in the order the document had
// them: this reader picks, it does not write.
func TestASummaryIsTheDocumentsOwnSentencesInOrder(t *testing.T) {
	got := summarise(t, report, 4)

	at := -1
	for _, sentence := range got {
		found := strings.Index(report, sentence)
		if found < 0 {
			t.Fatalf("summary says %q, which the document does not", sentence)
		}
		if found <= at {
			t.Errorf("summary has %q out of the order the document had it", sentence)
		}
		at = found
	}
}

// A document with less to say than the rung allows says less. Its three levels
// are the same words, and every one of them is still there.
func TestADocumentShorterThanTheBudgetIsItsOwnSummary(t *testing.T) {
	short := "The pumps failed. Nobody had maintained them."

	got := summarise(t, short, 20)

	if want := []string{"The pumps failed.", "Nobody had maintained them."}; !slices.Equal(got, want) {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// A document made of headings and lines that never finish a sentence is still
// made of sentences: a line that ends without a full stop ends where the blank
// line after it does.
func TestALineThatNeverFinishesIsStillASentence(t *testing.T) {
	notes := "# Pumps\n\nThe pumps failed\n\nMaintenance was cancelled"

	got := summarise(t, notes, 5)

	if want := []string{"# Pumps", "The pumps failed", "Maintenance was cancelled"}; !slices.Equal(got, want) {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// A document with no words has no summary, and neither has a rung that asked
// for no sentences. Both are empty rather than an error: nothing about either
// is a failure of the run.
func TestNothingToSayIsAnEmptySummary(t *testing.T) {
	if got := summarise(t, "   \n\n  ", 5); got != nil {
		t.Errorf("summary of nothing = %q, want none", got)
	}
	if got := summarise(t, report, 0); got != nil {
		t.Errorf("summary of no sentences = %q, want none", got)
	}
}

// The summariser reaches a caller as a model endpoint, so the one path a
// summary takes does not depend on whether a model wrote it.
func TestTheProviderServesSummarisation(t *testing.T) {
	provider, err := termsum.Factory(nil)
	if err != nil {
		t.Fatalf("Factory = %v", err)
	}

	summariser := provider.AsSummariser()
	if summariser == nil {
		t.Fatal("AsSummariser = nil, want the summariser")
	}

	answer, err := summariser.Summarise(context.Background(), "One sentence. Another sentence.", 1)
	if err != nil || answer == "" {
		t.Errorf("Summarise = %q, %v, want a sentence", answer, err)
	}
}
