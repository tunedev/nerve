// Package leak holds the goroutine-leak check that every test binary runs.
// A goroutine with no cancel path is a leak with good intentions, and no
// linter can see one: it exists only while the tests are running.
package leak

import (
	"testing"

	"go.uber.org/goleak"
)

// VerifyTestMain runs a package's tests and then fails the binary if any
// goroutine started during them is still running. A package wires it in with
//
//	func TestMain(m *testing.M) { leak.VerifyTestMain(m) }
//
// and the options every package should share are added here, not there.
func VerifyTestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
