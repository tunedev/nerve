package config

import "net/url"

// Effective is the resolved config as an operator reads it back: every setting
// under the one name it is spelled with everywhere else, in the order the field
// table declares, with any password replaced.
//
// It is a separate type from Config because it is a copy. Redaction here must
// never reach the values the process actually runs on.
type Effective struct {
	Settings []Setting `json:"settings"`
	Mounts   []Mount   `json:"mounts"`
}

// Setting is one resolved value under its canonical name — the flag, the JSON
// key, and the root of the environment variable, all at once.
type Setting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Effective builds the operator's view. Redaction happens here rather than in
// the surface that serves it, so no surface can forget, and a second surface
// costs nothing.
func (c Config) Effective() Effective {
	settings := make([]Setting, len(fields))
	for i, f := range fields {
		settings[i] = Setting{Name: f.name, Value: redact(*f.of(&c))}
	}

	mounts := make([]Mount, len(c.Mounts))
	for i, m := range c.Mounts {
		mounts[i] = Mount{Prefix: m.Prefix, Kind: m.Kind, Settings: redactAll(m.Settings)}
	}

	return Effective{Settings: settings, Mounts: mounts}
}

// redact replaces the password inside a URL and returns anything else as
// written. A credential in a connection string is the only secret this config
// can carry — a secret proper has a different lifecycle and does not live here
// — and a value that is not a URL has nothing to hide.
func redact(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	if _, ok := parsed.User.Password(); !ok {
		return value
	}
	return parsed.Redacted()
}

// redactAll is redact over a mount's settings, into a new map so the original
// is untouched.
func redactAll(settings map[string]string) map[string]string {
	if settings == nil {
		return nil
	}

	out := make(map[string]string, len(settings))
	for name, value := range settings {
		out[name] = redact(value)
	}
	return out
}
