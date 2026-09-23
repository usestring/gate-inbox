package accounts

import (
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

type contextSnapshot struct {
	conversation, model, borrower, lender string
	at                                    time.Time
	usage                                 promptcache.Usage
}

func recordSessionUsage(st *store.Store, tools map[string]config.Tool, running func(string) bool, reader *promptcache.Reader, last map[string]contextSnapshot) error {
	sessions, err := st.ListSessions(false)
	if err != nil {
		return err
	}
	active := map[string]bool{}
	for _, session := range sessions {
		if tools[session.Tool].AccountEnv != "CLAUDE_CODE_OAUTH_TOKEN" || session.AgentSessionID == "" || !running(session.ID) {
			continue
		}
		active[session.ID] = true
		state := reader.Lookup(session.Cwd, session.AgentSessionID)
		if state.Usage == nil || state.LastTurnAt.After(time.Now()) || state.LastTurnAt.Before(session.LaunchTime()) {
			continue
		}
		borrower, err := st.Setting("account_borrower:" + session.ID)
		if err != nil {
			return err
		}
		snapshot := contextSnapshot{session.AgentSessionID, state.Model, borrower, Normalize(session.Account), state.LastTurnAt, *state.Usage}
		if previous, ok := last[session.ID]; ok && previous == snapshot {
			continue
		}
		u := snapshot.usage
		now := time.Now()
		tracing.Record("account.context", now, now, nil,
			tracing.Attr{Key: "session", Value: session.ID},
			tracing.Attr{Key: "tool", Value: session.Tool},
			tracing.Attr{Key: "model", Value: state.Model},
			tracing.Attr{Key: "account.borrower", Value: borrower},
			tracing.Attr{Key: "account.lender", Value: snapshot.lender},
			tracing.Attr{Key: "account.attributed", Value: borrower != "" && snapshot.lender != ""},
			tracing.Attr{Key: "usage.observed_at", Value: state.LastTurnAt.Format(time.RFC3339Nano)},
			tracing.Attr{Key: "usage.context_tokens", Value: u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens},
			tracing.Attr{Key: "usage.input_tokens", Value: u.InputTokens},
			tracing.Attr{Key: "usage.output_tokens", Value: u.OutputTokens},
			tracing.Attr{Key: "usage.cache_read_input_tokens", Value: u.CacheReadInputTokens},
			tracing.Attr{Key: "usage.cache_creation_input_tokens", Value: u.CacheCreationInputTokens})
		last[session.ID] = snapshot
	}
	for id := range last {
		if !active[id] {
			delete(last, id)
		}
	}
	return nil
}
