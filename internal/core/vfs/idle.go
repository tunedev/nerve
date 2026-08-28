package vfs

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// idling is the bound on one transfer, and the other half of the split
// WithTimeout makes: the open gets a deadline, and what it returned gets this.
// A transfer is not a call and has no length worth bounding — a healthy
// download is as long as the entry — so what is bounded is idleness. Nothing
// moved for the bound is a far end that has gone quiet.
//
// Ending a transfer is ending its context, which is the only handle anything
// outside the plugin has on a read already in flight. A plugin that ignores
// its context stays blocked inside the call it is in; the transfer still ends,
// and the next read on the stream reports it.
type idling struct {
	within  time.Duration
	release context.CancelFunc
	timer   *time.Timer
	stalled atomic.Bool
}

// watch arms the bound over the transfer release ends.
func watch(within time.Duration, release context.CancelFunc) *idling {
	idle := &idling{within: within, release: release}
	idle.timer = time.AfterFunc(within, func() {
		idle.stalled.Store(true)
		release()
	})
	return idle
}

// moved restarts the bound, which is what a byte crossing the stream means. A
// transfer already ended stays ended: a late byte does not revive it.
func (i *idling) moved(n int) {
	if n > 0 && !i.stalled.Load() {
		i.timer.Reset(i.within)
	}
}

// done lifts the bound and ends the transfer, which is what closing a stream
// means for the context it was running on.
func (i *idling) done() {
	i.timer.Stop()
	i.release()
}

// err is the transfer out of time, or nil while it is still moving. A plugin
// that saw the transfer end reports the cancellation rather than the stall
// behind it, so the stall is what the caller is told instead.
func (i *idling) err() error {
	if !i.stalled.Load() {
		return nil
	}
	return fmt.Errorf("stream idle for %s: %w", i.within, context.DeadlineExceeded)
}

// streamReader is one read transfer under the bound. Every read that moves a
// byte restarts it, and closing the stream lifts it.
type streamReader struct {
	stream io.ReadCloser
	idle   *idling
}

func (r streamReader) Read(into []byte) (int, error) {
	if stalled := r.idle.err(); stalled != nil {
		return 0, stalled
	}

	n, err := r.stream.Read(into)
	r.idle.moved(n)
	if err != nil {
		if stalled := r.idle.err(); stalled != nil {
			return n, stalled
		}
	}
	return n, err
}

func (r streamReader) Close() error {
	r.idle.done()

	err := r.stream.Close()
	if stalled := r.idle.err(); stalled != nil {
		return stalled
	}
	return err
}

// streamWriter is one write transfer under the same bound. A write nobody is
// taking is as stuck as a read nobody is sending, and Close reports a transfer
// the bound ended rather than returning the nil that would say it landed.
type streamWriter struct {
	stream io.WriteCloser
	idle   *idling
}

func (w streamWriter) Write(data []byte) (int, error) {
	if stalled := w.idle.err(); stalled != nil {
		return 0, stalled
	}

	n, err := w.stream.Write(data)
	w.idle.moved(n)
	if err != nil {
		if stalled := w.idle.err(); stalled != nil {
			return n, stalled
		}
	}
	return n, err
}

func (w streamWriter) Close() error {
	w.idle.done()

	err := w.stream.Close()
	if stalled := w.idle.err(); stalled != nil {
		return stalled
	}
	return err
}
