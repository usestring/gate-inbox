package store

import "github.com/usestring/gate-inbox/internal/status"

// A parent at rest on its fan-out.
//
// A session that hands work to children and ends its turn reads finished on
// its own pane, and that is the one thing it is not: its children's results
// are still on their way back, and each one is typed into its prompt as it
// lands. Triage handed such a parent to the operator as done, and the
// operator found a session with nothing to say yet.
//
// So a parent at rest with live work under it reads delegating. The state is
// never stored: the pane says finished or idle, and that is what the poller
// derives from and writes, so every reading of the pane stays the pane's. It
// is laid over the rows on their way to whoever reads them, from the same
// rows and the same inbox, which is what keeps it from feeding back into the
// derive that produced them.
//
// A parent leaves it when nothing under it is still going and the last
// child's rest notice has been typed into it. Before that notice lands the
// parent still has a result coming, and between the two the parent would
// otherwise flash finished for a pass and be handed over for it.

// PendingChildReports is every session that has a child's rest notice queued
// for it and not yet typed in, keyed by the session it is queued for.
func (s *Store) PendingChildReports() (map[string]bool, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT i.session_id
  FROM session_inbox i
  JOIN sessions c ON c.id = i.sender_id
 WHERE i.delivered_at = 0 AND i.dropped_at = 0
   AND i.subject = ? || c.id
   AND c.parent_id != ''
   AND `+spawnerColumnOf("c")+` = i.session_id`, ChildRestSubject(""))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pending := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		pending[id] = true
	}
	return pending, rows.Err()
}

// delegatingFrom is the statuses a parent can be resting in and still read
// delegating. Waiting and errored are left alone: a parent asking something,
// or stopped on a limit, needs a person whatever its children are doing.
var delegatingFrom = map[string]bool{status.Finished: true, status.Idle: true}

// activeChildStates is what a child can be in and still owe its parent a
// result. A delegating child counts: its own result is still coming, and so
// is the report it will send up once it has it. A background child counts
// too: the shell it left running wakes it into another turn.
var activeChildStates = map[string]bool{
	status.Starting:   true,
	status.Working:    true,
	status.Waiting:    true,
	status.Delegating: true,
	status.Background: true,
}

// ApplyDelegation sets Status to delegating on every row at rest with work
// still owed to it: a child it tracks that is still going, or a child's rest
// notice not yet typed into it. pending is PendingChildReports. shell says
// which rows are terminals, which run until somebody closes them and report
// nothing, so they never hold a parent.
//
// It rewrites sessions in place and returns it. Archived rows are neither
// held nor holding.
func ApplyDelegation(sessions []Session, pending map[string]bool, shell func(Session) bool) []Session {
	byTracker := make(map[string][]int, len(sessions))
	for i, sess := range sessions {
		if sess.Archived || (shell != nil && shell(sess)) {
			continue
		}
		if tracker := TrackerOf(sess); tracker != "" {
			byTracker[tracker] = append(byTracker[tracker], i)
		}
	}
	const (
		unseen = iota
		visiting
		done
	)
	state := make([]int, len(sessions))
	var resolve func(i int) string
	resolve = func(i int) string {
		switch state[i] {
		case visiting:
			// A tracking loop is a corrupt tree, not a fan-out; leave the
			// row as its pane reads.
			return sessions[i].Status
		case done:
			return sessions[i].Status
		}
		state[i] = visiting
		sess := sessions[i]
		held := pending[sess.ID]
		for _, kid := range byTracker[sess.ID] {
			if activeChildStates[resolve(kid)] {
				held = true
			}
		}
		if held && !sess.Archived && delegatingFrom[sess.Status] {
			sessions[i].Status = status.Delegating
		}
		state[i] = done
		return sessions[i].Status
	}
	for i := range sessions {
		resolve(i)
	}
	return sessions
}
