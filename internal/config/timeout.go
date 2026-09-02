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

// DrainDelayDuration is the drain-delay setting in the vocabulary of the code
// that uses it: how long the process answers readiness with 503 before it
// stops accepting connections at all.
//
// The delay is what makes readiness worth asking. Close the listener the
// instant the signal arrives and nothing ever observes the refusal; whatever
// routes traffic here learns the process is gone by having a connection
// refused, which is the outage readiness exists to avoid.
func (c Config) DrainDelayDuration() time.Duration {
	d, _ := time.ParseDuration(c.DrainDelay)
	return d
}

// ProviderTimeoutDuration is the provider-timeout setting in the vocabulary of
// the code that uses it: the deadline one call to a model endpoint carries.
//
// It is the gateway's own rather than the one every mount is bounded by,
// because a model is slow in a way a filesystem is not, and a chain that fell
// through to its next endpoint on a deadline the first was always going to
// overrun would spend every endpoint it has.
func (c Config) ProviderTimeoutDuration() time.Duration {
	d, _ := time.ParseDuration(c.ProviderTimeout)
	return d
}
