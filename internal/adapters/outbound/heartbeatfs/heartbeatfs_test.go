package heartbeatfs_test

import (
	"errors"
	"io/fs"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/heartbeatfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// clock is time a test moves by hand, because everything this plugin says is a
// function of time and nothing can be said about it against a clock that runs
// on its own.
type clock struct{ nanos atomic.Int64 }

func newClock(at time.Time) *clock {
	c := &clock{}
	c.nanos.Store(at.UnixNano())
	return c
}

func (c *clock) now() time.Time { return time.Unix(0, c.nanos.Load()).UTC() }

func (c *clock) advance(d time.Duration) { c.nanos.Add(int64(d)) }

func TestStatDescribesBothEntries(t *testing.T) {
	fsys := heartbeatfs.New(time.Second, newClock(time.Unix(0, 0)).now)

	for _, name := range []string{heartbeatfs.Beat, heartbeatfs.Count} {
		info, err := fsys.Stat(t.Context(), "/"+name)
		if err != nil {
			t.Fatalf("Stat(/%s) = %v", name, err)
		}
		if info.Name != name {
			t.Errorf("Stat(/%s).Name = %q, want %q", name, info.Name, name)
		}
		if info.IsDir() {
			t.Errorf("Stat(/%s).IsDir() = true, want a file", name)
		}
	}
}

func TestCountRisesOnceABeat(t *testing.T) {
	c := newClock(time.Unix(0, 0))
	fsys := heartbeatfs.New(time.Second, c.now)

	if count := read(t, fsys, "/count"); count != "0\n" {
		t.Errorf("count at the moment the mount came up = %q, want %q", count, "0\n")
	}

	c.advance(900 * time.Millisecond)
	if count := read(t, fsys, "/count"); count != "0\n" {
		t.Errorf("count part way through the first beat = %q, want %q", count, "0\n")
	}

	c.advance(200 * time.Millisecond)
	if count := read(t, fsys, "/count"); count != "1\n" {
		t.Errorf("count after the first beat = %q, want %q", count, "1\n")
	}
}

// Two reads inside one beat see one beat: the entry holds when the pulse last
// landed, not what the clock says.
func TestBeatHoldsStillInsideOneBeat(t *testing.T) {
	c := newClock(time.Unix(0, 0).UTC())
	fsys := heartbeatfs.New(time.Second, c.now)

	first := read(t, fsys, "/beat")
	c.advance(500 * time.Millisecond)
	if second := read(t, fsys, "/beat"); second != first {
		t.Errorf("beat read twice inside one beat = %q then %q, want one answer", first, second)
	}

	c.advance(600 * time.Millisecond)
	if third := read(t, fsys, "/beat"); third == first {
		t.Errorf("beat after the pulse landed = %q, want it moved on from %q", third, first)
	}
}

// ModTime is when the content last changed, and for a pulse that is the beat
// itself rather than when the mount came up.
func TestModTimeIsTheLatestBeat(t *testing.T) {
	start := time.Unix(0, 0).UTC()
	c := newClock(start)
	fsys := heartbeatfs.New(time.Second, c.now)
	c.advance(2500 * time.Millisecond)

	info, err := fsys.Stat(t.Context(), "/beat")
	if err != nil {
		t.Fatalf("Stat(/beat) = %v", err)
	}
	if want := start.Add(2 * time.Second); !info.ModTime.Equal(want) {
		t.Errorf("Stat(/beat).ModTime = %v, want %v", info.ModTime, want)
	}
}

func TestFactoryRefusesAnIntervalThatIsNotABeat(t *testing.T) {
	for _, spelled := range []string{"0s", "-1s", "soon"} {
		if _, err := heartbeatfs.Factory(map[string]string{"interval": spelled}); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Factory(interval=%q) = %v, want fs.ErrInvalid", spelled, err)
		}
	}
}

func TestFactoryRefusesASettingItCannotHonour(t *testing.T) {
	if _, err := heartbeatfs.Factory(map[string]string{"namespace": "beats"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(namespace) = %v, want fs.ErrInvalid", err)
	}
}

func read(t *testing.T, fsys vfs.FileSystem, name string) string {
	t.Helper()
	content, err := fsys.Read(t.Context(), name)
	if err != nil {
		t.Fatalf("Read(%q) = %v", name, err)
	}
	return string(content)
}
