package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// logLevels are the levels nerved understands, lowest first.
var logLevels = []string{"debug", "info", "warn", "error"}

// Validate reports everything wrong with the resolved config at once, joined
// into one error. An operator fixing a boot failure gets the whole list rather
// than one problem per restart.
func (c Config) Validate() error {
	return errors.Join(
		oneOf("log-level", c.LogLevel, logLevels),
		reachable("postgres-dsn", c.PostgresDSN, "postgres", "postgresql"),
		reachable("rabbitmq-url", c.RabbitMQURL, "amqp", "amqps"),
		reachable("ollama-url", c.OllamaURL, "http", "https"),
	)
}

// oneOf accepts a value from a closed set.
func oneOf(name, value string, allowed []string) error {
	if slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("%s is %q, want one of %s", name, value, strings.Join(allowed, ", "))
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
