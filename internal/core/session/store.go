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
	scoped, dir, err := st.dir(s, name)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if err := validMeta(meta); err != nil {
		return fmt.Errorf("create session %q: %w", name, err)
	}
	if _, err := scoped.Stat(ctx, path.Join(dir, recordName)); err == nil {
		return fmt.Errorf("create session %q: %w", name, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("create session %q: %w", name, err)
	}
	if err := scoped.Mkdir(ctx, dir); err != nil {
		return fmt.Errorf("create session %q: %w", name, err)
	}
	if err := writeRecord(ctx, scoped, dir, record{Meta: meta, Created: st.now().UTC()}); err != nil {
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
	scoped, err := st.held(s)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	children, err := scoped.ReadDir(ctx, "/")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	sessions := make([]Session, 0, len(children))
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		held, err := readRecord(ctx, scoped, path.Join("/", child.Name))
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
//
// What has been archived is not in the messages. It is what the session's
// summary stands for, and Archived is the verb that reads it back.
func (st *Store) Get(ctx context.Context, s workspace.Scope, name string) (Transcript, error) {
	scoped, dir, err := st.dir(s, name)
	if err != nil {
		return Transcript{}, fmt.Errorf("get session: %w", err)
	}
	held, seqs, err := readDir(ctx, scoped, dir)
	if err != nil {
		return Transcript{}, fmt.Errorf("get session %q: %w", name, err)
	}

	messages, err := messagesIn(ctx, scoped, dir, seqs)
	if err != nil {
		return Transcript{}, fmt.Errorf("get session %q: %w", name, err)
	}
	return Transcript{Session: sessionOf(name, held), Messages: messages}, nil
}

// Delete removes the session named name in s, record and transcript together. A
// session that is not there is not an error, so a delete that failed halfway is
// completed by running it again.
func (st *Store) Delete(ctx context.Context, s workspace.Scope, name string) error {
	scoped, dir, err := st.dir(s, name)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if err := scoped.RemoveAll(ctx, dir); err != nil {
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
//
// The next sequence continues past the archive as well as past the transcript.
// A session whose every message has been moved aside has nothing left to count
// from, and numbering the next turn one again would put two different messages
// under one sequence.
func (st *Store) Append(ctx context.Context, s workspace.Scope, name, role, body string) error {
	scoped, dir, err := st.dir(s, name)
	if err != nil {
		return fmt.Errorf("append to session: %w", err)
	}
	if err := validRole(role); err != nil {
		return fmt.Errorf("append to session %q: %w", name, err)
	}
	held, seqs, err := readDir(ctx, scoped, dir)
	if err != nil {
		return fmt.Errorf("append to session %q: %w", name, err)
	}
	next := st.nextSeq(held, seqs)
	data, err := encodeMessage(message{Role: role, Body: body, At: st.now().UTC()})
	if err != nil {
		return fmt.Errorf("append to session %q: %w", name, err)
	}
	if err := scoped.Write(ctx, path.Join(dir, messageName(next)), data); err != nil {
		return fmt.Errorf("append to session %q: %w", name, err)
	}
	return nil
}

// Archive moves every message in the session named name in s up to and
// including through into the session's archive, and retains summary on the
// session in their place. It is what happens to a conversation that has gone
// cold: the transcript a caller reads becomes the part still in the
// conversation, and what the rest of it said is the summary.
//
// Nothing is deleted. The archived messages keep the sequences they were said
// under, and Archived reads them back.
//
// A through that names a turn the session never held is refused with
// fs.ErrInvalid, and so is one below what is already archived: the summary
// stands for everything in the archive, and one written for less of it is a
// claim about a conversation nobody can see.
//
// The record is written before the messages move. A caller interrupted the
// other way round leaves messages under sequences the record does not know are
// taken, and the next append would hand one of them out twice; interrupted this
// way it leaves cold messages still in the transcript, which running the same
// archival again completes.
func (st *Store) Archive(ctx context.Context, s workspace.Scope, name string, through int, summary string) error {
	scoped, dir, err := st.dir(s, name)
	if err != nil {
		return fmt.Errorf("archive session: %w", err)
	}
	if err := validArchive(through, summary); err != nil {
		return fmt.Errorf("archive session %q: %w", name, err)
	}
	held, seqs, err := readDir(ctx, scoped, dir)
	if err != nil {
		return fmt.Errorf("archive session %q: %w", name, err)
	}
	if last := st.nextSeq(held, seqs) - 1; through > last {
		return fmt.Errorf("archive session %q through %d, which was never said: %w", name, through, fs.ErrInvalid)
	}
	if through < held.Archived {
		return fmt.Errorf("archive session %q through %d, below the %d already archived: %w", name, through, held.Archived, fs.ErrInvalid)
	}

	held.Summary, held.Archived = summary, through
	if err := writeRecord(ctx, scoped, dir, held); err != nil {
		return fmt.Errorf("archive session %q: %w", name, err)
	}
	archive := path.Join(dir, archiveName)
	if err := scoped.Mkdir(ctx, archive); err != nil {
		return fmt.Errorf("archive session %q: %w", name, err)
	}
	for _, seq := range seqs {
		if seq > through {
			break
		}
		from := path.Join(dir, messageName(seq))
		if err := scoped.Rename(ctx, from, path.Join(archive, messageName(seq))); err != nil {
			return fmt.Errorf("archive session %q message %d: %w", name, seq, err)
		}
	}
	return nil
}

// Archived returns the messages moved aside from the session named name in s,
// in the order they were said. A session nobody has archived from has none,
// which is not an error. A session nobody created reports fs.ErrNotExist.
//
// This is what makes archiving a move rather than a deletion: the cold half of
// a conversation is out of the way of every caller that only wants the live
// one, and still there for the caller that asks for it.
func (st *Store) Archived(ctx context.Context, s workspace.Scope, name string) ([]Message, error) {
	scoped, dir, err := st.dir(s, name)
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	if _, err := readRecord(ctx, scoped, dir); err != nil {
		return nil, fmt.Errorf("read archive of session %q: %w", name, err)
	}
	archive := path.Join(dir, archiveName)
	seqs, err := seqsIn(ctx, scoped, archive)
	if err != nil {
		return nil, fmt.Errorf("read archive of session %q: %w", name, err)
	}
	messages, err := messagesIn(ctx, scoped, archive, seqs)
	if err != nil {
		return nil, fmt.Errorf("read archive of session %q: %w", name, err)
	}
	return messages, nil
}

// readRecord returns the record standing in dir. A directory with no record in
// it reports fs.ErrNotExist, which is what "no session was created here" is.
func readRecord(ctx context.Context, fsys vfs.FileSystem, dir string) (record, error) {
	data, err := fsys.Read(ctx, path.Join(dir, recordName))
	if err != nil {
		return record{}, err
	}
	return decodeRecord(data)
}

// writeRecord puts held in dir as the session's record. It is the one write
// this package makes to a file it has already written, and it touches nothing
// in the transcript: the record is its own file precisely so that retaining a
// summary does not rewrite a single message.
func writeRecord(ctx context.Context, fsys vfs.FileSystem, dir string, held record) error {
	data, err := encodeRecord(held)
	if err != nil {
		return err
	}
	return fsys.Write(ctx, path.Join(dir, recordName), data)
}

// readDir returns the record standing in dir and the sequence of every message
// beside it, ascending, in one listing rather than one call per question.
func readDir(ctx context.Context, fsys vfs.FileSystem, dir string) (record, []int, error) {
	held, err := readRecord(ctx, fsys, dir)
	if err != nil {
		return record{}, nil, err
	}
	seqs, err := seqsIn(ctx, fsys, dir)
	if err != nil {
		return record{}, nil, err
	}
	return held, seqs, nil
}

// seqsIn returns the sequence of every message directly in dir, ascending. A
// directory nothing has been written into holds none, which is what an archive
// nobody has moved anything into looks like.
//
// The sequences come from the names on the mount rather than from a count the
// record keeps, so a session whose caller was interrupted still says what it
// holds. Anything in the directory that is not a message — the record, the
// archive, and whatever else a mount put there — is passed over.
func seqsIn(ctx context.Context, fsys vfs.FileSystem, dir string) ([]int, error) {
	children, err := fsys.ReadDir(ctx, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seqs := make([]int, 0, len(children))
	for _, child := range children {
		if seq, ok := messageSeq(child.Name); ok {
			seqs = append(seqs, seq)
		}
	}
	slices.Sort(seqs)
	return seqs, nil
}

// messagesIn reads the message at each of seqs out of dir, in the order given.
func messagesIn(ctx context.Context, fsys vfs.FileSystem, dir string, seqs []int) ([]Message, error) {
	messages := make([]Message, 0, len(seqs))
	for _, seq := range seqs {
		data, err := fsys.Read(ctx, path.Join(dir, messageName(seq)))
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", seq, err)
		}
		said, err := decodeMessage(data)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", seq, err)
		}
		messages = append(messages, Message{Seq: seq, Role: said.Role, Body: said.Body, At: said.At})
	}
	return messages, nil
}

// nextSeq is the sequence the session's next message takes, counting past the
// archive as well as past the messages still in the transcript. Subtracting one
// from it is the highest sequence the session has ever handed out.
func (st *Store) nextSeq(held record, seqs []int) int {
	last := held.Archived
	if len(seqs) > 0 {
		last = max(last, seqs[len(seqs)-1])
	}
	return last + 1
}

// held is the store as a caller inside s sees it: the sessions s keeps are
// names in it rather than a path derived down to them.
//
// The view is what makes the scope a boundary rather than a prefix: a name is
// resolved by the subtree the tree beneath hands out, so a link standing at any
// component of the scope path is refused instead of followed. A path derived
// down to the session and handed to the whole area is resolved by the whole
// area, which follows that link.
//
// User scope is refused here and so everywhere below it. A scope with no
// workspace derives the path holding every workspace that user has, so a
// session created there would stand where a workspace belongs and be listed as
// one.
func (st *Store) held(s workspace.Scope) (vfs.FileSystem, error) {
	if s.Workspace == "" {
		return nil, fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.View(st.fsys, st.root)
}

// dir is the store as a caller inside s sees it, and where the session named
// name stands in it.
func (st *Store) dir(s workspace.Scope, name string) (vfs.FileSystem, string, error) {
	if err := vfs.ValidSegment("session name", name); err != nil {
		return nil, "", err
	}
	scoped, err := st.held(s)
	if err != nil {
		return nil, "", err
	}
	return scoped, path.Join("/", name), nil
}

// sessionOf names a record, which is the one thing a record does not carry.
func sessionOf(name string, held record) Session {
	return Session{Name: name, Meta: held.Meta, Created: held.Created, Summary: held.Summary, Archived: held.Archived}
}
