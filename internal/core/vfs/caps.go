package vfs

// Caps is what a mount says about itself at one path.
//
// Capability varies inside a single mount — a queue's job path is consumed by
// reading while the config beside it is an ordinary file — so it is data
// resolved per path rather than a type a caller can assert on. Assertion would
// interrogate the mount dispatcher and never the plugin that would do the work.
//
// The zero value is a plain read-write tree, which is what most storage kinds
// are. A plugin sets only what is true of it.
type Caps struct {
	// AppendOnly means Write adds to the end of path. What is already there is
	// never replaced or shortened.
	AppendOnly bool

	// ReadDestructive means Read consumes: what one reader receives is gone for
	// the next. A queue job path is the case.
	ReadDestructive bool

	// ReadOnly means every mutating verb fails with fs.ErrPermission.
	ReadOnly bool

	// Broadcast means every reader receives every entry, rather than readers
	// competing for each one.
	Broadcast bool

	// ObjectStore means names are prefixes, not a tree: no directory exists
	// apart from the entries under it, and Mkdir has nothing to make.
	ObjectStore bool
}
