package config

import "github.com/tunedev/nerve/internal/core/vfs"

// Mount is one line of the mounts array: where a plugin goes in the tree, which
// kind it is, and what that kind needs to know.
//
// It comes from the file layer alone. A mount table is a list, and neither an
// environment variable nor a flag carries one; naming the file is how an
// operator changes it.
type Mount struct {
	Prefix   string            `json:"prefix"`
	Kind     string            `json:"kind"`
	Settings map[string]string `json:"settings"`
}

// MountSpecs is the mount config in the storage domain's vocabulary, which is
// what a registry builds a table from. The JSON spelling stops here.
func (c Config) MountSpecs() []vfs.MountSpec {
	specs := make([]vfs.MountSpec, len(c.Mounts))
	for i, m := range c.Mounts {
		specs[i] = vfs.MountSpec{Prefix: m.Prefix, Kind: m.Kind, Settings: m.Settings}
	}
	return specs
}
