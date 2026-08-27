package queuefs_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
)

// A queue cannot be guessed at. Every refusal here happens before anything is
// dialled, because boot is the last place an operator can still act on a typo
// and a mount that reached Postgres to find out it had no name has waited too
// long to say so.
func TestFactoryRefusesSettingsItCannotHonour(t *testing.T) {
	cases := map[string]map[string]string{
		"nothing at all": {},
		"no name": {
			"journal_url": "postgres://nerve@127.0.0.1/nerve",
			"broker_url":  "amqp://nerve@127.0.0.1",
		},
		"no journal": {
			"name":       "orders",
			"broker_url": "amqp://nerve@127.0.0.1",
		},
		"no broker": {
			"name":        "orders",
			"journal_url": "postgres://nerve@127.0.0.1/nerve",
		},
		"a setting meant for another kind": {
			"name":        "orders",
			"journal_url": "postgres://nerve@127.0.0.1/nerve",
			"broker_url":  "amqp://nerve@127.0.0.1",
			"bucket":      "nerve",
		},
		"a batch that is not a count": {
			"name":        "orders",
			"journal_url": "postgres://nerve@127.0.0.1/nerve",
			"broker_url":  "amqp://nerve@127.0.0.1",
			"batch":       "lots",
		},
		"a batch of none": {
			"name":        "orders",
			"journal_url": "postgres://nerve@127.0.0.1/nerve",
			"broker_url":  "amqp://nerve@127.0.0.1",
			"batch":       "0",
		},
	}

	for what, settings := range cases {
		t.Run(what, func(t *testing.T) {
			fsys, err := queuefs.Factory(settings)
			if fsys != nil {
				t.Errorf("Factory(%v) built a mount, want none", settings)
			}
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Factory(%v) = %v, want fs.ErrInvalid", settings, err)
			}
		})
	}
}

// What a mount reads back to an operator is the queue and never the two URLs
// that reach it, because both carry a password.
func TestOnlyTheQueueReadsBack(t *testing.T) {
	for _, setting := range queuefs.Public {
		if strings.HasSuffix(setting, "_url") {
			t.Errorf("Public names %q, which carries a credential", setting)
		}
	}
}
