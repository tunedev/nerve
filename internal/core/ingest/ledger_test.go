package ingest_test

import (
	"errors"
	"io/fs"
	"path"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const ledgerRoot = "/ingest"

// recordedAt is the moment the ledger's clock reads, so a time that came back
// is compared against the moment it was written at and not against now.
var recordedAt = time.Date(2026, 8, 31, 9, 30, 0, 0, time.UTC)

func newLedger(t *testing.T) (*ingest.Ledger, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(ledgerRoot, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", ledgerRoot, err)
	}
	ledger, err := ingest.NewLedger(table, ledgerRoot, func() time.Time { return recordedAt })
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	return ledger, table
}

// The acceptance, at the ledger: a document that has been through ingestion
// says so, and says when.
func TestALedgerSaysWhenADocumentBecameSearchable(t *testing.T) {
	ledger, _ := newLedger(t)

	if err := ledger.Indexed(t.Context(), here, "transcript.txt"); err != nil {
		t.Fatalf("Indexed: %v", err)
	}

	got, err := ledger.Get(t.Context(), here, "transcript.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := ingest.Indexing{Name: "transcript.txt", Status: ingest.Indexed, At: recordedAt}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

// A document the ledger has never heard of is one nothing has ingested, which
// is an answer rather than a refusal: "not yet" is what the caller asked.
func TestADocumentNothingHasIngestedIsPending(t *testing.T) {
	ledger, _ := newLedger(t)

	got, err := ledger.Get(t.Context(), here, "transcript.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := ingest.Indexing{Name: "transcript.txt", Status: ingest.Pending}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

// A document that did not become searchable says which stage stopped it and
// why. Either alone leaves the caller guessing at the other.
func TestAFailedIngestionSaysWhichStageStoppedItAndWhy(t *testing.T) {
	ledger, _ := newLedger(t)

	if err := ledger.Failed(t.Context(), here, "transcript.txt", "extract", "no processor for this format"); err != nil {
		t.Fatalf("Failed: %v", err)
	}

	got, err := ledger.Get(t.Context(), here, "transcript.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := ingest.Indexing{
		Name:    "transcript.txt",
		Status:  ingest.Failed,
		Stage:   "extract",
		Failure: "no processor for this format",
		At:      recordedAt,
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

// The latest record is the one that stands. A worker that died mid-run leaves a
// document saying it is working, and the retry that finishes it has to be able
// to say so.
func TestARetryOverwritesWhatTheAttemptBeforeItSaid(t *testing.T) {
	ledger, _ := newLedger(t)

	if err := ledger.Failed(t.Context(), here, "transcript.txt", "extract", "the worker died"); err != nil {
		t.Fatalf("Failed: %v", err)
	}
	if err := ledger.Working(t.Context(), here, "transcript.txt"); err != nil {
		t.Fatalf("Working: %v", err)
	}
	if err := ledger.Indexed(t.Context(), here, "transcript.txt"); err != nil {
		t.Fatalf("Indexed: %v", err)
	}

	got, err := ledger.Get(t.Context(), here, "transcript.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := ingest.Indexing{Name: "transcript.txt", Status: ingest.Indexed, At: recordedAt}
	if got != want {
		t.Errorf("Get = %+v, want %+v, with nothing left of the failure", got, want)
	}
}

// A record standing where one belongs and not being one is a file someone has
// to fix. Reading it back as Pending would tell a caller to wait for an
// ingestion that already happened.
func TestARecordThatWillNotDecodeFailsTheRead(t *testing.T) {
	ledger, table := newLedger(t)

	at, err := here.Path(ledgerRoot, "transcript.txt")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := table.Mkdir(t.Context(), path.Dir(at)); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := table.Write(t.Context(), at, []byte("not a record")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := ledger.Get(t.Context(), here, "transcript.txt"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Get = %v, want fs.ErrInvalid", err)
	}
}

// A name that is not one flat segment names a record inside another one, or one
// outside the scope that owns it.
func TestALedgerRefusesANameThatIsNotOneSegment(t *testing.T) {
	ledger, _ := newLedger(t)

	for _, name := range []string{"", "notes/transcript.txt", ".", ".."} {
		if err := ledger.Indexed(t.Context(), here, name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Indexed(%q) = %v, want fs.ErrInvalid", name, err)
		}
		if _, err := ledger.Get(t.Context(), here, name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Get(%q) = %v, want fs.ErrInvalid", name, err)
		}
	}
}

// User scope derives the path holding every workspace that user has, so a
// record written there would stand where a workspace belongs.
func TestALedgerRefusesAScopeThatNamesNoWorkspace(t *testing.T) {
	ledger, _ := newLedger(t)
	everything := workspace.Scope{User: "ada"}

	if err := ledger.Indexed(t.Context(), everything, "transcript.txt"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Indexed = %v, want fs.ErrInvalid", err)
	}
	if _, err := ledger.Get(t.Context(), everything, "transcript.txt"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Get = %v, want fs.ErrInvalid", err)
	}
}

// A ledger is where a mount was put, and the mount table's rule for a prefix is
// a rooted clean path.
func TestALedgerRefusesARootThatIsNotAMountPrefix(t *testing.T) {
	for _, root := range []string{"ingest", "/ingest/", "/ingest/../ingest"} {
		if _, err := ingest.NewLedger(vfs.NewMountTable(), root, time.Now); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("NewLedger(%q) = %v, want fs.ErrInvalid", root, err)
		}
	}
}
