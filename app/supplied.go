package app

import "github.com/usestring/gate-inbox/internal/config"

// SuppliedSetting is one setting the core leaves empty on purpose -- an
// endpoint, a credential source or an identity that belongs to whoever runs
// the board -- for a distribution to fill in.
type SuppliedSetting struct {
	// Key is where the value lives: a dotted config.toml key, which
	// Options.ConfigDefaults can set; the name of an environment variable
	// when Env is set; or an interface in the extension package when
	// Extension is set, which one of the build's extensions implements.
	Key       string
	Env       bool
	Extension bool
	// Without says what the board does while the value is absent.
	Without string
}

// DistributionSupplied lists every setting a distribution brings. The core
// ships none of them, so a board nobody configured sends nothing anywhere
// and reads no credential.
func DistributionSupplied() []SuppliedSetting {
	out := make([]SuppliedSetting, len(config.DistributionSupplied))
	for i, s := range config.DistributionSupplied {
		out[i] = SuppliedSetting{Key: s.Key, Env: s.Env, Extension: s.Extension, Without: s.Without}
	}
	return out
}
