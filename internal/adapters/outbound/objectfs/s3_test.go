package objectfs_test

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/core/stability"
)

// What a store answers and what the plugin makes of it can be asserted without
// a store: these stand a server in front of the client and read the requests it
// sends and the errors it makes of the replies. That the signature is one a
// real store accepts is what the MinIO cases prove; nothing here can.

// served returns a bucket talking to handler.
func served(t *testing.T, handler http.HandlerFunc) objectfs.Bucket {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Cleanup(http.DefaultTransport.(*http.Transport).CloseIdleConnections)

	bucket, err := objectfs.NewS3(objectfs.S3Config{
		Endpoint: server.URL,
		Bucket:   "nerve",
		Region:   "us-east-1",
		KeyID:    "key",
		Secret:   "secret",
	})
	if err != nil {
		t.Fatalf("NewS3 = %v", err)
	}
	return bucket
}

// A store returns a listing in pages, and a caller above asked one question.
// Following the tokens here is what keeps a bucket with more keys than one page
// from reading as a bucket with one page of keys.
func TestListFollowsTheStoresContinuationTokens(t *testing.T) {
	pages := map[string]string{
		"": `<ListBucketResult><IsTruncated>true</IsTruncated>
			<NextContinuationToken>second</NextContinuationToken>
			<Contents><Key>jobs/first</Key><Size>4</Size></Contents></ListBucketResult>`,
		"second": `<ListBucketResult><IsTruncated>false</IsTruncated>
			<Contents><Key>jobs/last</Key><Size>9</Size></Contents>
			<CommonPrefixes><Prefix>jobs/queued/</Prefix></CommonPrefixes></ListBucketResult>`,
	}

	bucket := served(t, func(w http.ResponseWriter, r *http.Request) {
		page, known := pages[r.URL.Query().Get("continuation-token")]
		if !known {
			t.Errorf("continuation-token = %q, want one this test serves", r.URL.Query().Get("continuation-token"))
		}
		fmt.Fprint(w, page)
	})

	listing, err := bucket.List(t.Context(), "jobs/", "/")
	if err != nil {
		t.Fatalf("List = %v", err)
	}

	if len(listing.Objects) != 2 {
		t.Errorf("List returned %d objects, want both pages", len(listing.Objects))
	}
	if len(listing.Prefixes) != 1 {
		t.Errorf("List returned %d prefixes, want the one on the second page", len(listing.Prefixes))
	}
}

// A write records the mode and the instant beside the content, because a bucket
// has a field for neither and the port above promises both.
func TestPutRecordsTheModeAndTheInstant(t *testing.T) {
	var recorded http.Header
	bucket := served(t, func(_ http.ResponseWriter, r *http.Request) { recorded = r.Header.Clone() })

	if err := bucket.Put(t.Context(), "notes", []byte("stored"), 0o600); err != nil {
		t.Fatalf("Put = %v", err)
	}

	if mode := recorded.Get("X-Amz-Meta-Nerve-Mode"); mode != "600" {
		t.Errorf("recorded mode = %q, want %q", mode, "600")
	}
	if when := recorded.Get("X-Amz-Meta-Nerve-Modtime"); when == "" {
		t.Error("no instant recorded, want the one the write happened at")
	}
	if length := recorded.Get("Authorization"); length == "" {
		t.Error("no Authorization, want the request signed")
	}
}

// The copy is the store's: the content never travels through this process, and
// the metadata is replaced rather than carried, which is how a mode changes
// without a write.
func TestCopyAsksTheStoreToDoItAndReplaceTheMetadata(t *testing.T) {
	var recorded http.Header
	bucket := served(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("X-Amz-Meta-Nerve-Mode", "644")
			w.Header().Set("Content-Length", strconv.Itoa(4))
			return
		}
		recorded = r.Header.Clone()
	})

	if err := bucket.Copy(t.Context(), "notes", "old-notes", 0o640); err != nil {
		t.Fatalf("Copy = %v", err)
	}

	if source := recorded.Get("X-Amz-Copy-Source"); source != "/nerve/notes" {
		t.Errorf("X-Amz-Copy-Source = %q, want %q", source, "/nerve/notes")
	}
	if directive := recorded.Get("X-Amz-Metadata-Directive"); directive != "REPLACE" {
		t.Errorf("X-Amz-Metadata-Directive = %q, want %q", directive, "REPLACE")
	}
	if mode := recorded.Get("X-Amz-Meta-Nerve-Mode"); mode != "640" {
		t.Errorf("recorded mode = %q, want the new one, %q", mode, "640")
	}
}

// A refusal is sorted at the plugin's edge, because only here is it known what
// the wire's numbers mean. Getting this wrong is a retry policy that hammers a
// store over a request it will always refuse, or gives up on one that was only
// busy.
func TestRefusalsCarryWhatARetryNeedsToKnow(t *testing.T) {
	refusals := []struct {
		status int
		want   error
		class  stability.Class
	}{
		{status: http.StatusNotFound, want: fs.ErrNotExist, class: stability.FailFast},
		{status: http.StatusForbidden, want: fs.ErrPermission, class: stability.FailFast},
		{status: http.StatusBadRequest, want: fs.ErrInvalid, class: stability.FailFast},
		{status: http.StatusServiceUnavailable, want: stability.ErrTransient, class: stability.Retryable},
		{status: http.StatusTooManyRequests, want: stability.ErrTransient, class: stability.Retryable},
	}

	for _, refusal := range refusals {
		t.Run(strconv.Itoa(refusal.status), func(t *testing.T) {
			bucket := served(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(refusal.status)
			})

			_, err := bucket.Head(t.Context(), "notes")
			if !errors.Is(err, refusal.want) {
				t.Errorf("Head against %d = %v, want %v", refusal.status, err, refusal.want)
			}
			if class := stability.Classify(err); class != refusal.class {
				t.Errorf("Classify = %v, want %v", class, refusal.class)
			}
		})
	}
}

// A misspelled setting is a boot failure rather than a line nobody reads. It is
// the last place an operator can still act on it.
func TestFactoryRefusesSettingsItCannotHonour(t *testing.T) {
	_, err := objectfs.Factory(map[string]string{
		"endpoint":          "http://127.0.0.1:9000",
		"bucket":            "nerve",
		"access_key_id":     "key",
		"secret_access_key": "secret",
		"secret_acces_key":  "secret",
	})

	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory with a misspelled setting = %v, want fs.ErrInvalid", err)
	}
}

// A mount with no credentials is refused at boot rather than at the first call,
// which is the difference between an operator seeing it and a caller seeing it.
func TestFactoryRefusesAMountItCannotReach(t *testing.T) {
	for _, settings := range []map[string]string{
		{"bucket": "nerve", "access_key_id": "key", "secret_access_key": "secret"},
		{"endpoint": "http://127.0.0.1:9000", "access_key_id": "key", "secret_access_key": "secret"},
		{"endpoint": "http://127.0.0.1:9000", "bucket": "nerve"},
		{"endpoint": "not-a-url", "bucket": "nerve", "access_key_id": "key", "secret_access_key": "secret"},
	} {
		if _, err := objectfs.Factory(settings); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Factory(%v) = %v, want fs.ErrInvalid", settings, err)
		}
	}
}
