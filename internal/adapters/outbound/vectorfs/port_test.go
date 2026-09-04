package vectorfs_test

import (
	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// vectorfs answers the port a mount table dispatches to, which is the only
// reason it can be mounted at all.
var _ vfs.FileSystem = vectorfs.New("test", 1, 1, nil, nil, nil)

// The DocStore the plugin keeps its documents in is a port, and it has two
// implementations: a directory on the local disk and a corpus in an object
// store. Both run the same cases in docstore_test.go, which is what makes it a
// port rather than an interface in front of one struct.
var (
	_ vectorfs.DocStore = (*vectorfs.LocalDocs)(nil)
	_ vectorfs.DocStore = vectorfs.NewObjectDocs(nil)
)

// The Index beside it is the other port, and pgvector is the side the platform
// ships. The ranking in a map that the offline cases run against is the second.
var (
	_ vectorfs.Index = (*vectorfs.Postgres)(nil)
	_ vectorfs.Index = newMemIndex()
)
