package store

import "fmt"

// Who spawned a row, as opposed to where it is drawn.
//
// parent_id answers both questions today and cannot: the tree carries one
// level on purpose -- childDepthCap in the ui package exists because relaxing
// that would walk the list off the right edge -- so a session that is already
// a child cannot become a parent, and validParent files the grandchild under
// the caller's own parent instead. That is the right answer for the board,
// which draws one level, and the wrong answer for every question about
// ownership: 48 of 48 second-generation spawns on the board this was written
// against were hoisted to a root manager that had not asked for any of them.
//
// What broke was not the drawing. send_children told a session that had just
// spawned five that it had no children, answer_session refused a session its
// own child's dialog, and every relay went to a root flooded with work it did
// not assign. place_session could not repair any of it either, since adopting
// a grandchild would give its real spawner both a parent and a child, which is
// the case validParent refuses.
//
// So the two questions get two columns. parent_id stays exactly what it was,
// one level deep, and the board draws from it unchanged. spawned_by records
// the session that actually called create_session, at whatever depth, and
// ownership keys on that: who may answer a dialog, who send_children reaches,
// and who hears that a child stopped or finished.

// spawnerColumnOf selects a row's owner, falling back to the session it is
// filed under. The fallback is what makes a store written before this column
// behave as it did -- every row on it was filed under its spawner or under
// that spawner's root, and the root is the answer those rows have always
// given. One definition, taking the table alias, because a query that spelled
// the fallback out again could drift and the two would then disagree about who
// owns a row.
func spawnerColumnOf(alias string) string {
	return fmt.Sprintf(`COALESCE(NULLIF(%[1]s.spawned_by, ''), %[1]s.parent_id)`, alias)
}

// SpawnerOf is spawnerColumnOf in Go, for a row already in hand.
func SpawnerOf(sess Session) string {
	if sess.SpawnedBy != "" {
		return sess.SpawnedBy
	}
	return sess.ParentID
}
