package config_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/config"
)

// The whole ticket in one test: what an operator reads back names the host, the
// database and the user, and does not name the password.
func TestEffectiveRedactsThePassword(t *testing.T) {
	cfg := config.Defaults()
	cfg.PostgresDSN = "postgres://nerve:hunter2@db:5432/nerve?sslmode=disable"

	value := settingValue(t, cfg.Effective(), "postgres-dsn")

	if strings.Contains(value, "hunter2") {
		t.Errorf("postgres-dsn = %q, still carries the password", value)
	}
	for _, want := range []string{"nerve@db:5432", "sslmode=disable"} {
		if !strings.Contains(value, want) {
			t.Errorf("postgres-dsn = %q, want it to still name %q", value, want)
		}
	}
}

// A value that is not a URL, or a URL with no password, is reported as written.
// Redaction that mangles what it does not have to hide is guesswork of its own.
func TestEffectiveLeavesEverythingElseAlone(t *testing.T) {
	cfg := config.Defaults()
	cfg.LogLevel = "debug"
	cfg.OllamaURL = "http://localhost:11434"

	effective := cfg.Effective()

	if got := settingValue(t, effective, "log-level"); got != "debug" {
		t.Errorf("log-level = %q, want %q", got, "debug")
	}
	if got := settingValue(t, effective, "ollama-url"); got != cfg.OllamaURL {
		t.Errorf("ollama-url = %q, want %q", got, cfg.OllamaURL)
	}
}

// Every setting appears. A setting added to Config and forgotten here is the
// one thing that would put "what is it configured as" back to guessing, so the
// count is checked against the struct rather than against a list kept by hand.
func TestEffectiveNamesEverySetting(t *testing.T) {
	effective := config.Defaults().Effective()

	typ := reflect.TypeOf(config.Config{})
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Type.Kind() != reflect.String {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		settingValue(t, effective, name)
	}

	want := 0
	for i := range typ.NumField() {
		if typ.Field(i).Type.Kind() == reflect.String {
			want++
		}
	}
	if len(effective.Settings) != want {
		t.Errorf("Settings has %d entries, want %d — one per string field of Config", len(effective.Settings), want)
	}
}

// Mount settings are operator-written strings that can carry a credential of
// their own, so the same rule reaches them.
func TestEffectiveRedactsMountSettings(t *testing.T) {
	cfg := config.Defaults()
	cfg.Mounts = []config.Mount{{
		Prefix:   "/blob",
		Kind:     "s3",
		Settings: map[string]string{"endpoint": "https://key:s3cret@minio:9000", "bucket": "nerve"},
	}}

	mounts := cfg.Effective().Mounts
	if len(mounts) != 1 {
		t.Fatalf("Mounts has %d entries, want 1", len(mounts))
	}
	if got := mounts[0].Settings["endpoint"]; strings.Contains(got, "s3cret") {
		t.Errorf("endpoint = %q, still carries the password", got)
	}
	if got := mounts[0].Settings["bucket"]; got != "nerve" {
		t.Errorf("bucket = %q, want %q", got, "nerve")
	}
}

// Redacting the view must not redact the config the process runs on: the
// endpoint reports a copy, and the copy is where the password is missing.
func TestEffectiveDoesNotChangeTheConfig(t *testing.T) {
	cfg := config.Defaults()
	dsn := "postgres://nerve:hunter2@db:5432/nerve"
	cfg.PostgresDSN = dsn
	cfg.Mounts = []config.Mount{{Prefix: "/blob", Kind: "s3", Settings: map[string]string{"endpoint": "https://key:s3cret@minio:9000"}}}

	_ = cfg.Effective()

	if cfg.PostgresDSN != dsn {
		t.Errorf("PostgresDSN = %q, want the unredacted %q", cfg.PostgresDSN, dsn)
	}
	if got := cfg.Mounts[0].Settings["endpoint"]; !strings.Contains(got, "s3cret") {
		t.Errorf("mount endpoint = %q, want the unredacted one", got)
	}
}

// The view is JSON, because the surface that serves it is.
func TestEffectiveMarshals(t *testing.T) {
	data, err := json.Marshal(config.Defaults().Effective())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"log-level"`) {
		t.Errorf("JSON = %s, want it to name log-level", data)
	}
}

// settingValue reads one setting out of the view, failing if it is absent.
func settingValue(t *testing.T, effective config.Effective, name string) string {
	t.Helper()
	for _, s := range effective.Settings {
		if s.Name == name {
			return s.Value
		}
	}
	t.Fatalf("no setting named %q in %+v", name, effective.Settings)
	return ""
}
