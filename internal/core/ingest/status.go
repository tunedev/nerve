package ingest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// Status is where a document's ingestion stands. There are four, and the caller
// cares about one distinction above all: Indexed means the document is
// searchable, and the other three mean it is not, for three different reasons.
type Status string

const (
	// Pending means nothing has ingested this document. It is what a document
	// whose job has not been claimed is, and what a document that was never
	// uploaded is: the ledger records ingestion and knows nothing else.
	Pending Status = "pending"
	// Working means ingestion started and has not said how it ended. It may
	// still be running, or the worker may have died without being able to say
	// so — the ledger cannot tell those apart and does not pretend to.
	Working Status = "working"
	// Indexed means every stage ran, which is the moment the document became
	// searchable.
	Indexed Status = "indexed"
	// Failed means a stage did not, and Stage and Failure say which and why.
	Failed Status = "failed"
)

// Indexing is what the ledger says about one document: where its ingestion
// stands, and when it got there.
type Indexing struct {
	// Name is the resource the ingestion was of, one flat segment inside the
	// scope.
	Name string
	// Status is where the ingestion stands.
	Status Status
	// Stage is the stage that stopped a failed ingestion. It is empty for every
	// other status.
	Stage string
	// Failure is why that stage stopped it. It is empty for every other status.
	Failure string
	// At is when the ingestion reached this status. It is the zero time for a
	// document nothing has ingested, which has reached nothing.
	At time.Time
}

// record is an Indexing as a file holds it. The name is not in it, because the
// name is the file's own: a record carrying one would be a second answer to
// what document it is about.
type record struct {
	Status  Status    `json:"status"`
	Stage   string    `json:"stage,omitempty"`
	Failure string    `json:"failure,omitempty"`
	At      time.Time `json:"at"`
}

// encode renders a record as the bytes of its file.
func encode(held record) ([]byte, error) {
	return json.Marshal(held)
}

// decode reads a file back into the record it holds.
//
// Everything it refuses, it refuses with fs.ErrInvalid, because a file standing
// where a record belongs and not being one is a file someone has to fix rather
// than a document in an unknown state. Pending is refused with everything else:
// it is the absence of a record, and a file spelling it would be a second way
// to say the same thing.
func decode(data []byte) (record, error) {
	var held record
	if err := json.Unmarshal(data, &held); err != nil {
		return record{}, fmt.Errorf("indexing record does not decode: %w: %w", err, fs.ErrInvalid)
	}
	switch held.Status {
	case Working, Indexed, Failed:
		return held, nil
	default:
		return record{}, fmt.Errorf("indexing record names no status: %w", fs.ErrInvalid)
	}
}

// validName returns fs.ErrInvalid for a name no document can have. The rule is
// the scope's flat-segment rule for the same reason: a name carrying a
// separator names a record inside another one, and "." or ".." is a spelling
// that resolves away and lands on a record about a different document.
func validName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("document name is empty: %w", fs.ErrInvalid)
	case strings.Contains(name, "/"):
		return fmt.Errorf("document name %q contains a separator: %w", name, fs.ErrInvalid)
	case name == "." || name == "..":
		return fmt.Errorf("document name %q names a directory: %w", name, fs.ErrInvalid)
	}
	return nil
}
