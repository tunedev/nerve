package config

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// logLevels are the levels nerved understands, lowest first. It is derived from
// the table the process resolves a level through rather than written beside it:
// a level this accepted and that one could not resolve would boot and then log
// at the wrong threshold.
var logLevels = slices.SortedFunc(maps.Keys(levels), func(a, b string) int {
	return cmp.Compare(levels[a], levels[b])
})

// Validate reports everything wrong with the resolved config at once, joined
// into one error. An operator fixing a boot failure gets the whole list rather
// than one problem per restart.
func (c Config) Validate() error {
	return errors.Join(
		oneOf("log-level", c.LogLevel, logLevels),
		positiveDuration("call-timeout", c.CallTimeout),
		nonNegativeDuration("drain-delay", c.DrainDelay),
		atLeastOne("retry-attempts", c.RetryAttempts),
		positiveDuration("retry-backoff", c.RetryBackoff),
		atLeastOne("breaker-threshold", c.BreakerThreshold),
		positiveDuration("breaker-cooldown", c.BreakerCooldown),
		positiveDuration("provider-timeout", c.ProviderTimeout),
		nonNegativeNumber("readahead-window", c.ReadaheadWindow),
		optional("telemetry-endpoint", c.TelemetryEndpoint, reachable, "http", "https"),
		reachable("postgres-dsn", c.PostgresDSN, "postgres", "postgresql"),
		reachable("rabbitmq-url", c.RabbitMQURL, "amqp", "amqps"),
		reachable("ollama-url", c.OllamaURL, "http", "https"),
		optional("kv-store-url", c.KVStoreURL, bucketURL),
		optional("doc-store-url", c.DocStoreURL, corpusURL),
		optional("resources-root", c.ResourcesRoot, rootedPath),
		optional("scratch-root", c.ScratchRoot, rootedPath),
		signable(c.ResourcesRoot, c.DownloadKeyFile),
		optional("ingest-queue", c.IngestQueue, rootedPath),
		optional("ingest-root", c.IngestRoot, rootedPath),
		ingestible(c.IngestQueue, c.ResourcesRoot, c.IngestRoot),
		optional("ingest-index", c.IngestIndex, rootedPath),
		indexable(c.IngestIndex, c.IngestQueue),
		listenable("admin-addr", c.AdminAddr),
		servable(c.APIAddr, c.ResourcesRoot),
	)
}

// oneOf accepts a value from a closed set.
func oneOf(name, value string, allowed []string) error {
	if slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("%s is %q, want one of %s", name, value, strings.Join(allowed, ", "))
}

// positiveDuration accepts a Go duration with time left in it. Zero and
// negative are rejected rather than read as "no timeout": a call with no
// deadline is what the setting exists to prevent.
func positiveDuration(name, value string) error {
	d, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s is not a duration: %w", name, err)
	}
	if d <= 0 {
		return fmt.Errorf("%s is %s, want a positive duration", name, d)
	}
	return nil
}

// nonNegativeDuration accepts a Go duration that does not run backwards. Zero
// is a real answer here — close the listener at once, because nothing is
// routing to this process — so it is accepted rather than read as a mistake.
func nonNegativeDuration(name, value string) error {
	d, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s is not a duration: %w", name, err)
	}
	if d < 0 {
		return fmt.Errorf("%s is %s, want zero or a positive duration", name, d)
	}
	return nil
}

// atLeastOne accepts a whole number of attempts with a call in it. Zero is
// rejected rather than read as "no retry": a call that is never made is not
// what the setting means, and one attempt already says it.
func atLeastOne(name, value string) error {
	attempts, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%s is not a number: %w", name, err)
	}
	if attempts < 1 {
		return fmt.Errorf("%s is %d, want at least 1", name, attempts)
	}
	return nil
}

// nonNegativeNumber accepts a whole number of bytes that does not run
// backwards. Zero is a real answer here — it is how reading ahead is turned
// off — so it is accepted rather than read as a mistake.
func nonNegativeNumber(name, value string) error {
	count, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%s is not a number: %w", name, err)
	}
	if count < 0 {
		return fmt.Errorf("%s is %d, want zero or a positive number of bytes", name, count)
	}
	return nil
}

// optional applies check to a setting that may be unset, because a setting with
// a real "off" is a different thing from one with a default. An empty value is
// the operator saying no, and saying no is not a boot failure.
func optional(name, value string, check func(string, string, ...string) error, args ...string) error {
	if value == "" {
		return nil
	}
	return check(name, value, args...)
}

// reachable accepts a URL that names one of the given schemes and a host, which
// is what every client here needs to dial. Whether the far end answers is not a
// config question.
func reachable(name, value string, schemes ...string) error {
	_, err := reachableURL(name, value, schemes...)
	return err
}

// reachableURL is reachable's check with the parsed URL handed back, for a
// check that has more to say about the rest of it.
func reachableURL(name, value string, schemes ...string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("%s is not a URL: %w", name, err)
	}
	if !slices.Contains(schemes, parsed.Scheme) {
		return nil, fmt.Errorf("%s has scheme %q, want %s", name, parsed.Scheme, strings.Join(schemes, " or "))
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%s names no host", name)
	}
	return parsed, nil
}

// bucketURL accepts the URL of one bucket, addressed path-style: a scheme a
// store answers on, a host, a path naming the bucket and nothing else, and the
// two credentials as userinfo. A deeper path is refused rather than read as a
// prefix inside the bucket, since a keyspace an operator thought was scoped and
// is not is found as keys under the wrong name.
//
// The credentials are required here rather than where they are signed with, so
// that a URL missing them is reported beside everything else wrong with the
// config instead of one restart later.
func bucketURL(name, value string, _ ...string) error {
	parsed, err := reachableURL(name, value, "http", "https")
	if err != nil {
		return err
	}

	switch bucket := strings.Trim(parsed.Path, "/"); {
	case bucket == "":
		return fmt.Errorf("%s names no bucket; its path is the bucket", name)
	case strings.Contains(bucket, "/"):
		return fmt.Errorf("%s has path %q, want one bucket name", name, parsed.Path)
	}

	secret, given := parsed.User.Password()
	if parsed.User.Username() == "" || !given || secret == "" {
		return fmt.Errorf("%s carries no credentials; write them as key:secret@ before the host", name)
	}
	return nil
}

// corpusURL accepts where a corpus of documents is kept: a file:// URL naming an
// absolute directory on this host, or the bucket URL bucketURL already spells.
// The two are one setting because they are one decision — where the documents
// live — and which of them it is, is the scheme.
func corpusURL(name, value string, _ ...string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is not a URL: %w", name, err)
	}
	if parsed.Scheme != "file" {
		return bucketURL(name, value)
	}
	if parsed.Host != "" {
		return fmt.Errorf("%s has host %q; a file URL names a path on this host, so write file:///path", name, parsed.Host)
	}
	if !strings.HasPrefix(parsed.Path, "/") {
		return fmt.Errorf("%s has path %q, want an absolute directory", name, parsed.Path)
	}
	return nil
}

// rootedPath accepts where an area of the tree begins: a rooted, clean path
// such as "/resources". It is the mount table's rule for a prefix, because a
// root is where a mount was put, and the stores built over one refuse anything
// else — checking here is what turns that into a boot failure an operator can
// read beside the rest.
func rootedPath(name, value string, _ ...string) error {
	if !strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return fmt.Errorf("%s is %q, want a rooted clean path such as /resources", name, value)
	}
	return nil
}

// signable refuses a resources root with nowhere to read the download key
// from. The key never stands in this document — a secret has its own storage —
// so what is checked is that the file holding it has been named.
func signable(root, keyFile string) error {
	if root == "" || keyFile != "" {
		return nil
	}
	return fmt.Errorf("resources-root is %q but download-key-file names no file to read the signing key from", root)
}

// ingestible refuses an ingest queue with nothing to ingest from and nowhere to
// record what it did. A worker with no resource store resolves every job it
// claims to nothing, and one with no ledger runs the stages and leaves no
// answer to the only question a caller asks afterwards.
func ingestible(queue, resources, ledger string) error {
	if queue == "" {
		return nil
	}
	return errors.Join(
		named("ingest-queue names a queue but resources-root names no tree to ingest from", resources),
		named("ingest-queue names a queue but ingest-root names no root to record indexing under", ledger),
	)
}

// indexable refuses an index with nothing feeding it. Only the ingest worker
// writes into the index, so a build that names one and no queue would boot
// saying it indexes and never write a document.
func indexable(index, queue string) error {
	if index == "" {
		return nil
	}
	return named("ingest-index names an index but ingest-queue names no queue to ingest from", queue)
}

// servable refuses a service address a server cannot bind, and one with no
// resource store behind it to serve.
func servable(addr, resources string) error {
	if addr == "" {
		return nil
	}
	return errors.Join(
		listenable("api-addr", addr),
		named("api-addr names an address but resources-root names no store to serve", resources),
	)
}

// named refuses an empty value with the sentence describing what its emptiness
// costs. It is the shape a setting required by another setting takes: what is
// wrong is the pair, so the message is about the pair.
func named(because, value string) error {
	if value != "" {
		return nil
	}
	return errors.New(because)
}

// listenable accepts a host:port a server can bind. An empty host means every
// interface, which is a deployment choice rather than a config error; an
// unusable port is not, and finding that out at boot beats finding it out when
// someone needs the answer.
func listenable(name, value string) error {
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("%s is not host:port: %w", name, err)
	}

	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("%s has port %q, want a number from 1 to 65535", name, port)
	}
	return nil
}
