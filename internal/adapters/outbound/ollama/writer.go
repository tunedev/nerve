package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
)

// instruction is what the model is told it is for. It is fixed rather than
// configurable because it is not a preference: every rule in it is one the
// caller checks afterwards, and a build that changed the wording here would be
// changing what the model is asked to do without changing what is allowed.
//
// The rules are stated as the shape of the answer rather than as prohibitions,
// because a model that is told what to write writes it and a model that is told
// what not to write often explains itself first.
const instruction = `You turn a question into one DuckDB SQL statement.

Answer with the statement alone: no explanation, no code fence, no trailing
notes. The statement begins with SELECT, ends without a semicolon, and reads
only the table described below. Use only the columns listed. Where the question
asks for something the table cannot answer, answer with the closest statement
the columns allow.`

// Writer is one Ollama endpoint asked for one model's SQL.
//
// No timeout is set on the client, for the reason the embedder sets none: the
// gateway has already bounded the call, and a second bound here would be a
// second layer deciding how long a model may take.
type Writer struct {
	endpoint string
	model    string
	client   *http.Client
}

// NewWriter returns a writer over base for model. It resolves the URL here so a
// misspelled endpoint is a boot failure rather than the first question's.
func NewWriter(base, model string, client *http.Client) (*Writer, error) {
	endpoint, err := resolve(base, model, generatePath)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{}
	}
	return &Writer{endpoint: endpoint, model: model, client: client}, nil
}

// CloseIdleConnections drops the connections the client is holding open.
func (w *Writer) CloseIdleConnections() { w.client.CloseIdleConnections() }

// WriteSQL returns the statement the model answers question with over table.
//
// Nothing here judges the statement. The caller runs it and the caller decides
// what it will run, so a writer that refused a statement it disliked would be a
// second, quieter copy of that decision in the place least able to see it.
//
// The temperature is zero. The same question over the same table is asked again
// by whoever reads the answer and doubts it, and two different statements would
// make that reading impossible.
func (w *Writer) WriteSQL(ctx context.Context, question string, table llm.Table) (string, error) {
	where := fmt.Sprintf("write SQL with %q", w.model)
	if strings.TrimSpace(question) == "" {
		return "", fmt.Errorf("%s: the question is empty: %w", where, fs.ErrInvalid)
	}
	if len(table.Columns) == 0 {
		return "", fmt.Errorf("%s: table %q has no columns to answer from: %w", where, table.Name, fs.ErrInvalid)
	}

	body, err := json.Marshal(generation{
		Model:   w.model,
		System:  instruction,
		Prompt:  prompt(question, table),
		Stream:  false,
		Options: options{Temperature: 0},
	})
	if err != nil {
		return "", fmt.Errorf("%s: %w", where, err)
	}

	var answered completion
	if err := call(ctx, w.client, w.endpoint, where, body, &answered); err != nil {
		return "", err
	}

	statement := unfenced(answered.Response)
	if statement == "" {
		return "", fmt.Errorf("%s: the endpoint answered nothing: %w", where, fs.ErrInvalid)
	}
	return statement, nil
}

// prompt is the table and the question, in that order: what can be answered
// from, and then what is being asked.
func prompt(question string, table llm.Table) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Table %s\n", table.Name)
	for _, column := range table.Columns {
		fmt.Fprintf(&out, "  %s %s\n", column.Name, column.Type)
	}
	fmt.Fprintf(&out, "\nQuestion: %s\n", question)
	return out.String()
}

// generation is what /api/generate is asked. Stream is off because the answer
// is one statement and there is nothing to show while it arrives.
type generation struct {
	Model   string  `json:"model"`
	System  string  `json:"system"`
	Prompt  string  `json:"prompt"`
	Stream  bool    `json:"stream"`
	Options options `json:"options"`
}

// options is what the endpoint is asked to run the model with. Everything else
// it takes is left at whatever the model was built with.
type options struct {
	Temperature float64 `json:"temperature"`
}

// completion is what it answers. Everything else it reports is timing.
type completion struct {
	Response string `json:"response"`
}

// unfenced is answer with the markdown a model wraps code in taken off.
//
// Models fence code however plainly they are asked not to, and a fence reaching
// the caller is a statement refused for not beginning with SELECT — a true
// refusal of a statement that was fine. Nothing else is stripped: text before
// or after the statement stays, because a model that explained itself has not
// answered with a statement and the caller should see that.
func unfenced(answer string) string {
	trimmed := strings.TrimSpace(answer)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	_, after, found := strings.Cut(trimmed, "\n")
	if !found {
		return ""
	}
	if end := strings.LastIndex(after, "```"); end >= 0 {
		after = after[:end]
	}
	return strings.TrimSpace(after)
}
