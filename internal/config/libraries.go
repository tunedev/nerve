package config

import "github.com/tunedev/nerve/internal/core/library"

// Library is one line of the libraries array: which document kind the library
// holds, the file that makes a directory one of those documents, and the root
// in the tree they are kept under.
//
// It comes from the file layer alone, for the reason a mount does: a library
// table is a list, and neither an environment variable nor a flag carries one.
type Library struct {
	Kind string `json:"kind"`
	File string `json:"file"`
	Root string `json:"root"`
}

// LibrarySpecs is the library config in the document domain's vocabulary, which
// is what a catalog is built from. The JSON spelling stops here.
func (c Config) LibrarySpecs() []library.Spec {
	specs := make([]library.Spec, len(c.Libraries))
	for i, l := range c.Libraries {
		specs[i] = library.Spec{Kind: library.Kind{Name: l.Kind, Filename: l.File}, Root: l.Root}
	}
	return specs
}
