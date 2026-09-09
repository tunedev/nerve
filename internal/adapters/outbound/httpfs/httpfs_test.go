package httpfs_test

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/httpfs"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// origin serves a fixed set of paths and 404s everything else, which is what an
// HTTP origin looks like to a mount over it.
func origin(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, served := files[r.URL.Path]
		if !served {
			http.Error(w, "not here", http.StatusNotFound)
			return
		}
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		http.ServeContent(w, r, r.URL.Path, time.Time{}, strings.NewReader(content))
	}))
}

func mustNew(t *testing.T, server *httptest.Server) *httpfs.FS {
	t.Helper()
	fsys, err := httpfs.New(server.URL, 1<<20, server.Client())
	if err != nil {
		t.Fatalf("New(%q) = %v", server.URL, err)
	}
	t.Cleanup(func() {
		fsys.CloseIdleConnections()
		server.Close()
	})
	return fsys
}

func TestStatDescribesWhatTheOriginServes(t *testing.T) {
	fsys := mustNew(t, origin(t, map[string]string{"/notes": "text on the far end"}))

	info, err := fsys.Stat(t.Context(), "/notes")
	if err != nil {
		t.Fatalf("Stat(/notes) = %v", err)
	}
	if info.Name != "notes" {
		t.Errorf("Stat(/notes).Name = %q, want %q", info.Name, "notes")
	}
	if info.Size != int64(len("text on the far end")) {
		t.Errorf("Stat(/notes).Size = %d, want %d", info.Size, len("text on the far end"))
	}
	if info.ModTime.IsZero() {
		t.Error("Stat(/notes).ModTime is zero, want what Last-Modified said")
	}
}

func TestReadFetchesWhatTheOriginServes(t *testing.T) {
	fsys := mustNew(t, origin(t, map[string]string{"/deep/notes": "text"}))

	content, err := fsys.Read(t.Context(), "/deep/notes")
	if err != nil {
		t.Fatalf("Read(/deep/notes) = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("Read(/deep/notes) = %q, want %q", content, "text")
	}
}

// A path the origin does not serve is a path that does not exist, in the
// vocabulary a caller of the port already matches on.
func TestAPathTheOriginRefusesDoesNotExist(t *testing.T) {
	fsys := mustNew(t, origin(t, nil))

	if _, err := fsys.Read(t.Context(), "/absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/absent) = %v, want fs.ErrNotExist", err)
	}
	if _, err := fsys.Stat(t.Context(), "/absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(/absent) = %v, want fs.ErrNotExist", err)
	}
}

// A status a caller cannot act on but another attempt might is marked
// transient, at the plugin's edge, so the retry wrapper does not have to learn
// HTTP.
func TestAServerFailureIsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "later", http.StatusServiceUnavailable)
	}))
	fsys := mustNew(t, server)

	_, err := fsys.Read(t.Context(), "/notes")
	if !errors.Is(err, stability.ErrTransient) {
		t.Errorf("Read against a 503 = %v, want stability.ErrTransient", err)
	}
	if stability.Classify(err) != stability.Retryable {
		t.Errorf("Classify(503) = %v, want retryable", stability.Classify(err))
	}
}

func TestARefusedPathIsAPermission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))

	if _, err := mustNew(t, server).Read(t.Context(), "/notes"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Read against a 403 = %v, want fs.ErrPermission", err)
	}
}

// The origin is somebody else's, so how much it sends is not this process's
// decision to leave open. A response past the bound fails rather than being
// handed back short, which a caller could not tell from a small file.
func TestAResponsePastTheBoundFails(t *testing.T) {
	server := origin(t, map[string]string{"/big": "far more than eight bytes"})
	fsys, err := httpfs.New(server.URL, 8, server.Client())
	if err != nil {
		t.Fatalf("New(%q) = %v", server.URL, err)
	}
	t.Cleanup(func() {
		fsys.CloseIdleConnections()
		server.Close()
	})

	if _, err := fsys.Read(t.Context(), "/big"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Read of a response past the bound = %v, want fs.ErrInvalid", err)
	}
}

// A body is already a stream, so this is the one optional verb set an origin
// answers rather than invents.
func TestOpenReadStreamsTheBody(t *testing.T) {
	fsys := mustNew(t, origin(t, map[string]string{"/notes": "streamed"}))

	stream := fsys.AsStreamer()
	if stream == nil {
		t.Fatal("AsStreamer() = nil, want the streaming verbs")
	}
	reader, err := stream.OpenRead(t.Context(), "/notes")
	if err != nil {
		t.Fatalf("OpenRead(/notes) = %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading the stream = %v", err)
	}
	if string(content) != "streamed" {
		t.Errorf("the stream held %q, want %q", content, "streamed")
	}
}

// Names on an origin are prefixes rather than a tree, and the descriptor says
// so before a caller asks for a walk that cannot be done.
func TestCapabilitiesClaimAReadOnlyObjectStore(t *testing.T) {
	fsys := mustNew(t, origin(t, nil))

	want := vfs.Caps{ReadOnly: true, ObjectStore: true}
	for _, name := range []string{"/", "/notes", "/deep/notes"} {
		if caps := fsys.Capabilities(name); caps != want {
			t.Errorf("Capabilities(%q) = %+v, want %+v", name, caps, want)
		}
	}
}

func TestReadDirHasNothingToList(t *testing.T) {
	if _, err := mustNew(t, origin(t, nil)).ReadDir(t.Context(), "/"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadDir(/) = %v, want fs.ErrInvalid", err)
	}
}

// A misspelled origin is a boot failure rather than the first read's.
func TestNewRefusesAnOriginItCannotReach(t *testing.T) {
	for _, base := range []string{"", "notaurl", "ftp://example.invalid", "http://"} {
		if _, err := httpfs.New(base, 1<<20, nil); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("New(%q) = %v, want fs.ErrInvalid", base, err)
		}
	}
}

func TestFactoryNeedsAnOrigin(t *testing.T) {
	if _, err := httpfs.Factory(nil); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(nil) = %v, want fs.ErrInvalid", err)
	}
}

func TestFactoryRefusesASettingItCannotHonour(t *testing.T) {
	settings := map[string]string{"base": "http://example.invalid", "root": "/srv"}
	if _, err := httpfs.Factory(settings); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(root) = %v, want fs.ErrInvalid", err)
	}
}
