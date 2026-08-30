package ingest_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const (
	resourcesRoot = "/resources"
	queueMount    = "queue"
)

var here = workspace.Scope{User: "ada", Workspace: "hunt"}

// seen is a stage that does nothing but remember it ran, which is what makes
// "the work has not happened yet" and "the work has happened" two different
// observations rather than the same one twice.
type seen struct {
	ran []string
	got []resource.Resource
}

func (s *seen) stage(name string) ingest.Stage {
	return ingest.Stage{Name: name, Run: func(_ context.Context, _ workspace.Scope, r resource.Resource) error {
		s.ran = append(s.ran, name)
		s.got = append(s.got, r)
		return nil
	}}
}

// listen hands the worker every key that is queued and then stops, which is a
// broker that has caught up rather than one that has gone away.
type listen struct{ keys []string }

func (l listen) Listen(ctx context.Context, notify queuefs.Notify) error {
	for _, key := range l.keys {
		if err := notify(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// bus is the whole loop: the store, the tree whose writes are published as
// jobs, and the pipeline waiting at the other end.
//
// The jobs mount is a flat key space rather than a queue. What a queue adds —
// a claim that consumes and a journal that outlives the process — is asserted
// by that plugin's own suite, and what is asserted here is the trip a changed
// path makes from the write that made it to the pipeline that acts on it.
type bus struct {
	resources *resource.Store
	jobs      vfs.FileSystem
	keyspace  *kvfs.MemStore
	pipeline  *ingest.Pipeline
}

func newBus(t *testing.T, stages ...ingest.Stage) *bus {
	t.Helper()

	keyspace := kvfs.NewMemStore()
	jobs := vfs.NewMountTable()
	if err := jobs.Mount("/"+queueMount, kvfs.New(keyspace, "")); err != nil {
		t.Fatalf("Mount(%q): %v", queueMount, err)
	}

	table := vfs.NewMountTable()
	if err := table.Mount(resourcesRoot, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", resourcesRoot, err)
	}
	signer, err := resource.NewSigner([]byte("a key nobody else has"), time.Now)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	tree := vfs.WithWriteObserver(table, queuefs.NewEmitter(jobs, queueMount))
	store, err := resource.NewStore(tree, resourcesRoot, signer)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return &bus{resources: store, jobs: jobs, keyspace: keyspace, pipeline: ingest.New(store, stages...)}
}

// queued is every job standing on the queue, read from the keyspace holding
// them rather than by walking the tree: a job key can also be the prefix of
// another, which is one entry to a listing and two jobs to a queue.
func (b *bus) queued(t *testing.T) []string {
	t.Helper()

	prefix := queuefs.Jobs + "/"
	entries, err := b.keyspace.List(t.Context(), prefix)
	if err != nil {
		t.Fatalf("List(%q): %v", prefix, err)
	}
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, strings.TrimPrefix(entry.Key, prefix))
	}
	slices.Sort(keys)
	return keys
}

// worker is the consuming end wired the way the shipped stack wires it: a
// worker told a key claims it through the tree and hands it to the pipeline.
func (b *bus) worker(t *testing.T) *queuefs.Worker {
	t.Helper()

	return queuefs.NewWorker(b.jobs, queueMount, listen{keys: b.queued(t)},
		func(ctx context.Context, key string, _ []byte) error { return b.pipeline.Ingest(ctx, key) })
}

func (b *bus) work(t *testing.T) {
	t.Helper()

	if err := b.worker(t).Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func (b *bus) upload(t *testing.T, name, content string) {
	t.Helper()

	if _, err := b.resources.Create(t.Context(), here, name, "text/plain"); err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	if _, err := b.resources.Upload(t.Context(), here, name, strings.NewReader(content)); err != nil {
		t.Fatalf("Upload(%q): %v", name, err)
	}
	if _, err := b.resources.Complete(t.Context(), here, name); err != nil {
		t.Fatalf("Complete(%q): %v", name, err)
	}
}

// The acceptance: finalising a resource queues its ingestion and returns having
// done none of it. A stage that had run by the time Complete came back would be
// a stage the caller waited for.
func TestFinalisingAResourceQueuesItsIngestionAndDoesNoneOfIt(t *testing.T) {
	ran := &seen{}
	b := newBus(t, ran.stage("extract"))

	b.upload(t, "transcript.txt", "a first draft")

	if len(ran.ran) != 0 {
		t.Fatalf("stages ran during the upload: %v", ran.ran)
	}
	if want := "resources/ada/hunt/transcript.txt/meta.json"; !slices.Contains(b.queued(t), want) {
		t.Fatalf("queued %v, want %q among them", b.queued(t), want)
	}

	b.work(t)

	if want := []string{"extract"}; !slices.Equal(ran.ran, want) {
		t.Fatalf("stages ran = %v, want %v", ran.ran, want)
	}
	got := ran.got[0]
	if got.Name != "transcript.txt" || !got.Complete || got.Size != int64(len("a first draft")) {
		t.Errorf("stage saw %+v, want the finalised transcript.txt", got)
	}
}

// Every write under the root is published and only one of them is a resource
// arriving. The content the upload streamed in is a job too, and it is not a
// second ingestion of the same file.
func TestIngestionRunsOncePerResourceAndNotOncePerWrite(t *testing.T) {
	ran := &seen{}
	b := newBus(t, ran.stage("extract"))

	b.upload(t, "transcript.txt", "a first draft")
	if queued := b.queued(t); len(queued) < 2 {
		t.Fatalf("queued %v, want the content and the meta file both", queued)
	}

	b.work(t)

	if want := []string{"extract"}; !slices.Equal(ran.ran, want) {
		t.Errorf("stages ran = %v, want %v", ran.ran, want)
	}
}

// A resource declared and not yet finalised is one whose content is still
// arriving. Ingesting it would read whatever the upload has managed so far.
func TestPipelineLeavesAResourceThatHasNotBeenFinalised(t *testing.T) {
	ran := &seen{}
	b := newBus(t, ran.stage("extract"))

	if _, err := b.resources.Create(t.Context(), here, "transcript.txt", "text/plain"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	b.work(t)

	if len(ran.ran) != 0 {
		t.Errorf("stages ran = %v, want none for a resource still arriving", ran.ran)
	}
}

// The bus carries every change the tree saw and a pipeline is one listener on
// it. What it has no resource for is somebody else's work, not a failure.
func TestPipelineIgnoresAChangeThatDeclaresNoResource(t *testing.T) {
	ran := &seen{}
	b := newBus(t, ran.stage("extract"))

	for _, changed := range []string{
		"resources/ada/hunt/transcript.txt/content",
		"keys/ada/hunt/anything",
		"resources/ada",
	} {
		if err := b.pipeline.Ingest(t.Context(), changed); err != nil {
			t.Errorf("Ingest(%q) = %v, want it left alone", changed, err)
		}
	}
	if len(ran.ran) != 0 {
		t.Errorf("stages ran = %v, want none", ran.ran)
	}
}

// A notification outliving what it is about is the ordinary case, and a
// resource deleted before its job was claimed is not a job that failed.
func TestPipelineIgnoresAResourceDeletedBeforeTheJobWasClaimed(t *testing.T) {
	ran := &seen{}
	b := newBus(t, ran.stage("extract"))

	b.upload(t, "transcript.txt", "a first draft")
	worker := b.worker(t)
	if err := b.resources.Delete(t.Context(), here, "transcript.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := worker.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ran.ran) != 0 {
		t.Errorf("stages ran = %v, want none for a resource that is gone", ran.ran)
	}
}

func TestStagesRunInTheOrderTheyWereGiven(t *testing.T) {
	ran := &seen{}
	b := newBus(t, ran.stage("extract"), ran.stage("summarise"), ran.stage("index"))

	b.upload(t, "transcript.txt", "a first draft")
	b.work(t)

	if want := []string{"extract", "summarise", "index"}; !slices.Equal(ran.ran, want) {
		t.Errorf("stages ran = %v, want %v", ran.ran, want)
	}
}

// A run that failed says which stage did, and stops there: what follows a stage
// is written expecting it to have run.
func TestAStageThatFailsIsNamedAndEndsTheRun(t *testing.T) {
	ran := &seen{}
	refused := errors.New("no processor for this format")
	fails := ingest.Stage{Name: "extract", Run: func(context.Context, workspace.Scope, resource.Resource) error {
		return refused
	}}
	b := newBus(t, fails, ran.stage("summarise"))

	b.upload(t, "transcript.txt", "a first draft")
	err := b.worker(t).Run(t.Context())

	if !errors.Is(err, refused) {
		t.Fatalf("Run = %v, want %v", err, refused)
	}
	if !strings.Contains(err.Error(), `stage "extract"`) {
		t.Errorf("Run = %v, want it to name the stage that failed", err)
	}
	if len(ran.ran) != 0 {
		t.Errorf("stages ran after the failure = %v, want none", ran.ran)
	}
}
