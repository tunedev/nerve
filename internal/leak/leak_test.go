package leak_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/leak"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { leak.VerifyTestMain(m) }

// recorder stands in for *testing.T so a test can watch the check fail without
// failing itself.
type recorder struct{ report string }

func (r *recorder) Error(args ...any) { r.report = fmt.Sprint(args...) }

// A goroutine parked on a channel nobody closes is the leak the check exists
// to catch. The check is only worth running if it says so, and nothing else in
// the suite can tell a wired check from an absent one.
func TestGoroutineWithNoCancelPathIsReported(t *testing.T) {
	stop := make(chan struct{})
	go func() { <-stop }()
	defer close(stop)

	var got recorder
	goleak.VerifyNone(&got)

	if !strings.Contains(got.report, "TestGoroutineWithNoCancelPathIsReported") {
		t.Fatalf("check did not report the parked goroutine, got %q", got.report)
	}
}
