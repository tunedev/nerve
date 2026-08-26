// Package admin is the operator surface: plain HTTP with a JSON body, read
// only, answering what this process actually resolved.
//
// It is not the service contract. Service-to-service traffic is gRPC generated
// from a .proto; this is what someone curls at 3am, so it is a URL and a
// document and nothing more.
package admin

import (
	"encoding/json"
	"net/http"

	"github.com/tunedev/nerve/internal/config"
)

// Handler answers GET /config with the effective config.
//
// It takes the redacted view rather than the Config it came from, so the
// surface has no unredacted value to leak by accident.
//
// The view is a value because config is resolved once, at startup: a surface
// that re-read it would answer a different question from the one the process
// booted on.
func Handler(effective config.Effective) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, effective)
	})
	return mux
}

// writeJSON encodes before it writes a header, so a failure can still be
// reported as one.
func writeJSON(w http.ResponseWriter, body any) {
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		http.Error(w, "nerved: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	// A client that hung up mid-answer is not this handler's problem.
	_, _ = w.Write(append(data, '\n'))
}
