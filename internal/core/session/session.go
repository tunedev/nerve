// Package session is a workspace's record of a conversation: what the caller
// said about it when it opened, and everything said in it since.
//
// A session is two things that change at different rates. Its metadata is
// key/value state fixed when the session is created — the model, the agent, the
// title a caller wants to find it by — and its transcript is a log that only
// ever grows. Keeping them apart is what lets a caller ask which sessions exist
// without reading a conversation's worth of messages to find out.
//
// The log is append-only and the package offers no verb that is not. There is
// no way to rewrite a message or to drop one, because everything downstream of
// a session — the facts extracted from it, the summary that replaces its cold
// half — is derived from the transcript, and a transcript that can be edited
// underneath a derivation makes the derivation a claim about nothing.
//
// A session's digest is the one thing beside the log that is written more than
// once, and it is not part of the log: it is a summary of the cold half, held
// in a file of its own, refreshed as the conversation grows and never covering
// less than it did. Compressing a session leaves every message where it was
// said. What a reduced transcript is, is the digest plus the messages it does
// not reach.
//
// Reading a session answers with its metadata and its messages in one value,
// and never with the messages alone. A caller holding messages with no idea
// which session they came from is one mix-up away from attributing a user's
// conversation to somebody else.
//
// The store keeps nothing itself. It reads and writes the one FileSystem the
// mount table presents, at paths derived from the scope that owns them, so
// whether a transcript actually sits on a disk, in a keyspace or in an object
// store is a mount rather than a decision made here. Both shapes work
// unchanged: a session is a directory on a tree and a prefix in a keyspace, and
// nothing below depends on which.
package session

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"
)

// Meta is the key/value state a session carries. The keys are the caller's
// vocabulary and the store reads none of them: what a session is about is the
// caller's business, and a store that understood one key would be a store that
// has to be changed to understand a second.
type Meta map[string]string

// Session is what the store says about a conversation before anyone reads it:
// enough to list it, find it and know when it started, and not what was said.
type Session struct {
	// Name is the session's directory, one flat segment inside the scope.
	Name string
	// Meta is what the caller said about the session when it was created.
	Meta Meta
	// Created is when it was created.
	Created time.Time
}

// Message is one thing said in a session, with its position in the
// conversation. Sequence numbers start at one and have no gaps: the store
// assigns them, and a caller uses them to say how much of a transcript it has
// already seen.
type Message struct {
	// Seq is the position, counting from one.
	Seq int
	// Role is who said it, in the caller's vocabulary.
	Role string
	// Body is what was said.
	Body string
	// At is when it was appended.
	At time.Time
}

// Transcript is a session, what it has been reduced to, and everything said in
// it. It is what Get answers with, and the fields arrive together on purpose —
// see the package comment.
//
// Messages is the whole conversation whether or not it has been compressed.
// Compression writes a summary, it does not remove a turn, so a caller that
// wants the record still has it.
type Transcript struct {
	Session  Session
	Digest   Digest
	Messages []Message
}

// Hot returns the messages the digest does not cover: the tail of the
// conversation still held as it was said.
//
// With the digest and Hot together a caller has the reduced transcript — the
// cold half as a summary, the recent half verbatim — without reading what the
// summary already stands for. A session nothing has compressed is hot
// throughout, which is the truthful answer: nothing about it has been reduced.
func (t Transcript) Hot() []Message {
	for i, message := range t.Messages {
		if message.Seq > t.Digest.Through {
			return t.Messages[i:]
		}
	}
	return nil
}

// Digest is what a session's cold transcript was reduced to: a summary of
// everything said up to and including Through, and when it was made.
//
// The zero Digest is a session nobody has compressed. Sequences count from one,
// so a Through of zero covers no message, which keeps "not compressed" and
// "compressed through the first turn" different answers.
type Digest struct {
	// Summary is what the cold half says.
	Summary string `json:"summary"`
	// Through is the last message sequence the summary covers.
	Through int `json:"through"`
	// At is when it was written.
	At time.Time `json:"at"`
}

// The record a session is written in, beside the messages rather than in one of
// them: metadata is written once at creation and messages arrive forever after,
// so a file holding both would be rewritten by every append.
const recordName = "SESSION"

// The digest a compressed session is written in, beside the record and the
// messages rather than inside either. It is derived from the transcript and is
// rewritten whenever the cold half grows, which is the one thing the log itself
// never is.
const digestName = "DIGEST"

// record is a Session as a file holds it. The name is not in it, because the
// name is the directory's own: a record carrying one would be a second answer
// to which session it is.
type record struct {
	Meta    Meta      `json:"meta,omitempty"`
	Created time.Time `json:"created"`
}

// message is a Message as a file holds it. The sequence is not in it, because
// the sequence is the file's own name.
type message struct {
	Role string    `json:"role"`
	Body string    `json:"body"`
	At   time.Time `json:"at"`
}

// encodeRecord renders a session's record as the bytes of its file.
func encodeRecord(held record) ([]byte, error) {
	return json.Marshal(held)
}

// decodeRecord reads a file back into the record it holds.
//
// Everything it refuses, it refuses with fs.ErrInvalid: a file standing where a
// record belongs and not being one is a file someone has to fix, not a session
// in an unknown state.
func decodeRecord(data []byte) (record, error) {
	var held record
	if err := json.Unmarshal(data, &held); err != nil {
		return record{}, fmt.Errorf("session record does not decode: %w: %w", err, fs.ErrInvalid)
	}
	return held, nil
}

// encodeMessage renders a message as the bytes of its file.
func encodeMessage(held message) ([]byte, error) {
	return json.Marshal(held)
}

// decodeMessage reads a file back into the message it holds, refusing anything
// that is not one with fs.ErrInvalid. A message that would not decode fails the
// read of the whole transcript rather than being passed over: a transcript
// missing a turn reads as a conversation that was never had.
func decodeMessage(data []byte) (message, error) {
	var held message
	if err := json.Unmarshal(data, &held); err != nil {
		return message{}, fmt.Errorf("message does not decode: %w: %w", err, fs.ErrInvalid)
	}
	if held.Role == "" {
		return message{}, fmt.Errorf("message names no role: %w", fs.ErrInvalid)
	}
	return held, nil
}

// encodeDigest renders a digest as the bytes of its file.
func encodeDigest(held Digest) ([]byte, error) {
	return json.Marshal(held)
}

// decodeDigest reads a file back into the digest it holds, refusing anything
// that is not one with fs.ErrInvalid. A digest that would not decode fails the
// read of the session rather than being passed over as an uncompressed one: a
// caller told a compressed session has no digest reads the cold half twice,
// once as messages and once as nothing.
func decodeDigest(data []byte) (Digest, error) {
	var held Digest
	if err := json.Unmarshal(data, &held); err != nil {
		return Digest{}, fmt.Errorf("session digest does not decode: %w: %w", err, fs.ErrInvalid)
	}
	if err := validDigest(held.Summary, held.Through); err != nil {
		return Digest{}, err
	}
	return held, nil
}

// validDigest returns fs.ErrInvalid for a digest that is not a reduction. A
// digest with no summary marks a cold half reduced to nothing, and one covering
// no message marks a session compressed that was not.
func validDigest(summary string, through int) error {
	switch {
	case summary == "":
		return fmt.Errorf("session digest has no summary: %w", fs.ErrInvalid)
	case through < 1:
		return fmt.Errorf("session digest covers through message %d, which is no message: %w", through, fs.ErrInvalid)
	}
	return nil
}

// validName returns fs.ErrInvalid for a name no session can have. The rule is
// the scope's flat-segment rule for the same reason: a name carrying a
// separator names a session inside another one, and "." or ".." is a spelling
// that resolves away and lands on a conversation nobody had.
func validName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("session name is empty: %w", fs.ErrInvalid)
	case strings.Contains(name, "/"):
		return fmt.Errorf("session name %q contains a separator: %w", name, fs.ErrInvalid)
	case name == "." || name == "..":
		return fmt.Errorf("session name %q names a directory, not a session: %w", name, fs.ErrInvalid)
	}
	return nil
}

// validMeta returns fs.ErrInvalid for metadata a session cannot carry. A key
// has to be a name for the value to be reachable by; an empty one is state
// written where nothing can ask for it.
func validMeta(meta Meta) error {
	if _, empty := meta[""]; empty {
		return fmt.Errorf("session metadata has an empty key: %w", fs.ErrInvalid)
	}
	return nil
}

// validRole returns fs.ErrInvalid for a message with nobody saying it. What is
// extracted from a transcript turns on who said what, and a turn with no
// speaker is one the extraction has to guess at.
func validRole(role string) error {
	if role == "" {
		return fmt.Errorf("message names no role: %w", fs.ErrInvalid)
	}
	return nil
}

// messageName is what the message at seq is called inside a session. It is the
// sequence written plainly: messages are ordered by the number a name decodes
// to and never by the order the names sort in, so there is nothing for a padded
// name to buy.
func messageName(seq int) string {
	return strconv.Itoa(seq)
}

// messageSeq reads a name back into the sequence it stands for, and reports
// whether the name is a message at all. The record shares the directory with
// the messages, and so may anything else a mount put there.
//
// A name that does not round-trip is not a message, which is how a spelling
// like "01" or "+1" that decodes to a sequence already taken is refused rather
// than counted twice.
func messageSeq(name string) (int, bool) {
	seq, err := strconv.Atoi(name)
	if err != nil || seq < 1 || messageName(seq) != name {
		return 0, false
	}
	return seq, true
}
