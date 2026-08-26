package objectfs

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// modeHeader and modTimeHeader are where an object keeps what a bucket has no
// field for. A store records its own last-modified time to the second, which is
// coarse enough for two writes in a row to read as one; the port promises a
// caller can tell them apart, so the write records the instant itself.
const (
	modeHeader    = "X-Amz-Meta-Nerve-Mode"
	modTimeHeader = "X-Amz-Meta-Nerve-Modtime"
)

// S3Config is what it takes to reach one bucket. Endpoint is the store's base
// URL — a MinIO container locally, a hosted store otherwise — and addressing is
// always path-style, because a laptop has no DNS entry per bucket.
type S3Config struct {
	Endpoint string
	Bucket   string
	Region   string
	KeyID    string
	Secret   string
}

// S3 is a bucket in a store that speaks the S3 wire, which is what MinIO serves
// locally and what a hosted object store serves in the cloud. It is the reason
// the substitution in the platform's table is a setting rather than a rewrite.
//
// No timeout is set on the client. Every call is made on the caller's context,
// and the registry has already bounded that: a second timeout here would be a
// second layer deciding how long a call may take, which is the one thing the
// tenets say must have exactly one owner.
type S3 struct {
	base   *url.URL
	bucket string
	creds  credentials
	client *http.Client
}

// NewS3 returns a bucket client for cfg. It resolves the endpoint at
// construction so a misspelled URL is a boot failure rather than the first
// call's.
func NewS3(cfg S3Config) (*S3, error) {
	base, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("endpoint %q: %w", cfg.Endpoint, fs.ErrInvalid)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("endpoint %q: want a scheme and a host: %w", cfg.Endpoint, fs.ErrInvalid)
	}

	return &S3{
		base:   base,
		bucket: cfg.Bucket,
		creds:  credentials{keyID: cfg.KeyID, secret: cfg.Secret, region: cfg.Region},
		client: &http.Client{},
	}, nil
}

// Head describes key without fetching it.
func (s *S3) Head(ctx context.Context, key string) (Object, error) {
	response, err := s.do(ctx, http.MethodHead, s.object(key), nil, nil, nil)
	if err != nil {
		return Object{}, err
	}
	defer drain(response)

	return s.describe(key, response.Header), nil
}

// Get returns the content of key as a stream the caller closes, so an object
// larger than memory is read as it arrives rather than held whole.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	response, err := s.do(ctx, http.MethodGet, s.object(key), nil, nil, nil)
	if err != nil {
		return nil, Object{}, err
	}

	return response.Body, s.describe(key, response.Header), nil
}

// Put stores data under key, replacing whatever was there, and records the mode
// and the instant beside it.
func (s *S3) Put(ctx context.Context, key string, data []byte, mode fs.FileMode) error {
	response, err := s.do(ctx, http.MethodPut, s.object(key), nil, s.metadata(mode, time.Now()), data)
	if err != nil {
		return err
	}
	defer drain(response)

	return nil
}

// Copy stores what is under from a second time under to, carrying the instant
// from recorded and putting mode in place of the mode it carried.
//
// The store does the copying, so the content never travels through this
// process. Reading from first is what makes the instant survive: a copy that
// replaces the metadata replaces all of it, so what is kept has to be sent.
func (s *S3) Copy(ctx context.Context, from, to string, mode fs.FileMode) error {
	source, err := s.Head(ctx, from)
	if err != nil {
		return err
	}

	headers := s.metadata(mode, source.ModTime)
	headers.Set("X-Amz-Copy-Source", escapePath("/"+s.bucket+"/"+from))
	headers.Set("X-Amz-Metadata-Directive", "REPLACE")

	response, err := s.do(ctx, http.MethodPut, s.object(to), nil, headers, nil)
	if err != nil {
		return err
	}
	defer drain(response)

	return nil
}

// Delete removes key. A key that is not there is not an error, which is what
// the wire already says: S3 answers a delete of nothing with success.
func (s *S3) Delete(ctx context.Context, key string) error {
	response, err := s.do(ctx, http.MethodDelete, s.object(key), nil, nil, nil)
	if err != nil {
		return err
	}
	defer drain(response)

	return nil
}

// List returns what sits under prefix, following the store's continuation
// tokens to the end. A listing is one answer to the caller above, so the paging
// stops here rather than becoming something every caller has to remember.
func (s *S3) List(ctx context.Context, prefix, delimiter string) (Listing, error) {
	listing := Listing{Objects: nil, Prefixes: nil}

	for token := ""; ; {
		page, err := s.page(ctx, prefix, delimiter, token)
		if err != nil {
			return Listing{}, err
		}

		for _, entry := range page.Contents {
			listing.Objects = append(listing.Objects, Object{
				Key:     entry.Key,
				Size:    entry.Size,
				Mode:    fileMode,
				ModTime: entry.LastModified,
			})
		}
		for _, common := range page.CommonPrefixes {
			listing.Prefixes = append(listing.Prefixes, common.Prefix)
		}

		if !page.IsTruncated || page.NextContinuationToken == "" {
			return listing, nil
		}
		token = page.NextContinuationToken
	}
}

// bucketListing is the shape of one ListObjectsV2 answer.
type bucketListing struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

// page fetches one ListObjectsV2 answer.
func (s *S3) page(ctx context.Context, prefix, delimiter, token string) (bucketListing, error) {
	query := map[string]string{"list-type": "2", "prefix": prefix, "delimiter": delimiter}
	if token != "" {
		query["continuation-token"] = token
	}

	response, err := s.do(ctx, http.MethodGet, "/"+s.bucket, query, nil, nil)
	if err != nil {
		return bucketListing{}, err
	}
	defer drain(response)

	var page bucketListing
	if err := xml.NewDecoder(response.Body).Decode(&page); err != nil {
		return bucketListing{}, fmt.Errorf("list %q: %w", prefix, err)
	}
	return page, nil
}

// object is the path one key sits at, path-style under the bucket.
func (s *S3) object(key string) string { return "/" + s.bucket + "/" + key }

// metadata is what a write records beside the content.
func (s *S3) metadata(mode fs.FileMode, when time.Time) http.Header {
	headers := http.Header{}
	headers.Set(modeHeader, strconv.FormatUint(uint64(mode.Perm()), 8))
	headers.Set(modTimeHeader, when.UTC().Format(time.RFC3339Nano))
	return headers
}

// describe reads back what a write recorded. An object another tool stored
// carries neither, and reads back with the default mode and whatever the store
// itself last noted.
func (s *S3) describe(key string, headers http.Header) Object {
	size, err := strconv.ParseInt(headers.Get("Content-Length"), 10, 64)
	if err != nil {
		size = 0
	}

	mode := fs.FileMode(fileMode)
	if recorded, err := strconv.ParseUint(headers.Get(modeHeader), 8, 32); err == nil {
		mode = fs.FileMode(recorded).Perm()
	}

	when, err := time.Parse(time.RFC3339Nano, headers.Get(modTimeHeader))
	if err != nil {
		when, _ = http.ParseTime(headers.Get("Last-Modified"))
	}

	return Object{Key: key, Size: size, Mode: mode, ModTime: when}
}

// do signs one request and makes it, turning the store's status codes into the
// vocabulary the port above speaks.
func (s *S3) do(ctx context.Context, method, path string, query map[string]string, headers http.Header, body []byte) (*http.Response, error) {
	// Path and RawPath are set together so what is sent on the wire is exactly
	// what the signature covers. Leaving RawPath empty lets url.URL escape a
	// key by its own rules, which are not the signature's.
	target := *s.base
	target.Path = path
	target.RawPath = escapePath(path)
	target.RawQuery = canonicalQuery(query)

	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	request.ContentLength = int64(len(body))
	for name, values := range headers {
		request.Header[name] = values
	}
	sign(request, s.creds, body, time.Now())

	response, err := s.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %w", method, path, err, vfs.ErrTransient)
	}
	if response.StatusCode >= http.StatusBadRequest {
		defer drain(response)
		return nil, statusError(method, path, response)
	}
	return response, nil
}

// statusError translates one refusal. What a caller does next depends on the
// class, so the transient mark goes on here, at the plugin's edge, rather than
// leaving the classifier to learn a wire's vocabulary.
func statusError(method, path string, response *http.Response) error {
	where := fmt.Sprintf("%s %s: %s", method, path, response.Status)

	switch {
	case response.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%s: %w", where, fs.ErrNotExist)
	case response.StatusCode == http.StatusForbidden, response.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%s: %w", where, fs.ErrPermission)
	case response.StatusCode == http.StatusTooManyRequests, response.StatusCode >= http.StatusInternalServerError:
		return fmt.Errorf("%s: %w", where, vfs.ErrTransient)
	default:
		return fmt.Errorf("%s: %s: %w", where, detail(response), fs.ErrInvalid)
	}
}

// detail is what the store said went wrong, which S3 puts in an XML body.
func detail(response *http.Response) string {
	var refusal struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if err := xml.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&refusal); err != nil {
		return "no detail"
	}
	return strings.TrimSpace(refusal.Code + " " + refusal.Message)
}

// canonicalQuery spells the query the way the signature expects it: sorted by
// key, every part percent-encoded. It is built here rather than by url.Values
// so what is signed and what is sent cannot drift apart.
func canonicalQuery(query map[string]string) string {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, escape(key)+"="+escape(query[key]))
	}
	return strings.Join(parts, "&")
}

// drain reads what is left of a response and closes it, so the connection goes
// back to the pool instead of being dropped.
func drain(response *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	_ = response.Body.Close()
}
