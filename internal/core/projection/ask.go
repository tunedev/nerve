package projection

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Querier runs one statement over a table an Encoder wrote and answers with the
// rows it returned.
//
// It is the second port E10.3 said a reading engine would need, and it is
// separate from Encoder for the reason the two verbs are separate: a build can
// write its tables with one thing and read them back with another, and neither
// side of that is the core's business. It takes the table as bytes rather than
// as a path because a rollup lives wherever its mount is, which may be a
// keyspace or an object store with no path an engine could open.
//
// Extension is here as well as on Encoder because a querier has to be able to
// find the file it reads without being told what wrote it.
type Querier interface {
	// Extension is what a file this querier reads is called, without the dot.
	Extension() string

	// Query runs statement over table, which holds the encoded bytes of the
	// table called name, and returns the columns it answered with and the rows
	// under them.
	Query(ctx context.Context, name string, table []byte, statement string) (Schema, []Row, error)
}

// Answer is what a question was answered with: the statement that was run, the
// columns that came back and the rows under them.
//
// The statement is part of the answer rather than something the caller has to
// ask for separately. A number a model wrote the query for is worth exactly as
// much as the query, and an answer that hid it would be asking to be trusted.
type Answer struct {
	SQL     string
	Columns Schema
	Rows    []Row
}

// Asker answers a question about a workspace's projection, asked in the words
// whoever asked it used.
//
// It holds no manifest, the way the materialiser holds none: which projections
// a build has is a question about where manifests live, and one asker serves
// every manifest handed to it.
type Asker struct {
	fsys    vfs.FileSystem
	writer  llm.SQLWriter
	querier Querier
	root    string
	timeout time.Duration
}

// NewAsker returns the asker reading the tables under root on fsys, turning
// questions into SQL with writer and running them through querier, giving each
// query timeout to finish in.
//
// The timeout bounds the engine and not the model. A writer reached through the
// gateway is already bounded per provider, and a second bound here would be a
// second layer deciding how long a model may take.
func NewAsker(fsys vfs.FileSystem, writer llm.SQLWriter, querier Querier, root string, timeout time.Duration) (*Asker, error) {
	if fsys == nil {
		return nil, fmt.Errorf("asker has no filesystem: %w", fs.ErrInvalid)
	}
	if writer == nil {
		return nil, fmt.Errorf("asker has nothing to write SQL with: %w", fs.ErrInvalid)
	}
	if querier == nil {
		return nil, fmt.Errorf("asker has no querier: %w", fs.ErrInvalid)
	}
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("projections root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("asker query timeout is %s, which bounds nothing: %w", timeout, fs.ErrInvalid)
	}
	return &Asker{fsys: fsys, writer: writer, querier: querier, root: root, timeout: timeout}, nil
}

// Ask answers question about the rollup of manifest.
//
// The rollup is the whole of what a question is asked over. It is the one table
// per workspace the slices combine into, so a question reaches every record
// through one file, and the columns the asker describes to the writer are the
// rollup's rather than the slice table's.
//
// The statement the writer answers with is checked before the engine sees it. A
// model is being asked to write code against data it has never been shown, and
// the check is what makes the worst it can do a refusal.
func (a *Asker) Ask(ctx context.Context, manifest Manifest, question string) (Answer, error) {
	if err := manifest.Valid(); err != nil {
		return Answer{}, fmt.Errorf("ask: %w", err)
	}
	if strings.TrimSpace(question) == "" {
		return Answer{}, fmt.Errorf("ask %q: the question is empty: %w", manifest.Name, fs.ErrInvalid)
	}

	at := path.Join(a.root, manifest.Name+"."+a.querier.Extension())
	table, err := a.fsys.Read(ctx, at)
	if err != nil {
		return Answer{}, fmt.Errorf("ask %q: read %s: %w", manifest.Name, at, err)
	}

	statement, err := a.writer.WriteSQL(ctx, question, described(manifest))
	if err != nil {
		return Answer{}, fmt.Errorf("ask %q: %w", manifest.Name, err)
	}
	statement, err = readOnly(statement)
	if err != nil {
		return Answer{}, fmt.Errorf("ask %q: %w", manifest.Name, err)
	}

	columns, rows, err := a.query(ctx, manifest.Name, table, statement)
	if err != nil {
		return Answer{}, fmt.Errorf("ask %q: %s: %w", manifest.Name, statement, err)
	}
	return Answer{SQL: statement, Columns: columns, Rows: rows}, nil
}

// query is one statement through the engine, bounded, so an engine that never
// answers is a deadline rather than an Ask that never returns.
func (a *Asker) query(ctx context.Context, name string, table []byte, statement string) (Schema, []Row, error) {
	bounded, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	return a.querier.Query(bounded, name, table, statement)
}

// described is the rollup as a writer is shown it: the projection's name, and
// the rollup schema's columns spelled in the manifest's own type vocabulary.
//
// The writer is shown the rollup and nothing else. A schema naming the slice
// files as well would invite a statement joining a table the asker does not
// have, and every such statement is a query that fails after the model call has
// been paid for.
func described(m Manifest) llm.Table {
	schema := m.RollupSchema()
	columns := make([]llm.Column, len(schema))
	for i, field := range schema {
		columns[i] = llm.Column{Name: field.Name, Type: string(field.Type)}
	}
	return llm.Table{Name: m.Name, Columns: columns}
}

// reaching are the words a statement uses to get at something other than the
// table it was given: another database, the local filesystem, an extension, or
// the process running the engine. A statement is refused for naming one.
//
// The list is short because the statement has already been narrowed to a single
// SELECT, which leaves no room for a verb that writes. What is left is the set
// of ways a SELECT reads something it was not handed, and every one of them
// spells a name here or begins with "read_".
var reaching = []string{
	"attach", "copy", "detach", "export", "glob", "import",
	"install", "load", "pragma", "set", "sniff_csv", "use",
}

// readOnly is statement with its trailing semicolon removed, or fs.ErrInvalid
// saying why a caller is not being handed it.
//
// A statement passes if it is one SELECT that names nothing outside the table.
// That is three rules and they are load-bearing in this order: one statement,
// so nothing rides along behind a semicolon; SELECT, so the one statement
// cannot write; and no reaching name, so the one read is the table.
//
// SELECT rather than SELECT or WITH is deliberate. A leading CTE is how the
// engine is handed the table, so a statement that opened with its own would
// have to be spliced rather than wrapped, and a rule that reads "starts with
// SELECT" is one a caller can check by looking.
func readOnly(statement string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(statement), ";"))
	if trimmed == "" {
		return "", fmt.Errorf("the writer answered with no statement: %w", fs.ErrInvalid)
	}
	if strings.Contains(trimmed, ";") {
		return "", fmt.Errorf("%s: more than one statement: %w", trimmed, fs.ErrInvalid)
	}
	if !strings.EqualFold(firstWord(trimmed), "select") {
		return "", fmt.Errorf("%s: not a SELECT: %w", trimmed, fs.ErrInvalid)
	}
	for _, word := range words(trimmed) {
		if strings.HasPrefix(word, "read_") {
			return "", fmt.Errorf("%s: %s reads something other than the table: %w", trimmed, word, fs.ErrInvalid)
		}
		for _, refused := range reaching {
			if word == refused {
				return "", fmt.Errorf("%s: %s reaches outside the table: %w", trimmed, word, fs.ErrInvalid)
			}
		}
	}
	return trimmed, nil
}

// firstWord is the leading word of statement, which is the verb it runs.
func firstWord(statement string) string {
	for i, r := range statement {
		if !identifier(r) {
			return statement[:i]
		}
	}
	return statement
}

// words is the identifiers statement names, lowercased, with what is inside
// string literals left out.
//
// The literals are dropped because they are values rather than names: a
// question about a stage called "load" would otherwise be refused for asking
// it.
func words(statement string) []string {
	var found []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			found = append(found, word.String())
			word.Reset()
		}
	}

	inLiteral := false
	for _, r := range strings.ToLower(statement) {
		switch {
		case r == '\'':
			flush()
			inLiteral = !inLiteral
		case inLiteral, !identifier(r):
			flush()
		default:
			word.WriteRune(r)
		}
	}
	flush()
	return found
}

// identifier reports whether r is a character a SQL name is spelled with.
func identifier(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
