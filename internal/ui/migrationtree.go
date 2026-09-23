package ui

import "github.com/usestring/gate-inbox/internal/store"

// A visual container connects peer panes without adding a keyboard stop or
// making one session own another's archive, focus, or work actions.
func connectMigrationRows(rows []treeRow) {
	for start := 0; start < len(rows); start++ {
		first := rows[start]
		if !first.isSession() || first.sess.MigrationID == "" {
			continue
		}
		end, last := start+1, start
		for end < len(rows) {
			next := rows[end]
			if next.depth > first.depth {
				end++
				continue
			}
			if next.depth != first.depth || !next.isSession() || !store.Linked(first.sess, next.sess) {
				break
			}
			last = end
			end++
		}
		if last == start {
			continue
		}
		for i := start; i < end; i++ {
			rows[i].depth++
			rows[i].migrationDepth = first.depth + 1
		}
		rows[start].migrationHead = true
		start = end - 1
	}
}
