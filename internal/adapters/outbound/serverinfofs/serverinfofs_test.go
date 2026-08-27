package serverinfofs_test

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/serverinfofs"
)

func TestStatDescribesEveryEntry(t *testing.T) {
	fsys := serverinfofs.New("v1.2.3", time.Now)

	for _, name := range []string{"version", "go", "pid", "hostname", "started", "uptime"} {
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
	fsys := serverinfofs.New("v1.2.3", time.Now)

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
	fsys := serverinfofs.New("v1.2.3", now)

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
	entries, err := serverinfofs.New("v1.2.3", time.Now).ReadDir(t.Context(), "/")
	if err != nil {
		t.Fatalf("ReadDir(/) = %v", err)
	}

	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	want := []string{"go", "hostname", "pid", "started", "uptime", "version"}
	if !slices.Equal(listed, want) {
		t.Errorf("ReadDir(/) = %v, want %v", listed, want)
	}
}

// The version is the binary's, so a config that tried to name one is refused
// rather than believed.
func TestFactoryTakesNoSettings(t *testing.T) {
	if _, err := serverinfofs.Factory("v1.2.3")(map[string]string{"version": "v9"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory()(version) = %v, want fs.ErrInvalid", err)
	}
}
