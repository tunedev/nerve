package thread

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// Status is where a run stands. There are three and the reader cares about one
// distinction above all: Running means more output may arrive, and the other
// two mean this is all of it.
type Status string

const (
	// Running means the run has not said how it ended. It may still be working,
	// or it may have died without being able to say so — the log cannot tell
	// those apart and does not pretend to.
	Running Status = "running"
	// Finished means the run ended having produced what it meant to.
	Finished Status = "finished"
	// Failed means the run ended without producing it, and Failure says why.
	Failed Status = "failed"
)

// Run is what a thread says about itself before anyone reads its output:
// enough to know whether there is more coming, and not the output itself.
type Run struct {
	// Name is the thread's directory, one flat segment inside the scope.
	Name string
	// Status is where the run stands.
	Status Status
	// Failure is the one line saying why a failed run did not produce its
	// output. It is empty for every other status.
	Failure string
}

// The file a run is written in holds one line and no more: the status, and for
// a failed run the reason after it. One line because a run declares one thing
// about itself that is not its output, and a format with one field needs no
// parser.
const (
	recordName = "RUN"
	reasonSep  = ": "
)

// encodeRun renders a run as the bytes of its record.
func encodeRun(run Run) []byte {
	if run.Status == Failed {
		return []byte(string(Failed) + reasonSep + run.Failure)
	}
	return []byte(run.Status)
}

// decodeRun reads a record back. The name is the directory the record was found
// in, so what comes back carries only the status and the reason.
//
// Everything it refuses, it refuses with fs.ErrInvalid: a file standing where a
// record belongs and not being one is a file someone has to fix, not a run in
// an unknown state. A record that read back as Running when it could not be
// parsed would tell a reader to wait for output that will never arrive.
func decodeRun(data []byte) (Run, error) {
	line := string(data)
	if strings.Contains(line, "\n") {
		return Run{}, fmt.Errorf("run record is more than one line: %w", fs.ErrInvalid)
	}
	if status, reason, found := strings.Cut(line, reasonSep); found {
		if Status(status) != Failed {
			return Run{}, fmt.Errorf("run record %q carries a reason for status %q: %w", line, status, fs.ErrInvalid)
		}
		return Run{Status: Failed, Failure: reason}, nil
	}
	switch Status(line) {
	case Running, Finished:
		return Run{Status: Status(line)}, nil
	default:
		return Run{}, fmt.Errorf("run record %q names no status: %w", line, fs.ErrInvalid)
	}
}

// validFailure returns fs.ErrInvalid for a reason the record cannot hold. It is
// one line, because a reason carrying a newline would read back as a record
// that is not one.
func validFailure(because string) error {
	switch {
	case because == "":
		return fmt.Errorf("failed run gives no reason: %w", fs.ErrInvalid)
	case strings.Contains(because, "\n"):
		return fmt.Errorf("failure reason is more than one line: %w", fs.ErrInvalid)
	}
	return nil
}

// entryName is what the entry at seq is called inside a thread. It is the
// sequence written plainly: entries are ordered by the number a name decodes to
// and never by the order the names sort in, so there is nothing for a padded
// name to buy.
func entryName(seq int) string {
	return strconv.Itoa(seq)
}

// entrySeq reads a name back into the sequence it stands for, and reports
// whether the name is an entry at all. The record shares the directory with the
// entries, and so may anything else a mount put there.
//
// A name that does not round-trip is not an entry, which is how a spelling like
// "01" or "+1" that decodes to a sequence already taken is refused rather than
// counted twice.
func entrySeq(name string) (int, bool) {
	seq, err := strconv.Atoi(name)
	if err != nil || seq < 1 || entryName(seq) != name {
		return 0, false
	}
	return seq, true
}
