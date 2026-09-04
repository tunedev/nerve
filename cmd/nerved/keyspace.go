package main

import (
	"fmt"
	"io/fs"
	"net/url"
	"strings"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
)

// regionQuery is where the URL carries what a signature needs and a bucket's
// address does not: the region is not part of reaching the bucket path-style,
// but every S3 signature is made for one.
const regionQuery = "region"

// corpus builds the one store every vectorfs mount keeps its documents in. It
// is here for the reason keyspace is: the composition root is the only place
// that knows both a corpus and the disk or the bucket under it, and a store two
// mounts share is owned where the things two mounts share are owned.
//
// The scheme is the decision. A file:// URL is a directory on this host, which
// is the laptop with no bucket; anything else is the same path-style bucket URL
// the keyspace takes. An empty URL is a build that keeps no documents, and the
// kind refuses a mount over that rather than booting one that could answer
// nothing.
func corpus(rawURL string) (vectorfs.DocStore, error) {
	if rawURL == "" {
		return nil, nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("doc-store-url %q: %w", rawURL, fs.ErrInvalid)
	}
	if parsed.Scheme == "file" {
		docs, err := vectorfs.OpenLocal(parsed.Path)
		if err != nil {
			return nil, fmt.Errorf("doc-store-url: %w", err)
		}
		return docs, nil
	}

	cfg, err := bucket("doc-store-url", rawURL)
	if err != nil {
		return nil, err
	}
	store, err := objectfs.NewS3(cfg)
	if err != nil {
		return nil, fmt.Errorf("doc-store-url: %w", err)
	}
	return vectorfs.NewObjectDocs(store), nil
}

// keyspace builds the one store every kvfs mount is a namespaced part of. It is
// here because the composition root is the only place that knows both a
// keyspace and the object store under it, and because a store two mounts share
// is owned where the things two mounts share are owned.
//
// An empty URL is the keyspace in memory. That is the laptop with no bucket,
// and it is why the durable store is a setting rather than a build.
func keyspace(rawURL string) (kvfs.Store, error) {
	if rawURL == "" {
		return kvfs.NewMemStore(), nil
	}

	cfg, err := bucket("kv-store-url", rawURL)
	if err != nil {
		return nil, err
	}
	store, err := objectfs.NewS3(cfg)
	if err != nil {
		return nil, fmt.Errorf("kv-store-url: %w", err)
	}
	return kvfs.NewObjectStore(store), nil
}

// bucket reads one bucket's address out of the URL named by setting, which is
// what the URL is:
// addressing is path-style, so with the credentials taken out of the userinfo
// and the region out of the query, what is left is where the bucket answers.
//
// The shape is not checked again here. config.Validate has already refused a
// URL that names no bucket, no host or no credentials, and a second copy of
// those rules is a second place for them to disagree.
func bucket(setting, rawURL string) (objectfs.S3Config, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return objectfs.S3Config{}, fmt.Errorf("%s %q: %w", setting, rawURL, fs.ErrInvalid)
	}

	secret, _ := parsed.User.Password()
	region := parsed.Query().Get(regionQuery)
	if region == "" {
		region = objectfs.DefaultRegion
	}

	endpoint := url.URL{Scheme: parsed.Scheme, Host: parsed.Host}
	return objectfs.S3Config{
		Endpoint: endpoint.String(),
		Bucket:   strings.Trim(parsed.Path, "/"),
		Region:   region,
		KeyID:    parsed.User.Username(),
		Secret:   secret,
	}, nil
}
