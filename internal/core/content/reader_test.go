package content_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
	"github.com/tunedev/nerve/internal/adapters/outbound/textproc"
	"github.com/tunedev/nerve/internal/core/content"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const (
	resourcesRoot = "/resources"
	ledgerRoot    = "/ingest"
	document      = "transcript.txt"
)

var here = workspace.Scope{User: "ada", Workspace: "hunt"}

// watching is the tree with every whole-file read written down, so a case can
// say what a call actually paid for rather than only what it answered with. An
// escalation that reads a rung it was not asked for is invisible in the return
// value and fails here.
type watching struct {
	vfs.FileSystem
	read []string
}

func (w *watching) Read(ctx context.Context, at string) ([]byte, error) {
	w.read = append(w.read, at)
	return w.FileSystem.Read(ctx, at)
}

// paid is the rungs a call read, by kind, with the two files a resource is made
// of left out: what a caller pays for is bodies, and a resource saying what it
// is costs the same on every rung.
func (w *watching) paid() []string {
	kinds := make([]string, 0, len(w.read))
	for _, at := range w.read {
		if kind := at[strings.LastIndex(at, "/")+1:]; kind != "meta.json" && kind != "content" {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// shelf is a store, the tree under it, and the reader answering for what stands
// there.
type shelf struct {
	resources *resource.Store
	reader    *content.Reader
	tree      *watching
}

func newShelf(t *testing.T, levels ...ingest.Level) *shelf {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(resourcesRoot, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", resourcesRoot, err)
	}
	signer, err := resource.NewSigner([]byte("a key nobody else has"), time.Now)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	tree := &watching{FileSystem: table}
	store, err := resource.NewStore(tree, resourcesRoot, signer)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reader, err := content.NewReader(store, levels...)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return &shelf{resources: store, reader: reader, tree: tree}
}

// upload puts a finalised resource on the shelf, which is a document nothing
// has ingested yet.
func (s *shelf) upload(t *testing.T, name, text string) {
	t.Helper()

	if _, err := s.resources.Create(t.Context(), here, name, "text/plain"); err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	if _, err := s.resources.Upload(t.Context(), here, name, strings.NewReader(text)); err != nil {
		t.Fatalf("Upload(%q): %v", name, err)
	}
	if _, err := s.resources.Complete(t.Context(), here, name); err != nil {
		t.Fatalf("Complete(%q): %v", name, err)
	}
}

// ingest runs the pipeline this build ships over the document, so what the
// reader serves is what the stages actually wrote rather than what a case
// arranged.
func (s *shelf) ingest(t *testing.T, levels ...ingest.Level) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(ledgerRoot, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", ledgerRoot, err)
	}
	ledger, err := ingest.NewLedger(table, ledgerRoot, time.Now)
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	processors := ingest.NewRegistry()
	if err := processors.Register(textproc.New(), textproc.MediaTypes...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	pipeline := ingest.New(s.resources, ledger,
		ingest.Extraction(s.resources, processors),
		ingest.Summarisation(s.resources, termsum.New(), levels...))

	if err := pipeline.Ingest(t.Context(), resourcesRoot+"/ada/hunt/"+document+"/meta.json"); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	s.tree.read = nil
}

// twelve sentences, because a summariser cannot be told from a broken one on
// four: the flood is what the document is about and the office is the aside.
const report = `The office closed on Wednesday and stayed closed for a week.
Nobody had thought to tell the cleaners, who came anyway.
The flood defences depend on the pumps, and the pumps depend on maintenance nobody funded.
Maintenance was moved to the following year in the last budget round.
The pumps were last serviced four years ago.
A pump that has not been serviced fails under the load it was bought for.
The defences held on Tuesday because the river was low.
The river is not low in February.
Funding for maintenance was refused twice.
The second refusal cited the first as precedent.
The pumps are the whole of the flood defences.
Nothing else stands between the river and the town.`

// The acceptance: a caller that found this document is handed L0, and the rungs
// above it cost nothing until it asks for one.
func TestRetrievalIsHandedTheAbstractAndEscalatesOnlyWhereItHasTo(t *testing.T) {
	s := newShelf(t, ingest.Levels...)
	s.upload(t, document, report)
	s.ingest(t, ingest.Levels...)

	abstract, err := s.reader.Abstract(t.Context(), here, document)
	if err != nil {
		t.Fatalf("Abstract: %v", err)
	}
	if abstract == "" {
		t.Fatal("Abstract is empty")
	}
	if got, want := s.tree.paid(), []string{ingest.AbstractSidecar}; !slices.Equal(got, want) {
		t.Errorf("Abstract read %v, want %v", got, want)
	}

	for _, escalation := range []struct {
		kind string
		read func() (string, error)
	}{
		{ingest.OverviewSidecar, func() (string, error) {
			return s.reader.Level(t.Context(), here, document, ingest.OverviewSidecar)
		}},
		{ingest.DetailSidecar, func() (string, error) {
			return s.reader.Level(t.Context(), here, document, ingest.DetailSidecar)
		}},
		{ingest.TextSidecar, func() (string, error) {
			return s.reader.Read(t.Context(), here, document)
		}},
	} {
		s.tree.read = nil
		got, err := escalation.read()
		if err != nil {
			t.Fatalf("%s: %v", escalation.kind, err)
		}
		if !strings.Contains(got, "pumps") {
			t.Errorf("%s = %q, want the document it summarises", escalation.kind, got)
		}
		if paid, want := s.tree.paid(), []string{escalation.kind}; !slices.Equal(paid, want) {
			t.Errorf("%s read %v, want %v", escalation.kind, paid, want)
		}
	}
}

// Stat is what a caller reads to decide whether to escalate at all, so it says
// which rungs stand without paying for any of them.
func TestStatSaysWhichRungsStandWithoutReadingOne(t *testing.T) {
	s := newShelf(t, ingest.Levels...)
	s.upload(t, document, report)
	s.ingest(t, ingest.Levels...)

	stood, err := s.reader.Stat(t.Context(), here, document)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	want := []string{ingest.AbstractSidecar, ingest.OverviewSidecar, ingest.DetailSidecar, ingest.TextSidecar}
	if !slices.Equal(stood.Levels, want) {
		t.Errorf("Levels = %v, want %v", stood.Levels, want)
	}
	if !stood.Resource.Complete || stood.Resource.Name != document {
		t.Errorf("Resource = %+v, want the finalised %q", stood.Resource, document)
	}
	if paid := s.tree.paid(); len(paid) != 0 {
		t.Errorf("Stat read %v, want no rung", paid)
	}
}

// A document nothing has ingested has nothing to escalate to, and says so
// rather than looking like one whose summariser produced nothing.
func TestADocumentNothingIngestedHasNoRungs(t *testing.T) {
	s := newShelf(t, ingest.Levels...)
	s.upload(t, document, report)

	stood, err := s.reader.Stat(t.Context(), here, document)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if len(stood.Levels) != 0 {
		t.Errorf("Levels = %v, want none", stood.Levels)
	}
	if _, err := s.reader.Abstract(t.Context(), here, document); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Abstract = %v, want fs.ErrNotExist", err)
	}
	if _, err := s.reader.Read(t.Context(), here, document); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read = %v, want fs.ErrNotExist", err)
	}
}

// The ladder is what a caller may escalate to and not every file standing
// beside the document. A sidecar something else wrote is not a rung.
func TestASidecarThatIsNotARungIsNotAnEscalation(t *testing.T) {
	s := newShelf(t, ingest.Levels...)
	s.upload(t, document, report)
	if err := s.resources.SetDerived(t.Context(), here, document, "embedding", []byte("0.1 0.2")); err != nil {
		t.Fatalf("SetDerived: %v", err)
	}

	stood, err := s.reader.Stat(t.Context(), here, document)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if len(stood.Levels) != 0 {
		t.Errorf("Levels = %v, want none", stood.Levels)
	}
	for _, kind := range []string{"embedding", "meta.json", "content", "", "l0/abstract"} {
		if _, err := s.reader.Level(t.Context(), here, document, kind); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Level(%q) = %v, want fs.ErrInvalid", kind, err)
		}
	}
}

// The ladder is data here for the reason it is data in the stage that writes
// it: a build with another ladder passes another one, and the cheapest rung is
// whatever stands first in it.
func TestTheCheapestRungIsWhicheverTheLadderStartsWith(t *testing.T) {
	headline := ingest.Level{Kind: "headline", Sentences: 1}
	s := newShelf(t, headline)
	s.upload(t, document, report)
	s.ingest(t, headline)

	got, err := s.reader.Abstract(t.Context(), here, document)
	if err != nil {
		t.Fatalf("Abstract: %v", err)
	}
	if !strings.Contains(got, "pumps") {
		t.Errorf("Abstract = %q, want the headline the ladder named", got)
	}
	if _, err := s.reader.Level(t.Context(), here, document, ingest.OverviewSidecar); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Level(%q) = %v, want fs.ErrInvalid on a ladder without it", ingest.OverviewSidecar, err)
	}
}

// The last rung is the document itself, and it is a grant rather than a read:
// what leaves the process is one file until one moment.
func TestDownloadIsTheRungBelowTheWordsAndIsAGrant(t *testing.T) {
	s := newShelf(t, ingest.Levels...)
	s.upload(t, document, report)

	grant, err := s.reader.Download(t.Context(), here, document, time.Minute)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	redeemed, err := s.resources.Download(t.Context(), grant.Token)
	if err != nil {
		t.Fatalf("redeeming: %v", err)
	}
	defer func() { _ = redeemed.Close() }()

	read, err := io.ReadAll(redeemed)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(read) != report {
		t.Errorf("download = %q, want the document as it was uploaded", read)
	}
}

// A reader with no ladder can serve no escalation, and every call it ever got
// would be refused. That is a startup failure rather than a runtime one.
func TestAReaderWithNoLadderIsRefused(t *testing.T) {
	if _, err := content.NewReader(nil, ingest.Levels...); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewReader(nil) = %v, want fs.ErrInvalid", err)
	}
	table := vfs.NewMountTable()
	if err := table.Mount(resourcesRoot, memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	signer, err := resource.NewSigner([]byte("a key nobody else has"), time.Now)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	store, err := resource.NewStore(table, resourcesRoot, signer)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := content.NewReader(store); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewReader with no levels = %v, want fs.ErrInvalid", err)
	}
}
