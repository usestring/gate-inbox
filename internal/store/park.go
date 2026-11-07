package store

import (
	"encoding/json"
	"slices"
	"strconv"
	"time"
)

// parkedSetting is the settings key holding the ids park stopped and unpark
// has not yet brought back. A settings row rather than a column: the set is
// one fact about the board, written and cleared as a whole, and a column
// would need a migration for a flag every row but a handful carries empty.
const parkedSetting = "parked_sessions"

// interruptedSetting holds the subset of the parked set that was mid-turn
// when park stopped it, so unpark can tell those agents to carry on.
const interruptedSetting = "parked_interrupted"

// Parked returns the ids park stopped, oldest first, that unpark still owes
// a revive.
func (s *Store) Parked() ([]string, error) { return s.idSet(parkedSetting) }

// SetParked replaces the parked set. An empty set clears it.
func (s *Store) SetParked(ids []string) error { return s.setIDSet(parkedSetting, ids) }

// AddParked appends ids to the parked set, keeping the ones already there
// so a second park that finds little left running does not forget the
// first one's work. Order is preserved and ids never repeat.
func (s *Store) AddParked(ids []string) error { return s.addToIDSet(parkedSetting, ids) }

// RemoveParked drops ids from the parked set.
func (s *Store) RemoveParked(ids []string) error { return s.removeFromIDSet(parkedSetting, ids) }

// Interrupted returns the parked ids that were working when park stopped
// them.
func (s *Store) Interrupted() ([]string, error) { return s.idSet(interruptedSetting) }

// AddInterrupted records parked ids that were mid-turn.
func (s *Store) AddInterrupted(ids []string) error { return s.addToIDSet(interruptedSetting, ids) }

// RemoveInterrupted drops ids from the interrupted set.
func (s *Store) RemoveInterrupted(ids []string) error {
	return s.removeFromIDSet(interruptedSetting, ids)
}

// dormantSetting holds the subset of the parked set that was at rest with
// nothing in flight under it when park stopped it: idle, finished, waiting
// on a person, stopped on a limit. Unpark leaves those parked unless asked
// for all of them, so a reboot does not relaunch a fleet of sessions that
// were doing nothing. A parked id absent from it is owed a revive, which is
// also how a set recorded before this one existed reads.
const dormantSetting = "parked_dormant"

// Dormant returns the parked ids unpark leaves parked by default.
func (s *Store) Dormant() ([]string, error) { return s.idSet(dormantSetting) }

// AddDormant records parked ids that had nothing in flight.
func (s *Store) AddDormant(ids []string) error { return s.addToIDSet(dormantSetting, ids) }

// RemoveDormant drops ids from the dormant set.
func (s *Store) RemoveDormant(ids []string) error { return s.removeFromIDSet(dormantSetting, ids) }

// parkedAtSetting is when the last park that stopped anything ran, in unix
// seconds. A starting board brings the in-flight set back only when the host
// has booted since: park is the step before a reboot, and a board restarted
// between the two (an upgrade, a crash) that unparked would put those
// sessions back up just for the reboot to kill them, with the set already
// cleared of them.
const parkedAtSetting = "parked_at"

// ParkedAt is when the last park that stopped anything ran; zero when none
// is recorded.
func (s *Store) ParkedAt() (time.Time, error) {
	raw, err := s.Setting(parkedAtSetting)
	if err != nil || raw == "" {
		return time.Time{}, err
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(secs, 0), nil
}

// SetParkedAt records when a park stopped something.
func (s *Store) SetParkedAt(at time.Time) error {
	return s.SetSetting(parkedAtSetting, strconv.FormatInt(at.Unix(), 10))
}

func (s *Store) idSet(key string) ([]string, error) {
	raw, err := s.Setting(key)
	if err != nil || raw == "" {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) setIDSet(key string, ids []string) error {
	if len(ids) == 0 {
		return s.SetSetting(key, "")
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return s.SetSetting(key, string(raw))
}

func (s *Store) addToIDSet(key string, ids []string) error {
	current, err := s.idSet(key)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !slices.Contains(current, id) {
			current = append(current, id)
		}
	}
	return s.setIDSet(key, current)
}

func (s *Store) removeFromIDSet(key string, ids []string) error {
	current, err := s.idSet(key)
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(current, func(id string) bool { return slices.Contains(ids, id) })
	return s.setIDSet(key, kept)
}

// QueuePendingInput appends one input for the poller to type in once the
// session's agent is ready for it, behind whatever is already queued.
func (s *Store) QueuePendingInput(id, input string) error {
	encoded, inputs, _, err := s.pendingInputState(id)
	if err != nil {
		return err
	}
	next, err := encodePendingInputs(append(inputs, input))
	if err != nil {
		return err
	}
	res, err := s.db.Exec(
		`UPDATE sessions SET pending_inputs = ? WHERE id = ? AND pending_inputs = ?`, next, id, encoded)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// PromoteAdopted turns an adopted row into a managed one: the pane it
// borrowed is gone, and from here the row is revived as gi_<id> like any
// session the manager started. The directory is updated to where the agent
// actually was, and the conversation id is recorded when one was found,
// so revive resumes that conversation rather than the directory's latest.
func (s *Store) PromoteAdopted(id, cwd, agentSessionID string) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET tmux_socket = '', tmux_pane_id = '', cwd = ?,
		 agent_session_id = CASE WHEN ? != '' THEN ? ELSE agent_session_id END
		 WHERE id = ?`, cwd, agentSessionID, agentSessionID, id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}
