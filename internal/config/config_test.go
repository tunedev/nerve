package config_test

import (
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
