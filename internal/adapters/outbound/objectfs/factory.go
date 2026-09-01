package objectfs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for an object store.
const Kind = "objectfs"

// The settings one mount takes. Region has a default because a store on the
// laptop has no regions and every S3 signature needs one anyway; the rest have
// none, because there is no sensible guess at where a bucket is or who may
// write to it.
const (
	settingEndpoint = "endpoint"
	settingBucket   = "bucket"
	settingRegion   = "region"
	settingKeyID    = "access_key_id"
	settingSecret   = "secret_access_key"
)

// DefaultRegion is what a bucket is signed for when nothing names a region. It
// is exported because a mount is not the only way a bucket is reached: the
// composition root builds one from a URL, and two spellings of the fallback
// would sign two ways.
const DefaultRegion = "us-east-1"

// Public names the settings safe to read back to an operator. The two
// credentials are not among them, so `GET /config` shows that a mount carries
// them without showing what they are.
var Public = []string{settingEndpoint, settingBucket, settingRegion}

// known is every setting this kind accepts, which is what makes a misspelling a
// boot failure instead of a silently ignored line. A setting nobody reads is
// indistinguishable from a setting that did not work.
var known = []string{settingEndpoint, settingBucket, settingRegion, settingKeyID, settingSecret}

// Factory builds one mount from its settings. Everything it needs is required
// except the region, and everything it does not recognise is refused: boot is
// the last place an operator can still act on a typo.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	if unknown := unknownSettings(settings); len(unknown) > 0 {
		return nil, fmt.Errorf("kind %q does not take %s; it takes %s: %w",
			Kind, strings.Join(unknown, ", "), strings.Join(known, ", "), fs.ErrInvalid)
	}

	cfg := S3Config{
		Endpoint: settings[settingEndpoint],
		Bucket:   settings[settingBucket],
		Region:   settings[settingRegion],
		KeyID:    settings[settingKeyID],
		Secret:   settings[settingSecret],
	}
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}
	if missing := missingSettings(settings); len(missing) > 0 {
		return nil, fmt.Errorf("kind %q needs %s: %w", Kind, strings.Join(missing, ", "), fs.ErrInvalid)
	}

	bucket, err := NewS3(cfg)
	if err != nil {
		return nil, fmt.Errorf("kind %q: %w", Kind, err)
	}
	return New(bucket), nil
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
	for _, name := range []string{settingEndpoint, settingBucket, settingKeyID, settingSecret} {
		if settings[name] == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
