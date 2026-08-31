package ingest_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// barrier releases only once every handler has reached it, which is what makes
// "these ran together" an observation rather than an assumption: a dispatcher
// running its handlers one after another never gets past the first.
type barrier struct {
	left atomic.Int32
	open chan struct{}
}

func newBarrier(handlers int) *barrier {
	b := &barrier{open: make(chan struct{})}
	b.left.Store(int32(handlers))
	return b
}

func (b *barrier) meet(ctx context.Context) error {
	if b.left.Add(-1) == 0 {
		close(b.open)
	}
	select {
	case <-b.open:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// indexer is a handler written the way a real one is: it decides for itself
// which resources are its work, and does that work when it is handed one.
type indexer struct {
	name    string
	claims  func(resource.Resource) bool
	fails   error
	meet    *barrier
	mu      sync.Mutex
	indexed []string
}

func (i *indexer) Name() string { return i.name }

func (i *indexer) Handles(r resource.Resource) bool {
	if i.claims == nil {
		return true
	}
	return i.claims(r)
}

func (i *indexer) Index(ctx context.Context, _ workspace.Scope, r resource.Resource) error {
	if i.meet != nil {
		if err := i.meet.meet(ctx); err != nil {
			return err
		}
	}
	i.mu.Lock()
	i.indexed = append(i.indexed, r.Name)
	i.mu.Unlock()
	return i.fails
}

func (i *indexer) saw() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.indexed)
}

func plain(name string) resource.Resource {
	return resource.Resource{Name: name, ContentType: "text/plain", Size: 3, Complete: true}
}

func handlesType(want string) func(resource.Resource) bool {
	return func(r resource.Resource) bool { return r.ContentType == want }
}

// The acceptance: a handler is added by handing it to the dispatcher, which
// then routes by what the handler says about itself. Nothing here knows what
// kinds of indexing exist.
func TestDispatchFansAResourceToEveryHandlerThatClaimsIt(t *testing.T) {
	text := &indexer{name: "text", claims: handlesType("text/plain")}
	vectors := &indexer{name: "vectors"}
	pdf := &indexer{name: "pdf", claims: handlesType("application/pdf")}

	if err := ingest.NewDispatch(text, vectors, pdf).Run(t.Context(), here, plain("transcript.txt")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, claimed := range []*indexer{text, vectors} {
		if want := []string{"transcript.txt"}; !slices.Equal(claimed.saw(), want) {
			t.Errorf("%s indexed %v, want %v", claimed.name, claimed.saw(), want)
		}
	}
	if got := pdf.saw(); len(got) != 0 {
		t.Errorf("pdf indexed %v, want nothing it did not claim", got)
	}
}

// Nothing claiming a resource is a resource with no indexing to do, which is
// the ordinary case for a format nobody handles yet.
func TestDispatchIsQuietWhenNoHandlerClaimsTheResource(t *testing.T) {
	pdf := &indexer{name: "pdf", claims: handlesType("application/pdf")}

	if err := ingest.NewDispatch(pdf).Run(t.Context(), here, plain("transcript.txt")); err != nil {
		t.Errorf("Run = %v, want no error for a resource nobody indexes", err)
	}
}

// Handlers are independent of each other, so one failing costs its own index
// and not the others. A pipeline stage ends the run because what follows it is
// written expecting it to have happened; nothing follows an indexer.
func TestAFailingHandlerDoesNotStopTheOthers(t *testing.T) {
	refused := errors.New("the vector store is down")
	vectors := &indexer{name: "vectors", fails: refused}
	text := &indexer{name: "text"}
	graph := &indexer{name: "graph"}

	err := ingest.NewDispatch(vectors, text, graph).Run(t.Context(), here, plain("transcript.txt"))

	if !errors.Is(err, refused) {
		t.Fatalf("Run = %v, want %v", err, refused)
	}
	if !strings.Contains(err.Error(), `indexer "vectors"`) {
		t.Errorf("Run = %v, want it to name the handler that failed", err)
	}
	for _, ran := range []*indexer{text, graph} {
		if want := []string{"transcript.txt"}; !slices.Equal(ran.saw(), want) {
			t.Errorf("%s indexed %v, want %v despite the failure beside it", ran.name, ran.saw(), want)
		}
	}
}

// Every failure is reported, not whichever one arrived first.
func TestDispatchReportsEveryHandlerThatFailed(t *testing.T) {
	down := errors.New("the vector store is down")
	full := errors.New("the graph is out of space")

	err := ingest.NewDispatch(
		&indexer{name: "vectors", fails: down},
		&indexer{name: "graph", fails: full},
	).Run(t.Context(), here, plain("transcript.txt"))

	if !errors.Is(err, down) || !errors.Is(err, full) {
		t.Errorf("Run = %v, want both %v and %v", err, down, full)
	}
}

// Fan-out is the point: an indexer is remote work, so N of them cost one of
// them. Each handler waits for the others to arrive, and only a dispatcher
// that started them together ever releases them.
func TestHandlersRunConcurrently(t *testing.T) {
	const handlers = 3

	meet := newBarrier(handlers)
	all := make([]ingest.Indexer, 0, handlers)
	for range handlers {
		all = append(all, &indexer{name: "meet", meet: meet})
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := ingest.NewDispatch(all...).Run(ctx, here, plain("transcript.txt")); err != nil {
		t.Fatalf("Run = %v, want the handlers to have run together", err)
	}
}

// A dispatcher is a stage, which is how a handler set reaches the pipeline
// without the pipeline knowing there is such a thing as an indexer.
func TestDispatchIsAPipelineStage(t *testing.T) {
	text := &indexer{name: "text"}
	b := newBus(t, ingest.NewDispatch(text).Stage())

	b.upload(t, "transcript.txt", "a first draft")
	b.work(t)

	if want := []string{"transcript.txt"}; !slices.Equal(text.saw(), want) {
		t.Errorf("text indexed %v, want %v", text.saw(), want)
	}
}
