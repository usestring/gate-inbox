package migrate

import (
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/store"
)

func AccountSwitchTranscript(roots Roots, tool config.Tool, source store.Session) (Transcript, bool) {
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
	return transcript, u.InputTokens+u.CacheReadInputTokens+u.CacheCreationInputTokens > 200_000
}
