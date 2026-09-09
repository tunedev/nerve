package main

import "github.com/tunedev/nerve/internal/core/vfs"

// grown holds the mount table for the mounts that read through it.
//
// A fusion mount is part of the tree it reads its rankings from, so the table
// cannot be handed to the factory that builds it: the table is what building
// every mount produces. This is the one indirection that breaks that ring, and
// nothing reads it until the table is there — a mount is built at boot and asks
// a question only once the server is answering.
//
// Its alternative was for a fusion to build private copies of the mounts it
// fuses, the way a proxy builds its target. That would give a graph mount two
// graphs: one the tree writes texts to, and one loaded from the blob at boot
// that never hears about them.
type grown struct{ table vfs.FileSystem }

// tree hands back the table every mount is in, which is what a fusion reads its
// sources through.
func (g *grown) tree() vfs.FileSystem { return g.table }
