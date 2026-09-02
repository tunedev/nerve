package main

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/inbound/admin"
	"github.com/tunedev/nerve/internal/config"
)

const (
	readyPath = "/readyz"
	livePath  = "/livez"

	// drainWindow is long enough that the probe interval cannot be mistaken
	// for it and short enough to spend in a test.
	drainWindow = 200 * time.Millisecond

	// probeInterval is how often a balancer and an orchestrator are stood in
	// for. Fine enough to land several answers inside the drain window.
	probeInterval = 5 * time.Millisecond

	// probeBudget bounds one probe against a listener that is up and
	// answering from memory.
	probeBudget = time.Second

	// serveBudget bounds the whole run, so a wedged serve fails this test
	// rather than hanging the suite.
	serveBudget = 10 * time.Second
)

// observation is one probe answer, dated from the moment it was asked for. A
// timestamp taken before the request can never land after the state the answer
// reports, so the ordering it supports is the conservative reading.
type observation struct {
	path string
	code int
	at   time.Time
}

// watcher is what routes traffic here and what restarts crashed processes: it
// knows only what the probes answered and when. It stops the first time a
// probe cannot connect, because a closed listener is the end of what there is
// to observe.
type watcher struct {
	mu   sync.Mutex
	seen []observation
}

func (w *watcher) run(base string, stop <-chan struct{}) {
	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   probeBudget,
	}
	defer client.CloseIdleConnections()

	tick := time.NewTicker(probeInterval)
	defer tick.Stop()

	for {
		for _, path := range []string{readyPath, livePath} {
			asked := time.Now()
			resp, err := client.Get(base + path)
			if err != nil {
				return
			}
			resp.Body.Close()
			w.record(observation{path: path, code: resp.StatusCode, at: asked})
		}

		select {
		case <-stop:
			return
		case <-tick.C:
		}
	}
}

func (w *watcher) record(o observation) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = append(w.seen, o)
}

func (w *watcher) all() []observation {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]observation(nil), w.seen...)
}

// first and last bracket a code on a path: first is when it started being
// answered, last is the latest moment it still was.
func (w *watcher) first(path string, code int) (time.Time, bool) {
	for _, o := range w.all() {
		if o.path == path && o.code == code {
			return o.at, true
		}
	}
	return time.Time{}, false
}

func (w *watcher) last(path string, code int) (time.Time, bool) {
	var at time.Time
	var found bool
	for _, o := range w.all() {
		if o.path == path && o.code == code {
			at, found = o.at, true
		}
	}
	return at, found
}

// The ordering serve holds is load-bearing and silent when broken. Shut down
// before draining, or drop the wait, and every other test in this repo still
// passes: the handler is not what breaks. What breaks is behind a balancer,
// which never observes readiness failing, keeps routing to a socket that is
// closing, and fails requests during a deploy.
//
// So this drives the real serve over a real socket and asserts only what a
// balancer and an orchestrator can see: the order the probes changed answer
// in, and the spacing between.
func TestDrainIsObservableBeforeShutdown(t *testing.T) {
	health := &admin.Health{}
	srv := &http.Server{
		Addr:    freeAddr(t),
		Handler: admin.Handler(config.Defaults().Effective(nil, nil), health),
	}

	// Shutdown runs its hooks the moment it is entered. It is the one event
	// here that has no answer to probe for.
	entered := make(chan time.Time, 1)
	srv.RegisterOnShutdown(func() { entered <- time.Now() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, health, drainWindow) }()

	base := "http://" + srv.Addr
	waitUntilReady(t, base)

	var seen watcher
	stop := make(chan struct{})
	watching := make(chan struct{})
	go func() { defer close(watching); seen.run(base, stop) }()
	waitUntilWatched(t, &seen)

	cancel() // the signal the process would be sent

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(serveBudget):
		t.Fatal("serve did not return after its context was cancelled")
	}
	close(stop)
	<-watching

	var shutdownAt time.Time
	select {
	case shutdownAt = <-entered:
	case <-time.After(probeBudget):
		t.Fatal("shutdown was never entered")
	}

	lastReady, ok := seen.last(readyPath, http.StatusOK)
	if !ok {
		t.Fatal("readiness never answered ready: the run says nothing about ordering")
	}
	readyFailed, ok := seen.first(readyPath, http.StatusServiceUnavailable)
	if !ok {
		t.Fatal("readiness never failed over an open listener: all a balancer would see is the socket close")
	}

	if !readyFailed.Before(shutdownAt) {
		t.Errorf("readiness failed %s after shutdown was entered, want before it", readyFailed.Sub(shutdownAt))
	}
	if waited := shutdownAt.Sub(lastReady); waited < drainWindow {
		t.Errorf("shutdown was entered %s after the last ready answer, want at least %s: nothing routing here had time to notice", waited, drainWindow)
	}

	// Liveness is the process, not its willingness to take work. An
	// orchestrator that sees it fail restarts a process that is shutting down
	// cleanly, which is the deploy failing twice over.
	alive := 0
	for _, o := range seen.all() {
		if o.path != livePath || o.at.After(shutdownAt) {
			continue
		}
		if o.code != http.StatusOK {
			t.Errorf("liveness answered %d while draining, want %d", o.code, http.StatusOK)
		}
		if !o.at.Before(readyFailed) {
			alive++
		}
	}
	if alive == 0 {
		t.Error("liveness was never observed answering between readiness failing and shutdown being entered")
	}
}

// freeAddr returns an address nothing is listening on. serve drives
// srv.ListenAndServe, so what it can be handed is an address and not a
// listener.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return addr
}

// waitUntilWatched blocks until the watcher has an answer of its own, so the
// drain cannot begin before anything is looking.
func waitUntilWatched(t *testing.T, seen *watcher) {
	t.Helper()
	deadline := time.Now().Add(serveBudget)
	for time.Now().Before(deadline) {
		if _, ok := seen.last(readyPath, http.StatusOK); ok {
			return
		}
		time.Sleep(probeInterval)
	}
	t.Fatalf("watcher recorded no ready answer within %s", serveBudget)
}

// waitUntilReady blocks until the server is answering, so that everything the
// watcher records afterwards belongs to the drain.
func waitUntilReady(t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Timeout: probeBudget}
	defer client.CloseIdleConnections()

	deadline := time.Now().Add(serveBudget)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + readyPath)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(probeInterval)
	}
	t.Fatalf("server never answered %s within %s", readyPath, serveBudget)
}
