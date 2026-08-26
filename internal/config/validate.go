package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// logLevels are the levels nerved understands, lowest first.
var logLevels = []string{"debug", "info", "warn", "error"}

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
		reachable("postgres-dsn", c.PostgresDSN, "postgres", "postgresql"),
		reachable("rabbitmq-url", c.RabbitMQURL, "amqp", "amqps"),
		reachable("ollama-url", c.OllamaURL, "http", "https"),
		listenable("admin-addr", c.AdminAddr),
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

// reachable accepts a URL that names one of the given schemes and a host, which
// is what every client here needs to dial. Whether the far end answers is not a
// config question.
func reachable(name, value string, schemes ...string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is not a URL: %w", name, err)
	}
	if !slices.Contains(schemes, parsed.Scheme) {
		return fmt.Errorf("%s has scheme %q, want %s", name, parsed.Scheme, strings.Join(schemes, " or "))
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s names no host", name)
	}
	return nil
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
