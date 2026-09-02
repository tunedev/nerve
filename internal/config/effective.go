package config

import (
	"net/url"
	"slices"
)

// Effective is the resolved config as an operator reads it back: every setting
// under the one name it is spelled with everywhere else, in the order the field
// table declares, with any credential replaced.
//
// It is a separate type from Config because it is a copy. Redaction here must
// never reach the values the process actually runs on.
type Effective struct {
	Settings  []Setting  `json:"settings"`
	Mounts    []Mount    `json:"mounts"`
	Libraries []Library  `json:"libraries"`
	Providers []Provider `json:"providers"`
}

// Setting is one resolved value under its canonical name — the flag, the JSON
// key, and the root of the environment variable, all at once.
type Setting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PublicSettings reports which of a kind's setting keys are safe to echo back.
// A kind that names none, and a kind nobody registered, have none.
//
// It is a declaration rather than a rule about values because no rule about a
// value can tell a bucket name from an access key, or a model name from an API
// key. A kind knows which of its own settings is a credential; nothing else
// does.
//
// Storage kinds and provider kinds are registered in different tables, so the
// view is built from one function per table rather than one for both. A kind
// name means whatever the table it came from says it means.
type PublicSettings func(kind string) []string

// redacted is what stands in for a value that is not echoed. It is the string
// url.URL.Redacted uses, so one answer reads the same way throughout.
const redacted = "xxxxx"

// Effective builds the operator's view. Redaction happens here rather than in
// the surface that serves it, so no surface can forget, and a second surface
// costs nothing.
//
// A nil declaration declares nothing, which redacts every setting the kinds it
// covers carry: the answer is safe before anyone has said what is safe.
func (c Config) Effective(mountKinds, providerKinds PublicSettings) Effective {
	settings := make([]Setting, len(fields))
	for i, f := range fields {
		settings[i] = Setting{Name: f.name, Value: Redact(*f.of(&c))}
	}

	mounts := make([]Mount, len(c.Mounts))
	for i, m := range c.Mounts {
		mounts[i] = Mount{Prefix: m.Prefix, Kind: m.Kind, Settings: echo(m.Kind, m.Settings, mountKinds)}
	}

	providers := make([]Provider, len(c.Providers))
	for i, p := range c.Providers {
		providers[i] = Provider{Name: p.Name, Kind: p.Kind, Settings: echo(p.Kind, p.Settings, providerKinds)}
	}

	// A library line is echoed as written. A kind, the file that names it and
	// the root it sits under are all structural: there is no value here a
	// setting's question about credentials could be asked of.
	return Effective{
		Settings:  settings,
		Mounts:    mounts,
		Libraries: slices.Clone(c.Libraries),
		Providers: providers,
	}
}

// Redact replaces the password inside a URL and returns anything else as
// written. It is what an echoed value still gets: a setting a kind declares
// safe can still be written as a connection string with a password in it.
//
// It is exported because the operator surface is not the only place a resolved
// setting is read back. The composition root says at boot what it booted on,
// and a second spelling of what a credential looks like is how one of them
// ends up wrong.
func Redact(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	if _, ok := parsed.User.Password(); !ok {
		return value
	}
	return parsed.Redacted()
}

// echo copies one line's settings, keeping the value of every key its kind
// declares public and replacing every other. Keys are kept either way, so the
// answer still shows which settings the line carries.
func echo(kind string, settings map[string]string, public PublicSettings) map[string]string {
	if settings == nil {
		return nil
	}

	var declared []string
	if public != nil {
		declared = public(kind)
	}

	out := make(map[string]string, len(settings))
	for name, value := range settings {
		if slices.Contains(declared, name) {
			out[name] = Redact(value)
			continue
		}
		out[name] = redacted
	}
	return out
}
