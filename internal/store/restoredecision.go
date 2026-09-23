package store

import (
	"encoding/json"
	"time"
)

// The startup restore offer asks about panes the operator has already lost
// once. Answering it -- restoring the fleet, picking part of it, or waving
// the whole thing away -- settles those rows, and a second launch that finds
// them still dead must not ask again: an offer that comes back every start is
// one the operator learns to dismiss without reading.
//
// The mark carries the launch of the agent that was lost, not just the id,
// because the decision is about that loss: a session brought back by hand and
// lost again launched a second time, and asking about that one is the whole
// point of the prompt. It is not keyed on the death itself -- the pass that
// opens the prompt is the pass that wrote the death, and the row it holds
// still carries the LastStatusAt from before it, so a death-keyed mark would
// be written stale and never match again.
//
// A settings row rather than a column, for park's reason: the set is one fact
// about the board, written whole on each answer, and a column would need a
// migration for a field every row but a handful carries empty.
const restoreDecidedSetting = "restore_decided"

// RestoreDecided maps session id to the LaunchTime of the agent the operator
// answered the offer about.
func (s *Store) RestoreDecided() (map[string]time.Time, error) {
	raw, err := s.Setting(restoreDecidedSetting)
	if err != nil || raw == "" {
		return nil, err
	}
	var encoded map[string]int64
	if err := json.Unmarshal([]byte(raw), &encoded); err != nil {
		return nil, err
	}
	decided := make(map[string]time.Time, len(encoded))
	for id, at := range encoded {
		decided[id] = decodeTime(at)
	}
	return decided, nil
}

// SetRestoreDecided replaces the marks. An empty set clears the row.
func (s *Store) SetRestoreDecided(decided map[string]time.Time) error {
	if len(decided) == 0 {
		return s.SetSetting(restoreDecidedSetting, "")
	}
	encoded := make(map[string]int64, len(decided))
	for id, at := range decided {
		encoded[id] = encodeTime(at)
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return err
	}
	return s.SetSetting(restoreDecidedSetting, string(raw))
}
