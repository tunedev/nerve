package config

import "strconv"

// ReadaheadBytes is the readahead-window setting in the vocabulary of the code
// that uses it: how far in front of a caller a streaming read is fetched. Zero
// turns reading ahead off, and the mounts are built without the wrapper at all.
//
// Load has already validated it, so a Config that came from Load parses. One
// built by hand and never validated resolves to zero, which is reading ahead
// turned off rather than anything worse — which is why Load is still the way in.
func (c Config) ReadaheadBytes() int {
	window, _ := strconv.Atoi(c.ReadaheadWindow)
	return window
}
