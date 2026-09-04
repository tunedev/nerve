package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/config"
)

// writeFile puts a config document on disk and returns its path.
func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nerve.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}

// env builds a lookup over a fixed set, standing in for os.LookupEnv.
func env(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := vars[name]
		return value, ok
	}
}

// The whole ticket in one test: four layers, each naming one setting more than
// the last, and every setting resolving to the highest layer that names it.
func TestLaterLayerWins(t *testing.T) {
	src := config.Sources{
		File: writeFile(t, `{
			"log-level":    "debug",
			"postgres-dsn": "postgres://file@db:5432/nerve",
			"rabbitmq-url": "amqp://file@broker:5672/"
		}`),
		Lookup: env(map[string]string{
			"NERVE_LOG_LEVEL":    "warn",
			"NERVE_POSTGRES_DSN": "postgres://env@db:5432/nerve",
		}),
		Args: []string{"-log-level", "error"},
	}

	cfg, err := config.Load(src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LogLevel != "error" {
		t.Errorf("LogLevel = %q, want the flag's %q", cfg.LogLevel, "error")
	}
	if cfg.PostgresDSN != "postgres://env@db:5432/nerve" {
		t.Errorf("PostgresDSN = %q, want the environment's", cfg.PostgresDSN)
	}
	if cfg.RabbitMQURL != "amqp://file@broker:5672/" {
		t.Errorf("RabbitMQURL = %q, want the file's", cfg.RabbitMQURL)
	}
	if cfg.OllamaURL != config.Defaults().OllamaURL {
		t.Errorf("OllamaURL = %q, want the default %q", cfg.OllamaURL, config.Defaults().OllamaURL)
	}
}

// No file, no environment, no flags: a checkout boots on its defaults.
func TestDefaultsAloneAreValid(t *testing.T) {
	cfg, err := config.Load(config.Sources{})
	if err != nil {
		t.Fatalf("Load with no sources: %v", err)
	}

	if !reflect.DeepEqual(cfg, config.Defaults()) {
		t.Errorf("Load() = %+v, want %+v", cfg, config.Defaults())
	}
}

// A flag that is not passed changes nothing, even though every flag is
// declared on every boot.
func TestUnpassedFlagsChangeNothing(t *testing.T) {
	cfg, err := config.Load(config.Sources{Args: []string{}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !reflect.DeepEqual(cfg, config.Defaults()) {
		t.Errorf("Load() = %+v, want the defaults untouched", cfg)
	}
}

func TestInvalidConfigFailsAtBoot(t *testing.T) {
	cases := map[string]config.Sources{
		"unknown log level":    {Lookup: env(map[string]string{"NERVE_LOG_LEVEL": "trace"})},
		"empty setting":        {Lookup: env(map[string]string{"NERVE_OLLAMA_URL": ""})},
		"wrong scheme":         {Args: []string{"-rabbitmq-url", "http://broker:5672/"}},
		"no host":              {Args: []string{"-postgres-dsn", "postgres:///nerve"}},
		"misspelled key":       {File: writeFile(t, `{"log_level": "debug"}`)},
		"unreadable file":      {File: filepath.Join(t.TempDir(), "absent.json")},
		"admin addr host only": {Args: []string{"-admin-addr", "127.0.0.1"}},
		"admin addr no port":   {Args: []string{"-admin-addr", "127.0.0.1:"}},
		// The exporters log a URL they cannot parse and keep their own
		// default, so an endpoint that is not one has to be refused here or it
		// silently exports somewhere else.
		"telemetry endpoint not a URL": {Args: []string{"-telemetry-endpoint", "://nope"}},
		"telemetry endpoint scheme":    {Args: []string{"-telemetry-endpoint", "grpc://collector:4317"}},
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := config.Load(src)
			if err == nil {
				t.Fatalf("Load() = %+v, want an error", cfg)
			}
			if !reflect.DeepEqual(cfg, config.Config{}) {
				t.Errorf("Load() returned %+v alongside its error, want the zero Config", cfg)
			}
		})
	}
}

// Telemetry is off by default and a setting that is off is not a broken one.
// Every other setting here has a default that works; this one has a real "no".
func TestNoTelemetryEndpointIsNotAProblem(t *testing.T) {
	cfg, err := config.Load(config.Sources{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TelemetryEndpoint != "" {
		t.Errorf("telemetry-endpoint defaults to %q, want nothing exported until an operator says so", cfg.TelemetryEndpoint)
	}

	for _, endpoint := range []string{"http://localhost:4317", "https://collector.example:4317"} {
		if _, err := config.Load(config.Sources{Args: []string{"-telemetry-endpoint", endpoint}}); err != nil {
			t.Errorf("Load with telemetry-endpoint %s: %v", endpoint, err)
		}
	}
}

// The level Validate accepts is the level the process runs at. The two used to
// be separate lists; they are one table now, and this is what says so.
func TestEveryAcceptedLogLevelResolves(t *testing.T) {
	for name, want := range map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	} {
		cfg, err := config.Load(config.Sources{Args: []string{"-log-level", name}})
		if err != nil {
			t.Fatalf("Load with log-level %s: %v", name, err)
		}
		if got := cfg.LogLevelValue(); got != want {
			t.Errorf("log-level %s resolves to %v, want %v", name, got, want)
		}
	}
}

// One boot reports every problem, so fixing a bad config does not take one
// restart per setting.
func TestValidateReportsEveryProblem(t *testing.T) {
	broken := config.Config{LogLevel: "trace", PostgresDSN: "", RabbitMQURL: "", OllamaURL: "", AdminAddr: ""}

	err := broken.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error")
	}

	for _, name := range []string{"log-level", "postgres-dsn", "rabbitmq-url", "ollama-url", "admin-addr"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("Validate() error does not name %s:\n%v", name, err)
		}
	}
}

// The keyspace kvfs mounts share is one URL because it is one connection, and
// it is optional because a laptop with no bucket keeps the keyspace in memory.
// A default that named a bucket would make a durable store the thing you have
// to turn off.
func TestKVStoreURLIsUnsetByDefault(t *testing.T) {
	cfg, err := config.Load(config.Sources{})
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.KVStoreURL != "" {
		t.Errorf("kv-store-url = %q, want it unset", cfg.KVStoreURL)
	}
}

// The corpus vectorfs mounts share is one URL for the reason the keyspace is,
// and it is optional because a build with no vectorfs mount needs nowhere to
// put a document. What refuses the mount is the kind, at boot, not this.
func TestDocStoreURLIsUnsetByDefault(t *testing.T) {
	cfg, err := config.Load(config.Sources{})
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.DocStoreURL != "" {
		t.Errorf("doc-store-url = %q, want it unset", cfg.DocStoreURL)
	}
}

// The corpus is a directory on this host or a bucket, and the scheme is which.
// Both spellings are accepted, and a file URL that names a host or a relative
// path is not: those are the two ways of writing one that reaches a directory
// nobody meant.
func TestValidateReadsTheCorpusURLByScheme(t *testing.T) {
	accepted := []string{
		"file:///var/lib/nerve/corpus",
		"http://nerve:nervenerve@127.0.0.1:9000/nerve",
		"https://AKIA:wJalrXUt@objects.example.com/nerve?region=eu-west-1",
	}
	for _, url := range accepted {
		cfg := config.Defaults()
		cfg.DocStoreURL = url
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate(%q) = %v", url, err)
		}
	}

	refused := map[string]string{
		"a host on a file URL":      "file://elsewhere/corpus",
		"a relative path":           "file:corpus",
		"a bucket with no path":     "http://nerve:nervenerve@127.0.0.1:9000",
		"a bucket with no password": "http://nerve@127.0.0.1:9000/nerve",
	}
	for what, url := range refused {
		cfg := config.Defaults()
		cfg.DocStoreURL = url
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate(%q) with %s = nil, want an error", url, what)
		}
	}
}

// The URL is the bucket's own address path-style, so what makes it usable is
// what the boot refuses: a store nothing can be signed for, a path that is not
// one bucket, and no bucket at all.
func TestValidateRefusesAStoreURLThatIsNotOneBucket(t *testing.T) {
	for name, url := range map[string]string{
		"noBucket":      "http://nerve:nervenerve@127.0.0.1:9000",
		"deeperPath":    "http://nerve:nervenerve@127.0.0.1:9000/nerve/sessions",
		"noCredentials": "http://127.0.0.1:9000/nerve",
		"noSecret":      "http://nerve@127.0.0.1:9000/nerve",
		"noHost":        "http:///nerve",
		"wrongScheme":   "s3://nerve:nervenerve@127.0.0.1:9000/nerve",
	} {
		cfg := config.Defaults()
		cfg.KVStoreURL = url
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: Validate(%q) = nil, want an error", name, url)
		}
	}
}

// A bucket an operator can reach is one the boot accepts, with or without the
// region a signature is made for.
func TestValidateAcceptsABucketURL(t *testing.T) {
	for _, url := range []string{
		"http://nerve:nervenerve@127.0.0.1:9000/nerve",
		"https://AKIA:wJalrXUt@objects.example.com/nerve?region=eu-west-1",
	} {
		cfg := config.Defaults()
		cfg.KVStoreURL = url
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate(%q) = %v", url, err)
		}
	}
}
