package stability

import (
	"context"
	"errors"
	"io/fs"
)

// ErrTransient marks a failure another attempt might not see: the far end
// refused a connection, dropped one mid-call, or answered that it is busy. A
// plugin wraps it around whatever its driver returned —
//
//	fmt.Errorf("read %q: %w: %w", path, err, stability.ErrTransient)
//
// — and that wrapping is the whole of how a driver's error vocabulary becomes
// this one. Translating at the plugin's edge is the same move as mapping a
// driver error onto an io/fs sentinel, and it keeps the classifier from having
// to know a single driver.
var ErrTransient = errors.New("transient failure")

// Class is what an error is worth: another attempt, or none.
//
// The zero value is FailFast, so an error nobody has classified is not
// retried. That default is the point of the type. Retrying a validation error
// is a slower failure, retrying an unknown error is a guess, and both cost a
// dependency that is already struggling.
type Class int

const (
	// FailFast is an error another attempt cannot change.
	FailFast Class = iota

	// Retryable is an error another attempt might not see.
	Retryable
)

// String names the class as the ticket does.
func (c Class) String() string {
	if c == Retryable {
		return "retryable"
	}
	return "fail-fast"
}

// answers are the errors that describe the world rather than a failure to
// reach it. A path that does not exist still will not exist on the second
// attempt, and a caller that has gone is not coming back.
var answers = []error{fs.ErrInvalid, fs.ErrNotExist, fs.ErrExist, fs.ErrPermission, context.Canceled}

// Classify sorts one error into the two classes. It is exported and takes no
// policy because classification is the part of retrying that is worth reading
// on its own: a caller can ask what an error is worth without a retry loop
// around it, and a test can check the sorting without timing anything.
//
// An answer wins over a transient mark. An error carrying both is a plugin
// saying the far end wobbled and the call was invalid anyway, and repeating an
// invalid call cannot make it valid.
//
// A call out of time is retryable: the deadline it overran is one attempt's,
// not the caller's budget, and the wrapper stops when that budget is spent.
func Classify(err error) Class {
	if err == nil {
		return FailFast
	}

	for _, answer := range answers {
		if errors.Is(err, answer) {
			return FailFast
		}
	}

	if errors.Is(err, ErrTransient) || errors.Is(err, context.DeadlineExceeded) {
		return Retryable
	}
	return FailFast
}
