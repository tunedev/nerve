// Package config resolves one Config from four layers — defaults, a file, the
// environment, then flags — and refuses to return an invalid one.
//
// Later wins. Each layer overwrites only the settings it names, so a value
// survives from the lowest layer that names it up to the highest, and what no
// layer names keeps its default.
package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Config is everything nerved needs to reach the world outside itself.
//
// Every setting is a string because that is what the file, the environment and
// the command line all carry. Parsing into richer types is the business of the
// adapter that uses one, not of the layer that resolves it.
type Config struct {
	LogLevel    string `json:"log-level"`
	CallTimeout string `json:"call-timeout"`
	DrainDelay  string `json:"drain-delay"`

	// RetryAttempts and RetryBackoff are the retry policy: how many times a
	// call may be made, counting the first, and the ceiling on the wait before
	// the second. One attempt is retry turned off.
	RetryAttempts string `json:"retry-attempts"`
	RetryBackoff  string `json:"retry-backoff"`

	// BreakerThreshold and BreakerCooldown are the breaker policy: how many
	// transient failures in a row stop a mount being called, and how long it is
	// left alone before one call finds out whether it is back.
	BreakerThreshold string `json:"breaker-threshold"`
	BreakerCooldown  string `json:"breaker-cooldown"`

	// ReadaheadWindow is how many bytes of a streaming read are fetched in
	// front of the caller, so the next piece is arriving while the caller works
	// on the one it has. Zero turns reading ahead off.
	ReadaheadWindow string `json:"readahead-window"`

	// TelemetryEndpoint is the collector traces, metrics and logs are exported
	// to. An http:// scheme is a plaintext connection and https:// a TLS one,
	// which is how the OTLP exporters read the standard variable. Empty
	// exports nothing, which is what a laptop with no collector wants.
	TelemetryEndpoint string `json:"telemetry-endpoint"`

	PostgresDSN string `json:"postgres-dsn"`
	RabbitMQURL string `json:"rabbitmq-url"`
	OllamaURL   string `json:"ollama-url"`

	// KVStoreURL is the keyspace every kvfs mount is a namespaced part of. It
	// is one URL rather than five settings because a keyspace is shared: a
	// mount names which part of it it holds, and the store behind it belongs to
	// the process. Path-style addressing makes the value the bucket's own URL —
	// credentials as userinfo, the bucket as the path, the signing region as a
	// query — so the same redaction postgres-dsn gets covers the secret here.
	// Empty is the keyspace in memory, which is what a laptop with no bucket
	// wants.
	KVStoreURL string `json:"kv-store-url"`

	// AdminAddr is where the operator surface listens. It defaults to the
	// loopback interface because what it answers with — endpoints, hosts, the
	// shape of the mount table — is for whoever is on the box.
	AdminAddr string `json:"admin-addr"`

	// Mounts is the mount table: which plugin answers for which subtree. It is
	// a list, so unlike the settings above it comes from the file layer alone.
	Mounts []Mount `json:"mounts"`

	// Libraries is the library table: which document kind is kept under which
	// root. It is a list, so it comes from the file layer alone as Mounts does.
	Libraries []Library `json:"libraries"`
}

// Defaults are the bottom layer: what a local checkout needs to run with no
// file, no environment and no flags. They match docker-compose.yml.
func Defaults() Config {
	return Config{
		LogLevel:         "info",
		CallTimeout:      "10s",
		DrainDelay:       "1s",
		RetryAttempts:    "3",
		RetryBackoff:     "100ms",
		BreakerThreshold: "5",
		BreakerCooldown:  "30s",
		ReadaheadWindow:  strconv.Itoa(vfs.DefaultReadahead),
		PostgresDSN:      "postgres://nerve:nerve@localhost:5432/nerve?sslmode=disable",
		RabbitMQURL:      "amqp://nerve:nerve@localhost:5672/",
		OllamaURL:        "http://localhost:11434",
		AdminAddr:        "127.0.0.1:7777",
	}
}

// Sources are the three layers above the defaults, in the form the caller has
// them. A zero Sources resolves to the defaults alone.
//
// File is a path rather than a flag because the file layer sits below the flag
// layer: a flag naming the file would have to be read before the layer it
// overrides. The environment is the one place a path can be named without that
// circularity.
type Sources struct {
	// File is the path to a JSON document. Empty means there is no file layer.
	File string
	// Lookup reads one environment variable, reporting whether it is set.
	// os.LookupEnv is the caller. Nil means there is no environment layer.
	Lookup func(string) (string, bool)
	// Args are the command-line arguments without the program name.
	Args []string
}

// A layer takes the config resolved so far and returns it with its own
// settings applied.
type layer func(Config) (Config, error)

// Load applies every layer in precedence order and validates the result. It
// returns the zero Config with an error if any layer cannot be read or the
// resolved config is invalid, so a caller that ignores the error boots on
// nothing rather than on something plausible.
func Load(src Sources) (Config, error) {
	cfg := Defaults()

	for _, apply := range []layer{fromFile(src.File), fromEnv(src.Lookup), fromFlags(src.Args)} {
		next, err := apply(cfg)
		if err != nil {
			return Config{}, err
		}
		cfg = next
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// fromFile applies a JSON document. A key the document does not name keeps the
// value below it; a key it names but does not recognise is an error, because a
// misspelled setting that is silently ignored is the failure this ticket
// exists to prevent.
func fromFile(path string) layer {
	return func(cfg Config) (Config, error) {
		if path == "" {
			return cfg, nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("config file: %w", err)
		}

		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		}
		return cfg, nil
	}
}

// fromEnv applies every NERVE_-prefixed variable that is set. A variable set to
// the empty string sets the empty string: the layer reports what the operator
// wrote, and Validate decides whether it is usable.
func fromEnv(lookup func(string) (string, bool)) layer {
	return func(cfg Config) (Config, error) {
		if lookup == nil {
			return cfg, nil
		}

		for _, f := range fields {
			if value, ok := lookup(envName(f.name)); ok {
				*f.of(&cfg) = value
			}
		}
		return cfg, nil
	}
}

// fromFlags applies the command line. Each flag defaults to the value the
// layers below resolved, so a flag that is not passed changes nothing and the
// usage text shows what would actually be used.
func fromFlags(args []string) layer {
	return func(cfg Config) (Config, error) {
		set := flag.NewFlagSet("nerved", flag.ContinueOnError)

		passed := make([]*string, len(fields))
		for i, f := range fields {
			passed[i] = set.String(f.name, *f.of(&cfg), f.usage)
		}

		if err := set.Parse(args); err != nil {
			return Config{}, err
		}

		for i, f := range fields {
			*f.of(&cfg) = *passed[i]
		}
		return cfg, nil
	}
}

// A field is one setting under its single canonical name. The flag is that
// name, the JSON key is that name, and the environment variable is derived
// from it, so a setting is spelled once and reached three ways.
type field struct {
	name  string
	usage string
	of    func(*Config) *string
}

var fields = []field{
	{"log-level", "debug, info, warn or error", func(c *Config) *string { return &c.LogLevel }},
	{"call-timeout", "deadline given to every remote call", func(c *Config) *string { return &c.CallTimeout }},
	{"drain-delay", "how long readiness fails before the listener closes", func(c *Config) *string { return &c.DrainDelay }},
	{"retry-attempts", "times a repeatable call may be made after a transient failure, counting the first", func(c *Config) *string { return &c.RetryAttempts }},
	{"retry-backoff", "ceiling on the wait before the second attempt, doubling after that", func(c *Config) *string { return &c.RetryBackoff }},
	{"breaker-threshold", "transient failures in a row that stop a mount being called", func(c *Config) *string { return &c.BreakerThreshold }},
	{"breaker-cooldown", "how long a mount is left alone before one call probes it", func(c *Config) *string { return &c.BreakerCooldown }},
	{"readahead-window", "bytes of a streaming read fetched in front of the caller; 0 turns it off", func(c *Config) *string { return &c.ReadaheadWindow }},
	{"telemetry-endpoint", "OTLP collector URL for traces, metrics and logs; empty exports nothing", func(c *Config) *string { return &c.TelemetryEndpoint }},
	{"postgres-dsn", "Postgres connection string", func(c *Config) *string { return &c.PostgresDSN }},
	{"rabbitmq-url", "RabbitMQ connection URL", func(c *Config) *string { return &c.RabbitMQURL }},
	{"ollama-url", "Ollama base URL", func(c *Config) *string { return &c.OllamaURL }},
	{"kv-store-url", "bucket URL holding the keyspace kvfs mounts share; empty keeps it in memory", func(c *Config) *string { return &c.KVStoreURL }},
	{"admin-addr", "host:port for the operator surface", func(c *Config) *string { return &c.AdminAddr }},
}

// envName is the environment spelling of a setting: NERVE_ before it, upper
// case, underscores for dashes.
func envName(name string) string {
	return "NERVE_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}
