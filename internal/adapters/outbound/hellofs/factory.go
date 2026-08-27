package hellofs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a greeting.
const Kind = "hellofs"

// MessageSetting names what the one entry holds. It defaults, because a mount
// whose purpose is to prove the seams should need no config to do it, and it is
// public: a greeting is the least secret thing in the process.
const MessageSetting = "message"

// DefaultMessage is what a mount that names no message holds.
const DefaultMessage = "hello from nerve"

// Factory builds one greeting. Any setting other than the message is refused
// rather than ignored: it is a setting meant for another kind, and boot is the
// last place an operator can still act on it.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	message, named := settings[MessageSetting]
	if !named {
		message = DefaultMessage
	}
	if extra := slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(key string) bool { return key == MessageSetting }); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %q, and was given %s: %w", Kind, MessageSetting, strings.Join(extra, ", "), fs.ErrInvalid)
	}
	return New(message), nil
}
