package serverinfofs_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/serverinfofs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestStatDescribesEveryEntry(t *testing.T) {
	fsys := serverinfofs.New("v1.2.3", time.Now, vfs.NewTraffic(time.Now))

	for _, name := range []string{"version", "go", "pid", "hostname", "started", "uptime", "traffic"} {
		info, err := fsys.Stat(t.Context(), "/"+name)
		if err != nil {
			t.Fatalf("Stat(/%s) = %v", name, err)
		}
		if info.Size == 0 {
			t.Errorf("Stat(/%s).Size = 0, want the length of what it holds", name)
		}
	}
}

// What a mount says about the process has to be what the process says about
// itself, or it is a second source of truth that can be wrong on its own.
func TestEntriesDescribeThisProcess(t *testing.T) {
	fsys := serverinfofs.New("v1.2.3", time.Now, vfs.NewTraffic(time.Now))

	want := map[string]string{
		"/version": "v1.2.3\n",
		"/go":      runtime.Version() + "\n",
		"/pid":     strconv.Itoa(os.Getpid()) + "\n",
	}
	for name, expected := range want {
		content, err := fsys.Read(t.Context(), name)
		if err != nil {
			t.Fatalf("Read(%q) = %v", name, err)
		}
		if string(content) != expected {
			t.Errorf("Read(%q) = %q, want %q", name, content, expected)
		}
	}
}

func TestUptimeRisesWithTheClock(t *testing.T) {
	at := time.Unix(0, 0).UTC()
	now := func() time.Time { return at }
	fsys := serverinfofs.New("v1.2.3", now, vfs.NewTraffic(now))

	at = at.Add(90 * time.Second)

	content, err := fsys.Read(t.Context(), "/uptime")
	if err != nil {
		t.Fatalf("Read(/uptime) = %v", err)
	}
	if string(content) != "1m30s\n" {
		t.Errorf("Read(/uptime) after 90s = %q, want %q", content, "1m30s\n")
	}
}

func TestReadDirListsEverythingSorted(t *testing.T) {
	entries, err := serverinfofs.New("v1.2.3", time.Now, vfs.NewTraffic(time.Now)).ReadDir(t.Context(), "/")
	if err != nil {
		t.Fatalf("ReadDir(/) = %v", err)
	}

	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	want := []string{"go", "hostname", "pid", "started", "traffic", "uptime", "version"}
	if !slices.Equal(listed, want) {
		t.Errorf("ReadDir(/) = %v, want %v", listed, want)
	}
}

// The version is the binary's, so a config that tried to name one is refused
// rather than believed.
func TestFactoryTakesNoSettings(t *testing.T) {
	if _, err := serverinfofs.Factory("v1.2.3", vfs.NewTraffic(time.Now))(map[string]string{"version": "v9"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory()(version) = %v, want fs.ErrInvalid", err)
	}
}

// The counters are the point of the entry: what a mount has been asked for has
// to read back through the tree, without a collector anywhere.
func TestTrafficReadsBackTheCounters(t *testing.T) {
	traffic := vfs.NewTraffic(time.Now)
	record(traffic, "/local", "localfs", nil)
	record(traffic, "/local", "localfs", nil)
	record(traffic, "/queue", "queuefs", errors.New("broker is down"))

	content, err := serverinfofs.New("v1.2.3", time.Now, traffic).Read(t.Context(), "/traffic")
	if err != nil {
		t.Fatalf("Read(/traffic) = %v", err)
	}

	want := "mount\tkind\tcalls\tfailed\n" +
		"/local\tlocalfs\t2\t0\n" +
		"/queue\tqueuefs\t1\t1\n"
	if string(content) != want {
		t.Errorf("Read(/traffic) = %q, want %q", content, want)
	}
}

// A process nothing has called yet still answers, with the shape and no rows.
// An empty answer and a missing entry would read the same.
func TestTrafficBeforeAnyCallIsTheHeadingAlone(t *testing.T) {
	content, err := serverinfofs.New("v1.2.3", time.Now, vfs.NewTraffic(time.Now)).Read(t.Context(), "/traffic")
	if err != nil {
		t.Fatalf("Read(/traffic) = %v", err)
	}
	if want := "mount\tkind\tcalls\tfailed\n"; string(content) != want {
		t.Errorf("Read(/traffic) with no calls = %q, want %q", content, want)
	}
}

// record puts one finished call through traffic.
func record(traffic *vfs.Traffic, mount, kind string, err error) {
	_, done := traffic.Begin(context.Background(), vfs.Call{Mount: mount, Kind: kind, Verb: "Read", Path: "/x"})
	done(err)
}
