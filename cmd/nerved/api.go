package main

import (
	"net/http"
	"time"

	"github.com/tunedev/nerve/internal/adapters/inbound/rpc"
	"github.com/tunedev/nerve/internal/core/resource"
)

// The service contract moves whole documents in one message, so these bound a
// document as well as a client.
const (
	apiMaxBytes     = 8 << 20
	apiWriteTimeout = time.Minute
)

// apiServer is the service contract on addr, serving resources. A build that
// names no address serves nothing and gets nil.
func apiServer(addr string, resources *resource.Store) *http.Server {
	if addr == "" {
		return nil
	}

	mux := http.NewServeMux()
	mux.Handle(rpc.Resources(resources, apiMaxBytes))
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		Protocols:         rpc.Protocols(),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      apiWriteTimeout,
		IdleTimeout:       idleTimeout,
	}
}
