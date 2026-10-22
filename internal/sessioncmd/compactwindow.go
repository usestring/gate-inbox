package sessioncmd

import (
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/store"
)

// writeSpawnCompactWindow gives a spawn the auto-compact window its tier
// earns, its tracker's included (store.InheritedTier), before its launch
// command is composed. Claude Code reads the window at launch only and the
// command names the session's own settings file only when one exists, so
// leaving this to the board's next pass would start the child on the CLI's
// own window and keep it there until a restart.
func writeSpawnCompactWindow(st *store.Store, manager *hooks.Manager, tool config.Tool, sess store.Session) error {
	if len(tool.CompactWindow) == 0 || tool.StatusSource != hooks.StatusSourceClaude {
		return nil
	}
	groups, err := st.Groups()
	if err != nil {
		return err
	}
	groupTiers := make(map[string]priority.Tier, len(groups))
	for _, g := range groups {
		if g.Priority != priority.Unset {
			groupTiers[g.Name] = g.Priority
		}
	}
	lookup := func(id string) (store.Session, bool) {
		sess, err := st.Get(id)
		return sess, err == nil
	}
	return manager.SetSessionCompactWindow(sess.ID, tool.CompactWindowFor(store.InheritedTier(groupTiers, sess, lookup)))
}
