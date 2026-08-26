package config

import "time"

// CallTimeoutDuration is the call-timeout setting in the vocabulary of the code
// that uses it: the deadline every remote call gets. The string spelling stops
// here, the way the JSON spelling of a mount stops at MountSpecs.
//
// Load has already validated it, so a Config that came from Load parses. One
// built by hand and never validated resolves to zero, which expires every call
// it bounds — which is why Load is the way in.
func (c Config) CallTimeoutDuration() time.Duration {
	d, _ := time.ParseDuration(c.CallTimeout)
	return d
}
