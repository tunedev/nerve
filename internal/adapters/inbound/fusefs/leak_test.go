package fusefs_test

import (
	"os"
	"testing"

	"github.com/tunedev/nerve/internal/leak"
)

// A test cannot kill itself and go on asserting, so the process the crash case
// kills is this binary re-executed. With helperDir set it mounts and blocks
// instead of running the tests; serveUntilKilled is in lifecycle_test.go,
// beside the case that spawns it.
func TestMain(m *testing.M) {
	if dir := os.Getenv(helperDir); dir != "" {
		serveUntilKilled(dir, os.Getenv(helperReady))
	}
	leak.VerifyTestMain(m)
}
