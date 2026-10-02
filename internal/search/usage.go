package search

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/usestring/gate-inbox/internal/promptcache"
)

type TokenUsage struct {
	Tokens   int
	Capacity int
	Known    bool
}

func ReadTokenUsage(target Target) TokenUsage {
	if target.Tool == ToolClaude {
		state := promptcache.ReadTranscript(target.Path)
		if u := state.Usage; u != nil {
			return TokenUsage{Tokens: u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens + u.OutputTokens, Known: true}
		}
		return TokenUsage{}
	}
	if target.Tool != ToolCodex {
		return TokenUsage{}
	}
	f, err := os.Open(target.Path)
	if err != nil {
		return TokenUsage{}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return TokenUsage{}
	}
	// Only recent context matters; a long rollout must not be read in full every poll.
	for window := int64(256 << 10); ; window *= 2 {
		window = min(window, info.Size(), 16<<20)
		start := info.Size() - window
		data, err := io.ReadAll(io.NewSectionReader(f, start, window))
		if err != nil {
			return TokenUsage{}
		}
		if start > 0 {
			_, data, _ = bytes.Cut(data, []byte("\n"))
		}
		lines := bytes.Split(data, []byte("\n"))
		for i := len(lines) - 1; i >= 0; i-- {
			line := lines[i]
			var record struct {
				Type    string `json:"type"`
				Payload struct {
					Type string `json:"type"`
					Info *struct {
						Last *struct {
							Total *int `json:"total_tokens"`
						} `json:"last_token_usage"`
						Capacity int `json:"model_context_window"`
					} `json:"info"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" || record.Payload.Type != "token_count" || record.Payload.Info == nil || record.Payload.Info.Last == nil || record.Payload.Info.Last.Total == nil {
				continue
			}
			u := record.Payload.Info
			if *u.Last.Total < 0 || u.Capacity < 0 {
				return TokenUsage{}
			}
			return TokenUsage{Tokens: *u.Last.Total, Capacity: u.Capacity, Known: true}
		}
		if start == 0 || window == 16<<20 {
			return TokenUsage{}
		}
	}
}
