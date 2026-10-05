package app

import "github.com/usestring/gate-inbox/internal/restartpresets"

// RestartFlag is one entry of restart_flags.json: a key and the extra CLI
// flags it restarts the session with. Options.RestartFlagDefaults is a list
// of them.
type RestartFlag struct {
	// Key is the menu key: a single letter a-z.
	Key string
	// Label is what the board calls the preset. Empty falls back to Args.
	Label string
	// Args are the extra CLI flags, e.g. "--chrome".
	Args string
}

// useRestartFlagDefaults hands the build's restart presets to the loader. It
// is its own step so Run refuses an entry that could never bind before any
// face runs.
func useRestartFlagDefaults(entries []RestartFlag) error {
	converted := make([]restartpresets.Preset, len(entries))
	for i, e := range entries {
		converted[i] = restartpresets.Preset{Key: e.Key, Label: e.Label, Args: e.Args}
	}
	_, err := restartpresets.UseDistribution(converted)
	return err
}
