// Package rpc is the service contract: the core's verb sets served by
// connect-go, so one handler answers Connect, gRPC and gRPC-Web on one port.
//
// Generated types stop here. Every method turns a request into the domain's
// terms, calls one verb, and turns the answer back; nothing it calls sees a
// protobuf message.
package rpc

import (
	"context"
	"errors"
	"io/fs"
	"net/http"

	"connectrpc.com/connect"

	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/gen/nerve/resource/v1/resourcev1connect"
)

// Resources returns the path and handler serving store as ResourceService.
// maxBytes bounds a message in either direction, so a document larger than
// that is refused on the way in and on the way out.
func Resources(store *resource.Store, maxBytes int) (string, http.Handler) {
	return resourcev1connect.NewResourceServiceHandler(
		&resources{store: store, maxBytes: maxBytes},
		connect.WithReadMaxBytes(maxBytes),
		connect.WithSendMaxBytes(maxBytes),
	)
}

// Protocols are what a server carrying the handler speaks: HTTP/1.1 for a
// browser and unencrypted HTTP/2 for a gRPC client, on the same port.
func Protocols() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	return p
}

// errorOf gives err the code a caller classifies it by. A sentinel the core
// reports keeps its meaning across the wire; anything else is INTERNAL, which a
// client does not retry.
func errorOf(err error) error {
	return connect.NewError(codeOf(err), err)
}

func codeOf(err error) connect.Code {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return connect.CodeNotFound
	case errors.Is(err, fs.ErrExist):
		return connect.CodeAlreadyExists
	case errors.Is(err, fs.ErrPermission):
		return connect.CodePermissionDenied
	case errors.Is(err, fs.ErrInvalid):
		return connect.CodeInvalidArgument
	case errors.Is(err, stability.ErrUnavailable):
		return connect.CodeUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		return connect.CodeDeadlineExceeded
	case errors.Is(err, context.Canceled):
		return connect.CodeCanceled
	}
	return connect.CodeInternal
}
