// Package resource is what a workspace uploads: a file declared first, streamed
// in second, and finalised third, so that content larger than memory never has
// to arrive in one call.
//
// The three steps are separate because they fail separately. Declaring records
// what is coming and reserves the name; uploading moves the bytes and is the
// part that is long enough to be interrupted; finalising is where the content
// becomes readable under its own digest. Nothing reads a resource until that
// last step, so an upload that stopped halfway leaves a resource with no
// content rather than a truncated one.
//
// A download is a grant rather than a call. The store signs a token naming one
// resource until one moment, and whoever holds it reads that content and
// nothing else — which is what lets a workspace hand a file to something that
// has no other business inside it.
//
// The store keeps nothing itself. It reads and writes the one FileSystem the
// mount table presents, at paths derived from the scope that owns them, so
// whether a resource sits on a disk, in an object store or in memory is a mount
// rather than a decision made here.
package resource

import (
	"github.com/tunedev/nerve/internal/core/cas"
)

// Resource is what the store knows about one upload.
//
// Size and Digest are what finalising established, so both are zero until
// Complete has run. Digest is the content's own name: two workspaces that
// uploaded the same file hold resources that say so.
type Resource struct {
	Name        string
	ContentType string
	Size        int64
	Digest      cas.Digest
	Complete    bool
}

// meta is what stands beside the content, spelling a resource in the terms a
// file can hold. It is separate from Resource so that a digest is written as
// the spelling cas already reads back rather than as its fields.
type meta struct {
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest,omitempty"`
	Complete    bool   `json:"complete"`
}
