package rpc_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/tunedev/nerve/internal/adapters/inbound/rpc"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/vfs"
	resourcev1 "github.com/tunedev/nerve/internal/gen/nerve/resource/v1"
	"github.com/tunedev/nerve/internal/gen/nerve/resource/v1/resourcev1connect"
)

const (
	root     = "/resources"
	maxBytes = 1 << 10
)

var scope = &resourcev1.Scope{User: "ada", Workspace: "hunt"}

func newStore(t *testing.T) *resource.Store {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	signer, err := resource.NewSigner([]byte("a key nobody else has"), time.Now)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	store, err := resource.NewStore(table, root, signer)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// serve starts store behind the handler on a server speaking what the
// composition root's does.
func serve(t *testing.T, store *resource.Store, bound int) string {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(rpc.Resources(store, bound))
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.Protocols = rpc.Protocols()
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

// client speaks one protocol over one HTTP version. A transport is closed with
// the test, so no connection outlives it.
func client(t *testing.T, base string, h2c bool, opts ...connect.ClientOption) resourcev1connect.ResourceServiceClient {
	t.Helper()

	transport := &http.Transport{Protocols: new(http.Protocols)}
	if h2c {
		transport.Protocols.SetUnencryptedHTTP2(true)
	} else {
		transport.Protocols.SetHTTP1(true)
	}
	t.Cleanup(transport.CloseIdleConnections)
	return resourcev1connect.NewResourceServiceClient(&http.Client{Transport: transport}, base, opts...)
}

func put(t *testing.T, c resourcev1connect.ResourceServiceClient, name string, content []byte) *resourcev1.Resource {
	t.Helper()
	ctx := t.Context()

	if _, err := c.Create(ctx, &resourcev1.CreateRequest{Scope: scope, Name: name, ContentType: "text/plain"}); err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	if _, err := c.Upload(ctx, &resourcev1.UploadRequest{Scope: scope, Name: name, Content: content}); err != nil {
		t.Fatalf("Upload(%q): %v", name, err)
	}
	done, err := c.Complete(ctx, &resourcev1.CompleteRequest{Scope: scope, Name: name})
	if err != nil {
		t.Fatalf("Complete(%q): %v", name, err)
	}
	return done.GetResource()
}

func TestDocumentRoundTripsOverEveryProtocol(t *testing.T) {
	base := serve(t, newStore(t), maxBytes)
	document := []byte("a document put in from outside the process")

	for _, tc := range []struct {
		protocol string
		resource string
		client   resourcev1connect.ResourceServiceClient
	}{
		{"connect json over http/1.1", "connect.txt", client(t, base, false, connect.WithProtoJSON())},
		{"grpc over h2c", "grpc.txt", client(t, base, true, connect.WithGRPC())},
		{"grpc-web over http/1.1", "grpc-web.txt", client(t, base, false, connect.WithGRPCWeb())},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			c, name := tc.client, tc.resource
			done := put(t, c, name, document)
			if !done.GetComplete() || done.GetSize() != int64(len(document)) || !strings.HasPrefix(done.GetDigest(), "sha256:") {
				t.Fatalf("Complete = %v, want complete, %d bytes, a sha256 digest", done, len(document))
			}

			got, err := c.Content(t.Context(), &resourcev1.ContentRequest{Scope: scope, Name: name})
			if err != nil {
				t.Fatalf("Content: %v", err)
			}
			if !bytes.Equal(got.GetContent(), document) {
				t.Fatalf("Content = %q, want %q", got.GetContent(), document)
			}
		})
	}
}

func TestErrorsKeepTheirCode(t *testing.T) {
	store := newStore(t)
	c := client(t, serve(t, store, maxBytes), false)
	// The request for "taken" fits under this bound and its content does not.
	small := client(t, serve(t, store, 64), false)
	ctx := t.Context()

	put(t, c, "taken", make([]byte, 65))
	if _, err := c.Create(ctx, &resourcev1.CreateRequest{Scope: scope, Name: "pending"}); err != nil {
		t.Fatalf("Create(pending): %v", err)
	}

	cases := map[string]struct {
		call func() error
		want connect.Code
	}{
		"a name already taken": {
			func() error {
				_, err := c.Create(ctx, &resourcev1.CreateRequest{Scope: scope, Name: "taken"})
				return err
			},
			connect.CodeAlreadyExists,
		},
		"content not yet completed": {
			func() error {
				_, err := c.Content(ctx, &resourcev1.ContentRequest{Scope: scope, Name: "pending"})
				return err
			},
			connect.CodeNotFound,
		},
		"a user naming another": {
			func() error {
				_, err := c.Create(ctx, &resourcev1.CreateRequest{Scope: &resourcev1.Scope{User: "ada/../bob", Workspace: "hunt"}, Name: "x"})
				return err
			},
			connect.CodeInvalidArgument,
		},
		"an upload over the bound": {
			func() error {
				_, err := c.Upload(ctx, &resourcev1.UploadRequest{Scope: scope, Name: "pending", Content: make([]byte, maxBytes+1)})
				return err
			},
			connect.CodeResourceExhausted,
		},
		"content over the bound": {
			func() error {
				_, err := small.Content(ctx, &resourcev1.ContentRequest{Scope: scope, Name: "taken"})
				return err
			},
			connect.CodeResourceExhausted,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := connect.CodeOf(tc.call()); got != tc.want {
				t.Fatalf("code = %v, want %v", got, tc.want)
			}
		})
	}
}
