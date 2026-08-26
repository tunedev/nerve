package vfs_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// The whole classification in one table, because sorting the error is the part
// that decides whether retrying helps or just fails slower.
//
// Fail-fast is what an error gets unless something has said otherwise: the
// io/fs sentinels are answers about the world rather than failures to reach it,
// and an error nobody has classified is a guess.
func TestClassify(t *testing.T) {
	both := fmt.Errorf("%w: %w", vfs.ErrTransient, fs.ErrInvalid)

	for name, c := range map[string]struct {
		err  error
		want vfs.Class
	}{
		"nothing wrong":                 {nil, vfs.FailFast},
		"a validation error":            {fs.ErrInvalid, vfs.FailFast},
		"a wrapped validation error":    {fmt.Errorf("mount %q: %w", "/x", fs.ErrInvalid), vfs.FailFast},
		"a missing entry":               {fs.ErrNotExist, vfs.FailFast},
		"an entry already there":        {fs.ErrExist, vfs.FailFast},
		"a refusal":                     {fs.ErrPermission, vfs.FailFast},
		"a caller that gave up":         {context.Canceled, vfs.FailFast},
		"an error nobody sorted":        {errors.New("something went wrong"), vfs.FailFast},
		"a call out of time":            {context.DeadlineExceeded, vfs.Retryable},
		"a plugin saying transient":     {vfs.ErrTransient, vfs.Retryable},
		"a wrapped transient":           {fmt.Errorf("dial: %w", vfs.ErrTransient), vfs.Retryable},
		"transient and invalid at once": {both, vfs.FailFast},
	} {
		t.Run(name, func(t *testing.T) {
			if got := vfs.Classify(c.err); got != c.want {
				t.Errorf("Classify(%v) = %s, want %s", c.err, got, c.want)
			}
		})
	}
}
