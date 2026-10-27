package migrate

import (
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/store"
)

// AccountSwitchTranscript is source's transcript and whether its context is
// over cutoff (config.Tool.AccountSwitchCutoff), so that moving it to another
// account should start a new conversation instead of resuming it.
func AccountSwitchTranscript(roots Roots, tool config.Tool, source store.Session, cutoff int) (Transcript, bool) {
	if Format(source.Tool, tool) != "claude" {
		return Transcript{}, false
	}
	transcript, err := Locate(roots, source.Tool, tool, source)
	if err != nil {
		return Transcript{}, false
	}
	state := promptcache.ReadTranscript(transcript.Path)
	u := state.Usage
	if u == nil || state.LastTurnAt.After(time.Now()) {
		return Transcript{}, false
	}
	return transcript, u.InputTokens+u.CacheReadInputTokens+u.CacheCreationInputTokens > cutoff
}
