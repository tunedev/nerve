// Package llm is the gateway: the one path a model call in this process takes.
//
// A model is a remote thing that fails the way remote things fail, and there is
// more than one of it — Ollama on the laptop, a hosted endpoint when there is
// one, a stand-in that needs no network at all. So nothing else holds a client.
// A stage that wants a summary holds a Summariser this package routes, the way
// a caller of the tree holds the mount table rather than a plugin, and which
// endpoint answers is a line of config.
//
// That is what the ordered chain buys. One flaky endpoint degrades a feature
// instead of taking down the path that used it: the provider that cannot answer
// is passed over, the next one is asked, and a caller learns only that it has a
// summary.
//
// Capability is reached through accessors and never through a type assertion.
// Every caller holds the Gateway rather than a provider, so gateway.(Embedder)
// would interrogate the router and never the thing that would do the work —
// the same defect that put ResponseController in the standard library. An
// accessor on the port forces the router to forward.
package llm

import "context"

// Provider is one model endpoint, described by what it will answer.
//
// It has no verbs of its own. Everything a provider does is reached through an
// accessor that returns a narrow interface or nil, because no endpoint serves
// every capability: the laptop's extractive summariser embeds nothing, and an
// embedding model writes no sentences. A provider that says nothing is legal
// and useless, which is the right shape for a port whose implementations differ
// in what they are for.
type Provider interface {
	// AsSummariser returns the endpoint's summarisation verb, or nil when it
	// does not summarise.
	AsSummariser() Summariser

	// AsEmbedder returns the endpoint's embedding verb, or nil when it does
	// not embed.
	AsEmbedder() Embedder
	// AsFactExtractor returns the endpoint's extraction verb, or nil when it
	// does not extract.
	AsFactExtractor() FactExtractor
	// AsFactConsolidator returns the endpoint's consolidation verb, or nil
	// when it does not merge facts.
	AsFactConsolidator() FactConsolidator

	// AsSQLWriter returns the endpoint's SQL-writing verb, or nil when it does
	// not write SQL.
	AsSQLWriter() SQLWriter
	// AsEntityExtractor returns the endpoint's entity extraction verb, or nil
	// when it does not read entities.
	AsEntityExtractor() EntityExtractor
}

// NoAccessors is the default a provider embeds so that implementing one
// capability costs one method. Every accessor answers nil, and a provider
// overrides the ones it serves.
type NoAccessors struct{}

// AsSummariser reports that the provider does not summarise.
func (NoAccessors) AsSummariser() Summariser { return nil }

// AsEmbedder reports that the provider does not embed.
func (NoAccessors) AsEmbedder() Embedder { return nil }

// AsFactExtractor reports that the provider does not extract facts.
func (NoAccessors) AsFactExtractor() FactExtractor { return nil }

// AsFactConsolidator reports that the provider does not merge facts.
func (NoAccessors) AsFactConsolidator() FactConsolidator { return nil }

// AsSQLWriter reports that the provider does not write SQL.
func (NoAccessors) AsSQLWriter() SQLWriter { return nil }

// AsEntityExtractor reports that the provider does not extract entities.
func (NoAccessors) AsEntityExtractor() EntityExtractor { return nil }

// Summariser says what a document is about in at most the sentences it is
// given.
//
// The budget is a ceiling and not a quota: a document with less to say than the
// budget allows says less. What separates two summarisers is whether the
// sentences are the document's own, and the caller is not told which it has.
type Summariser interface {
	Summarise(ctx context.Context, text string, sentences int) (string, error)
}

// Embedder turns text into the vectors a similarity search compares.
//
// It is a batch verb because a model endpoint is remote and a corpus is many
// documents: asking for a thousand vectors one call at a time pays the round
// trip a thousand times. One vector comes back per text, in the order the texts
// were given, which is what lets a caller pair them without a key.
//
// The vectors are one model's and are comparable only with each other. That is
// the operator's line to hold: a chain is meant to be one model at two
// endpoints, so a failover keeps an index comparable, and a chain listing two
// different embedding models is a config that quietly fills an index with
// vectors from neither.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Turn is one thing said in a conversation, as an extractor is handed it. It
// carries the speaker because what a transcript is worth extracting from turns
// on who said it: a preference the user stated is a fact about the user, and
// the same sentence in the assistant's own output is a fact about nothing.
type Turn struct {
	// Role is who said it, in the caller's vocabulary.
	Role string
	// Body is what was said.
	Body string
}

// Priority is how much a fact is worth keeping, on the three-step scale an
// extractor answers on. It is ordered, so a caller with room for some of what
// it was given keeps the larger values.
type Priority int

// The scale. Zero is not on it, so a fact that named no priority is refused
// rather than silently ranked lowest.
const (
	// Low is worth keeping and worth dropping first — something said in
	// passing, or hedged enough that it may not hold tomorrow.
	Low Priority = iota + 1
	// Normal is the ordinary fact: stated plainly, expected to hold.
	Normal
	// High is what a caller keeps when it keeps one thing — who the user is,
	// and what they told you to always or never do.
	High
)

// Fact is one thing an extractor found in a transcript: what sort of thing it
// is, what it says, and how much it is worth keeping.
//
// Kind is the extractor's vocabulary and this package reads none of it, for the
// reason a session's metadata keys are the caller's: what sorts of fact there
// are is a question the endpoint answers, and a port that understood one kind
// would have to be changed to understand a second.
//
// Subject names the one question a fact answers — where the user lives, what
// they are called — and is empty for a fact that answers none. It is the
// extractor's vocabulary too, and it is how a later answer to the same question
// is told apart from a second fact of the same kind.
type Fact struct {
	Kind     string
	Subject  string
	Body     string
	Priority Priority
}

// FactExtractor reads a conversation and answers with what is worth remembering
// from it.
//
// A transcript with nothing worth keeping extracts to no facts, which is an
// answer and not a failure. The turns arrive in the order they were said,
// because what a later turn means often depends on an earlier one.
type FactExtractor interface {
	Extract(ctx context.Context, turns []Turn) ([]Fact, error)
}

// Narrative is one thing a consolidator found several facts saying, and the
// sentence that says it in one place.
//
// Facts are positions in the batch the consolidator was handed rather than the
// facts themselves, the way an embedder answers in the order it was given: the
// caller holds what those positions came from, so it can name what the
// narrative replaces. A consolidator that answered with facts alone would
// leave the caller matching sentences to work out what it had merged.
//
// There is no priority on it. How much a merged claim is worth keeping is the
// caller's to derive from what it merged, because a model that ranked the
// narrative could quietly demote what it was told never to do.
type Narrative struct {
	// Facts are the positions of the facts this narrative merges, in the
	// batch the consolidator was handed.
	Facts []int
	// Body is what they say together.
	Body string
}

// FactConsolidator reads the facts a workspace believes and answers with the
// groups of them that say one thing, each with the sentence that says it.
//
// It is handed the whole believed set in one call rather than a group at a
// time, because which facts are related is the judgement being asked for: a
// caller that pre-grouped them would have already made it, and a call per group
// pays the round trip once per category.
//
// A set with nothing to merge consolidates to no narratives, which is an answer
// and not a failure — most of what a workspace believes is unrelated to the
// rest of it.
type FactConsolidator interface {
	Consolidate(ctx context.Context, facts []Fact) ([]Narrative, error)
}

// Column is one column of the table a question is asked over: what it is
// called, and what it holds spelled in whatever vocabulary the caller declares
// its tables in.
//
// The type is text and this package reads none of it, for the reason a fact's
// kind is the extractor's: what types a table has is a question the caller's
// own schema answers, and a port that understood one vocabulary would have to
// be changed to understand a second.
type Column struct {
	Name string
	Type string
}

// Table is the one table a question may be answered from: its name and its
// columns, in the order the caller declares them.
//
// One table and not a database, because what a writer is given has to be what
// the answer will be run against. A schema listing tables the caller cannot
// produce is an invitation to write a join that fails.
type Table struct {
	Name    string
	Columns []Column
}

// SQLWriter turns a question asked in the caller's own words into the SQL that
// answers it over one table.
//
// It answers with a statement and never with rows: nothing is run here, and a
// writer has no access to the data it is describing. That separation is what
// lets the caller refuse a statement before an engine ever sees it.
//
// A question the table cannot answer is still a statement — the writer is not
// the judge of whether the columns hold what was asked about — but a writer
// that cannot produce SQL at all fails rather than answering with prose.
type SQLWriter interface {
	WriteSQL(ctx context.Context, question string, table Table) (string, error)
}

// Entity is one thing a text names: what sort of thing it is, and which one of
// them.
//
// Kind is the extractor's vocabulary and this package reads none of it, for the
// reason a Fact's is: what sorts of thing there are is a question the endpoint
// answers, and a port that understood "person" would have to be changed to
// understand "product". The pair is an address rather than a sentence, which is
// what lets a graph put two mentions of one name in one place.
type Entity struct {
	Kind string
	Name string
}

// Relation is one thing a text says about two of the entities in it, read left
// to right.
//
// It is directed, because "Nerve uses Postgres" is not the claim that Postgres
// uses Nerve, and a graph that symmetrised it would answer a question nobody
// asked. Predicate is the extractor's vocabulary for the same reason Kind is.
type Relation struct {
	From      Entity
	Predicate string
	To        Entity
}

// Extraction is what an extractor read out of a text: the entities it found and
// the relations it found between them.
//
// Every entity a relation names is also in Entities, so a caller building a
// graph makes its nodes from one field and its edges from the other without
// reconciling the two. A text that names things and relates none of them
// extracts to entities alone, which is the ordinary case.
type Extraction struct {
	Entities  []Entity
	Relations []Relation
}

// EntityExtractor reads a text and answers with what it names and how those
// things are related.
//
// The verb is not called Extract, though FactExtractor's is. A hosted endpoint
// that served both capabilities would be one type implementing both interfaces,
// and Go gives it one method per name.
//
// A text naming nothing extracts to nothing, which is an answer and not a
// failure: most of a document is prose about things it named elsewhere.
type EntityExtractor interface {
	ExtractEntities(ctx context.Context, text string) (Extraction, error)
}
