// Package archive is what happens to a conversation that has gone on too long:
// its older turns are summarised, the summary is retained on the session, and
// the turns themselves are moved aside.
//
// It is the stage that makes a long session cheap to read again. What a caller
// gets back from a session afterwards is the part still being talked about,
// plus one sentence for everything before it, and the archive holds the rest
// for the caller that asks — nothing said is thrown away.
//
// Summarising is a model call and goes through the gateway, so no stage here
// holds a provider client and which endpoint reads a cold transcript is a line
// of config. A build with no summarising endpoint has no archiver, which it
// finds out where it wires the pipeline rather than on the first long
// conversation.
package archive

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/session"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Archiver moves the cold half of a session aside, retaining a summary of it.
type Archiver struct {
	sessions  *session.Store
	summarise llm.Summariser
	sentences int
}

// New returns the archiver reading and writing sessions in sessions,
// summarising through summarise in at most sentences sentences.
//
// A nil summariser is refused. It is what the gateway answers when nothing in
// the chain summarises, and an archiver built on it would accept every session
// and fail on each one, which is a build that looks wired and archives nothing.
func New(sessions *session.Store, summarise llm.Summariser, sentences int) (*Archiver, error) {
	switch {
	case sessions == nil:
		return nil, fmt.Errorf("archiver needs a session store: %w", fs.ErrInvalid)
	case summarise == nil:
		return nil, fmt.Errorf("archiver needs a summariser; no configured endpoint summarises: %w", fs.ErrInvalid)
	case sentences < 1:
		return nil, fmt.Errorf("archiver keeps %d sentences, which retains nothing: %w", sentences, fs.ErrInvalid)
	}
	return &Archiver{sessions: sessions, summarise: summarise, sentences: sentences}, nil
}

// Archive moves every turn of the session named name in s aside except the last
// keep, retaining a summary of what they said, and answers with the session as
// it stands afterwards.
//
// A session with keep turns or fewer in it is already short enough: nothing
// moves, no model is called, and that is not a failure. A conversation is
// archived because it grew, and a caller that archives on a schedule would
// otherwise pay a model call per idle session.
//
// The summary covers the whole archive and not only the turns this call moved,
// because that is what the session retains: the one it already had is
// summarised again alongside the newly cold turns, so a session archived twice
// does not lose its first half to its second.
func (a *Archiver) Archive(ctx context.Context, s workspace.Scope, name string, keep int) (session.Session, error) {
	if keep < 0 {
		return session.Session{}, fmt.Errorf("archive session %q keeping %d turns: %w", name, keep, fs.ErrInvalid)
	}
	transcript, err := a.sessions.Get(ctx, s, name)
	if err != nil {
		return session.Session{}, fmt.Errorf("archive session %q: %w", name, err)
	}
	if keep >= len(transcript.Messages) {
		return transcript.Session, nil
	}
	cold := transcript.Messages[:len(transcript.Messages)-keep]

	summary, err := a.summarise.Summarise(ctx, coldText(transcript.Session.Summary, cold), a.sentences)
	if err != nil {
		return session.Session{}, fmt.Errorf("archive session %q: %w", name, err)
	}
	through := cold[len(cold)-1].Seq
	if err := a.sessions.Archive(ctx, s, name, through, summary); err != nil {
		return session.Session{}, fmt.Errorf("archive session %q: %w", name, err)
	}

	archived := transcript.Session
	archived.Summary, archived.Archived = summary, through
	return archived, nil
}

// coldText renders what the summariser is shown: the summary the session
// already carried, then the turns going cold now, one per line.
//
// The turns arrive as "role: body" because who said something is what a summary
// of a conversation turns on — a claim the user made and one the model made read
// identically once the speakers are dropped.
func coldText(retained string, cold []session.Message) string {
	lines := make([]string, 0, len(cold)+1)
	if retained != "" {
		lines = append(lines, retained)
	}
	for _, m := range cold {
		lines = append(lines, m.Role+": "+m.Body)
	}
	return strings.Join(lines, "\n")
}
