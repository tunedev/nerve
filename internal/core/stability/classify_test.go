package stability_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/core/stability"
)

// The whole classification in one table, because sorting the error is the part
// that decides whether retrying helps or just fails slower.
//
// Fail-fast is what an error gets unless something has said otherwise: the
// io/fs sentinels are answers about the world rather than failures to reach it,
// and an error nobody has classified is a guess.
func TestClassify(t *testing.T) {
	both := fmt.Errorf("%w: %w", stability.ErrTransient, fs.ErrInvalid)

	for name, c := range map[string]struct {
		err  error
		want stability.Class
	}{
		"nothing wrong":                 {nil, stability.FailFast},
		"a validation error":            {fs.ErrInvalid, stability.FailFast},
		"a wrapped validation error":    {fmt.Errorf("mount %q: %w", "/x", fs.ErrInvalid), stability.FailFast},
		"a missing entry":               {fs.ErrNotExist, stability.FailFast},
		"an entry already there":        {fs.ErrExist, stability.FailFast},
		"a refusal":                     {fs.ErrPermission, stability.FailFast},
		"a caller that gave up":         {context.Canceled, stability.FailFast},
		"an error nobody sorted":        {errors.New("something went wrong"), stability.FailFast},
		"a call out of time":            {context.DeadlineExceeded, stability.Retryable},
		"a plugin saying transient":     {stability.ErrTransient, stability.Retryable},
		"a wrapped transient":           {fmt.Errorf("dial: %w", stability.ErrTransient), stability.Retryable},
		"transient and invalid at once": {both, stability.FailFast},
	} {
		t.Run(name, func(t *testing.T) {
			if got := stability.Classify(c.err); got != c.want {
				t.Errorf("Classify(%v) = %s, want %s", c.err, got, c.want)
			}
		})
	}
}
