package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Store is the conversations of every workspace on the tree under root,
// reached by the session name the caller gives inside its own scope.
type Store struct {
	fsys vfs.FileSystem
	root string
	now  func() time.Time
}

// NewStore returns the store keeping sessions under root on fsys, stamping each
// session and message with now.
//
// root must be a rooted, clean path such as "/sessions" — the mount table's
// rule for a prefix, because a store is where a mount was put.
func NewStore(fsys vfs.FileSystem, root string, now func() time.Time) (*Store, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("session root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	return &Store{fsys: fsys, root: root, now: now}, nil
}

// Create opens the session named name in s carrying meta.
//
// A name already taken is refused with fs.ErrExist rather than reopened. Two
// conversations sharing a session would interleave their turns into one
// transcript, and neither of them is the conversation anybody had.
func (st *Store) Create(ctx context.Context, s workspace.Scope, name string, meta Meta) error {
	dir, err := st.dir(s, name)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if err := validMeta(meta); err != nil {
		return fmt.Errorf("create session %q: %w", name, err)
	}
	if _, err := st.fsys.Stat(ctx, path.Join(dir, recordName)); err == nil {
		return fmt.Errorf("create session %q: %w", name, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("create session %q: %w", name, err)
	}
	data, err := encodeRecord(record{Meta: meta, Created: st.now().UTC()})
	if err != nil {
		return fmt.Errorf("create session %q: %w", name, err)
	}
	if err := st.fsys.Mkdir(ctx, dir); err != nil {
		return fmt.Errorf("create session %q: %w", name, err)
	}
	if err := st.fsys.Write(ctx, path.Join(dir, recordName), data); err != nil {
		return fmt.Errorf("create session %q: %w", name, err)
	}
	return nil
}

// List returns every session in s, sorted by name, without their transcripts.
// It is the cheap question — which conversations are there — asked without
// reading every one of them to answer it.
//
// A child that is not a directory, or is one holding no record, is not a
// session and is passed over: the tree under a mount is a tree, and something
// else standing in it is not a broken session. Both filters are needed and
// neither subsumes the other — a plain file where a session would be reports
// fs.ErrNotExist on a keyspace, where a name is a prefix, and fs.ErrInvalid on
// a tree, where a file has nothing under it.
//
// A record that will not decode is a broken session, and fails the whole call:
// a short list is indistinguishable from a correct one, and the caller is about
// to conclude something about a conversation it was not told about.
func (st *Store) List(ctx context.Context, s workspace.Scope) ([]Session, error) {
	base, err := st.base(s)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	children, err := st.fsys.ReadDir(ctx, base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list sessions %q: %w", base, err)
	}

	sessions := make([]Session, 0, len(children))
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		held, err := st.readRecord(ctx, path.Join(base, child.Name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list session %q: %w", child.Name, err)
		}
		sessions = append(sessions, sessionOf(child.Name, held))
	}
	slices.SortFunc(sessions, func(a, b Session) int { return strings.Compare(a.Name, b.Name) })
	return sessions, nil
}

// Get returns the session named name in s together with every message appended
// to it, in the order they were appended. A session nobody created reports
// fs.ErrNotExist.
//
// The session comes back with the transcript rather than beside it, because a
// caller holding a transcript alone cannot say whose conversation it is, and
// everything this package feeds derives a claim about a user from it.
func (st *Store) Get(ctx context.Context, s workspace.Scope, name string) (Transcript, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return Transcript{}, fmt.Errorf("get session: %w", err)
	}
	held, seqs, err := st.readDir(ctx, dir)
	if err != nil {
		return Transcript{}, fmt.Errorf("get session %q: %w", name, err)
	}

	digest, err := st.readDigest(ctx, dir)
	if err != nil {
		return Transcript{}, fmt.Errorf("get session %q: %w", name, err)
	}

	messages := make([]Message, 0, len(seqs))
	for _, seq := range seqs {
		data, err := st.fsys.Read(ctx, path.Join(dir, messageName(seq)))
		if err != nil {
			return Transcript{}, fmt.Errorf("get session %q message %d: %w", name, seq, err)
		}
		said, err := decodeMessage(data)
		if err != nil {
			return Transcript{}, fmt.Errorf("get session %q message %d: %w", name, seq, err)
		}
		messages = append(messages, Message{Seq: seq, Role: said.Role, Body: said.Body, At: said.At})
	}
	return Transcript{Session: sessionOf(name, held), Digest: digest, Messages: messages}, nil
}

// Compress records that everything said in the session named name in s up to
// and including through has been reduced to summary, and answers with the
// digest it wrote. A session nobody created reports fs.ErrNotExist.
//
// It writes no message and removes none. The transcript is what the summary is
// a claim about, so a verb that reduced the log while reducing it would leave
// the claim standing over something that is no longer there.
//
// A through past the last thing said is refused: a summary cannot cover a turn
// nobody has taken. So is one below the digest already standing, because a
// digest that covers less than it did has lost the half it already reduced, and
// the messages behind it would be read as cold, summarised and hot at once.
//
// Compressing through what is already covered rewrites the digest with the new
// summary. That is the caller's business — it asked — and the stamp is what
// says which of the two is being read.
func (st *Store) Compress(ctx context.Context, s workspace.Scope, name, summary string, through int) (Digest, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return Digest{}, fmt.Errorf("compress session: %w", err)
	}
	if err := validDigest(summary, through); err != nil {
		return Digest{}, fmt.Errorf("compress session %q: %w", name, err)
	}
	_, seqs, err := st.readDir(ctx, dir)
	if err != nil {
		return Digest{}, fmt.Errorf("compress session %q: %w", name, err)
	}
	if len(seqs) == 0 || through > seqs[len(seqs)-1] {
		return Digest{}, fmt.Errorf("compress session %q through message %d, which nobody said: %w", name, through, fs.ErrInvalid)
	}
	standing, err := st.readDigest(ctx, dir)
	if err != nil {
		return Digest{}, fmt.Errorf("compress session %q: %w", name, err)
	}
	if through < standing.Through {
		return Digest{}, fmt.Errorf("compress session %q through message %d, behind the digest through %d: %w", name, through, standing.Through, fs.ErrInvalid)
	}

	digest := Digest{Summary: summary, Through: through, At: st.now().UTC()}
	data, err := encodeDigest(digest)
	if err != nil {
		return Digest{}, fmt.Errorf("compress session %q: %w", name, err)
	}
	if err := st.fsys.Write(ctx, path.Join(dir, digestName), data); err != nil {
		return Digest{}, fmt.Errorf("compress session %q: %w", name, err)
	}
	return digest, nil
}

// readDigest returns the digest standing in dir, and the zero digest when
// nothing has compressed the session. Not being compressed is the ordinary
// state of a conversation, not a session missing a file it should have.
func (st *Store) readDigest(ctx context.Context, dir string) (Digest, error) {
	data, err := st.fsys.Read(ctx, path.Join(dir, digestName))
	if errors.Is(err, fs.ErrNotExist) {
		return Digest{}, nil
	}
	if err != nil {
		return Digest{}, err
	}
	return decodeDigest(data)
}

// Delete removes the session named name in s, record and transcript together. A
// session that is not there is not an error, so a delete that failed halfway is
// completed by running it again.
func (st *Store) Delete(ctx context.Context, s workspace.Scope, name string) error {
	dir, err := st.dir(s, name)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if err := st.fsys.RemoveAll(ctx, dir); err != nil {
		return fmt.Errorf("delete session %q: %w", name, err)
	}
	return nil
}

// Append adds what role said to the session named name in s, as the next
// message. A session nobody created reports fs.ErrNotExist rather than being
// made here: a transcript that appeared because somebody misspelled a name is a
// conversation with no record of who was having it.
//
// Each message is written once, to a path of its own, and nothing already
// written is read back and rewritten. A caller interrupted between two appends
// has lost the message it was making and nothing else.
func (st *Store) Append(ctx context.Context, s workspace.Scope, name, role, body string) error {
	dir, err := st.dir(s, name)
	if err != nil {
		return fmt.Errorf("append to session: %w", err)
	}
	if err := validRole(role); err != nil {
		return fmt.Errorf("append to session %q: %w", name, err)
	}
	_, seqs, err := st.readDir(ctx, dir)
	if err != nil {
		return fmt.Errorf("append to session %q: %w", name, err)
	}
	next := 1
	if len(seqs) > 0 {
		next = seqs[len(seqs)-1] + 1
	}
	data, err := encodeMessage(message{Role: role, Body: body, At: st.now().UTC()})
	if err != nil {
		return fmt.Errorf("append to session %q: %w", name, err)
	}
	if err := st.fsys.Write(ctx, path.Join(dir, messageName(next)), data); err != nil {
		return fmt.Errorf("append to session %q: %w", name, err)
	}
	return nil
}

// readRecord returns the record standing in dir. A directory with no record in
// it reports fs.ErrNotExist, which is what "no session was created here" is.
func (st *Store) readRecord(ctx context.Context, dir string) (record, error) {
	data, err := st.fsys.Read(ctx, path.Join(dir, recordName))
	if err != nil {
		return record{}, err
	}
	return decodeRecord(data)
}

// readDir returns the record standing in dir and the sequence of every message
// beside it, ascending, in one listing rather than one call per question.
//
// The sequences come from the names on the mount rather than from a count the
// record keeps, so a session whose caller was interrupted still says what it
// holds. Anything in the directory that is not a message — the record, and
// whatever else a mount put there — is passed over.
func (st *Store) readDir(ctx context.Context, dir string) (record, []int, error) {
	held, err := st.readRecord(ctx, dir)
	if err != nil {
		return record{}, nil, err
	}
	children, err := st.fsys.ReadDir(ctx, dir)
	if err != nil {
		return record{}, nil, err
	}
	seqs := make([]int, 0, len(children))
	for _, child := range children {
		if seq, ok := messageSeq(child.Name); ok {
			seqs = append(seqs, seq)
		}
	}
	slices.Sort(seqs)
	return held, seqs, nil
}

// base is where s keeps its sessions.
//
// User scope is refused here and so everywhere below it. A scope with no
// workspace derives the path holding every workspace that user has, so a
// session created there would stand where a workspace belongs and be listed as
// one.
func (st *Store) base(s workspace.Scope) (string, error) {
	if s.Workspace == "" {
		return "", fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.Path(st.root, "")
}

// dir is where the session named name stands in s.
func (st *Store) dir(s workspace.Scope, name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	base, err := st.base(s)
	if err != nil {
		return "", err
	}
	return path.Join(base, name), nil
}

// sessionOf names a record, which is the one thing a record does not carry.
func sessionOf(name string, held record) Session {
	return Session{Name: name, Meta: held.Meta, Created: held.Created}
}
