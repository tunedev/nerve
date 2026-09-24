package rpc

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"connectrpc.com/connect"

	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/workspace"
	resourcev1 "github.com/tunedev/nerve/internal/gen/nerve/resource/v1"
)

// resources is ResourceService over one resource.Store, one verb per method.
type resources struct {
	store    *resource.Store
	maxBytes int
}

func (r *resources) Create(ctx context.Context, req *resourcev1.CreateRequest) (*resourcev1.CreateResponse, error) {
	made, err := r.store.Create(ctx, scopeOf(req.GetScope()), req.GetName(), req.GetContentType())
	if err != nil {
		return nil, errorOf(err)
	}
	return &resourcev1.CreateResponse{Resource: messageOf(made)}, nil
}

func (r *resources) Upload(ctx context.Context, req *resourcev1.UploadRequest) (*resourcev1.UploadResponse, error) {
	written, err := r.store.Upload(ctx, scopeOf(req.GetScope()), req.GetName(), bytes.NewReader(req.GetContent()))
	if err != nil {
		return nil, errorOf(err)
	}
	return &resourcev1.UploadResponse{Written: written}, nil
}

func (r *resources) Complete(ctx context.Context, req *resourcev1.CompleteRequest) (*resourcev1.CompleteResponse, error) {
	done, err := r.store.Complete(ctx, scopeOf(req.GetScope()), req.GetName())
	if err != nil {
		return nil, errorOf(err)
	}
	return &resourcev1.CompleteResponse{Resource: messageOf(done)}, nil
}

// Content reads at most one byte past the bound, so content too large to send
// is refused before the whole of it is held.
func (r *resources) Content(ctx context.Context, req *resourcev1.ContentRequest) (*resourcev1.ContentResponse, error) {
	stream, err := r.store.Content(ctx, scopeOf(req.GetScope()), req.GetName())
	if err != nil {
		return nil, errorOf(err)
	}
	defer func() { _ = stream.Close() }()

	content, err := io.ReadAll(io.LimitReader(stream, int64(r.maxBytes)+1))
	if err != nil {
		return nil, errorOf(err)
	}
	if len(content) > r.maxBytes {
		return nil, connect.NewError(connect.CodeResourceExhausted,
			fmt.Errorf("resource %q is larger than %d bytes", req.GetName(), r.maxBytes))
	}
	return &resourcev1.ContentResponse{Content: content}, nil
}

// scopeOf is the one place a wire scope becomes a domain one.
func scopeOf(s *resourcev1.Scope) workspace.Scope {
	return workspace.Scope{User: s.GetUser(), Workspace: s.GetWorkspace()}
}

// messageOf is the one place a domain resource becomes a wire one.
func messageOf(res resource.Resource) *resourcev1.Resource {
	return &resourcev1.Resource{
		Name:        res.Name,
		ContentType: res.ContentType,
		Size:        res.Size,
		Digest:      res.Digest.String(),
		Complete:    res.Complete,
	}
}
