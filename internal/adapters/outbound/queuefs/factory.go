package queuefs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a queue.
const Kind = "queuefs"

// The settings one mount takes. The two URLs carry credentials; the name, the
// exchange and the batch do not.
const (
	settingName     = "name"
	settingJournal  = "journal_url"
	settingBroker   = "broker_url"
	settingExchange = "exchange"
	settingBatch    = "batch"
)

// exchangePrefix is what an exchange is named when a mount does not name one:
// the queue's name under this platform's, so a shared broker does not have to
// be a broker with nothing else on it.
const exchangePrefix = "nerve."

// Public names the settings safe to read back to an operator. The two URLs are
// not among them, so `GET /config` shows that a mount carries them without
// showing what they are.
var Public = []string{settingName, settingExchange, settingBatch}

// known is every setting this kind accepts, which is what makes a misspelling a
// boot failure instead of a silently ignored line.
var known = []string{settingName, settingJournal, settingBroker, settingExchange, settingBatch}

// required is what a queue cannot be built without. There is no guess at where
// a journal is, where a broker is, or what to call a queue.
var required = []string{settingName, settingJournal, settingBroker}

// Factory builds one queue from its settings, reaching both the journal and the
// broker while an operator is still watching. A queue that cannot record is not
// a queue, and a broker named wrongly is a mount that would look healthy while
// nothing it recorded ever left the process.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	if unknown := unknownSettings(settings); len(unknown) > 0 {
		return nil, fmt.Errorf("kind %q does not take %s; it takes %s: %w",
			Kind, strings.Join(unknown, ", "), strings.Join(known, ", "), fs.ErrInvalid)
	}
	if missing := missingSettings(settings); len(missing) > 0 {
		return nil, fmt.Errorf("kind %q needs %s: %w", Kind, strings.Join(missing, ", "), fs.ErrInvalid)
	}

	batch, err := batchSetting(settings[settingBatch])
	if err != nil {
		return nil, err
	}

	name := settings[settingName]
	exchange := settings[settingExchange]
	if exchange == "" {
		exchange = exchangePrefix + name
	}

	journal, err := OpenPostgres(settings[settingJournal], name)
	if err != nil {
		return nil, fmt.Errorf("kind %q: %w", Kind, err)
	}
	broker, err := DialAMQP(settings[settingBroker], exchange)
	if err != nil {
		journal.Close()
		return nil, fmt.Errorf("kind %q: %w", Kind, err)
	}
	return New(name, journal, NewRelay(journal, broker, batch)), nil
}

// batchSetting is how many messages one pass of the relay carries. It is
// operational — how much work a write pays for when the broker has been away —
// so it is config, and a spelling that is not a count is refused rather than
// defaulted.
func batchSetting(spelled string) (int, error) {
	if spelled == "" {
		return DefaultBatch, nil
	}

	batch, err := strconv.Atoi(spelled)
	if err != nil || batch <= 0 {
		return 0, fmt.Errorf("kind %q setting %q = %q, which is not a count of messages: %w",
			Kind, settingBatch, spelled, fs.ErrInvalid)
	}
	return batch, nil
}

func unknownSettings(settings map[string]string) []string {
	var unknown []string
	for _, name := range slices.Sorted(maps.Keys(settings)) {
		if !slices.Contains(known, name) {
			unknown = append(unknown, name)
		}
	}
	return unknown
}

func missingSettings(settings map[string]string) []string {
	var missing []string
	for _, name := range required {
		if settings[name] == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
