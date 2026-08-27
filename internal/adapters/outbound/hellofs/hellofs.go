// Package hellofs answers one path with one message. It stores nothing, reaches
// nothing and can fail in no way a mount table has to think about, which is the
// point: it is the multimeter. Mounting it proves the config layer, the
// registry, the mount table and the wrappers round every plugin all work,
// without a database, a broker or a network in the way.
package hellofs

import "github.com/tunedev/nerve/internal/adapters/outbound/synth"

// Greeting is the one name this mount answers for.
const Greeting = "greeting"

// New returns a mount holding one entry, whose content is message.
func New(message string) *synth.Tree {
	return synth.NewTree(map[string]synth.Entry{Greeting: synth.Fixed(message)})
}
