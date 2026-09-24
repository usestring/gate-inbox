package store

import (
	"fmt"
	"testing"
)

// Keys rise strictly down the list ListSessions returns, including across a
// migration block drawn up to its first member and rows whose creation time
// is stored in legacy seconds, and a cursor round-trips.
func TestListKeysRiseDownTheList(t *testing.T) {
	rowid := int64(0)
	row := func(id, group string, order int, created int64, link string) Session {
		rowid++
		return Session{ID: id, Group: group, MigrationID: link, position: rowKey{Group: group, SortOrder: order, Created: created, RowID: rowid}}
	}
	// In query order: m2 is linked to m1, so it is drawn up beside m1,
	// ahead of b and c.
	queried := []Session{
		row("a", "g", 0, 1_700_000_000, ""),
		row("m1", "g", 1, 1_700_000_000_000_000_000, "link"),
		row("b", "g", 2, 5, ""),
		row("c", "g", 2, 6, ""),
		row("m2", "g", 3, 1, "link"),
		row("d", "h", 0, 0, ""),
	}
	ordered := OrderLinkedSessions(queried)
	keys := ListKeys(ordered)
	var ids []string
	for i, sess := range ordered {
		ids = append(ids, sess.ID)
		if i > 0 && !keys[i].After(keys[i-1]) {
			t.Fatalf("key of %s is not after key of %s", sess.ID, ordered[i-1].ID)
		}
		if i > 0 && keys[i-1].After(keys[i]) {
			t.Fatalf("key of %s is after key of %s", ordered[i-1].ID, sess.ID)
		}
		parsed, err := ParseListKey(keys[i].String())
		if err != nil || parsed != keys[i] {
			t.Fatalf("cursor of %s round-tripped to %+v, %v", sess.ID, parsed, err)
		}
	}
	if got := ids; len(got) != 6 || got[1] != "m1" || got[2] != "m2" {
		t.Fatalf("order = %v, want m2 drawn up beside m1", got)
	}
	for _, bad := range []string{"", "!!", "e30"} {
		if _, err := ParseListKey(bad); err == nil {
			t.Fatalf("cursor %q was accepted", bad)
		}
	}
}

// pageChildren reads one page of parent's children in creation order, as
// sessioncmd's list does for a parent filter: the rows after the cursor, at
// most limit, and the cursor of the last when more follow.
func pageChildren(t *testing.T, st *Store, parent string, after *ListKey, limit int) ([]string, *ListKey) {
	t.Helper()
	stored, err := st.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	ordered, keys := ByCreation(stored)
	var ids []string
	var last ListKey
	for i, sess := range ordered {
		if sess.ParentID != parent || (after != nil && !keys[i].After(*after)) {
			continue
		}
		if len(ids) == limit {
			return ids, &last
		}
		ids = append(ids, sess.ID)
		last = keys[i]
	}
	return ids, nil
}

// A child not yet read is moved to the top of its seat mid-scan, where the
// board's order would put it behind the cursor. Creation order does not
// move it, so every child is still read once.
func TestCreationCursorReadsARowMovedMidScan(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("parent", "g")); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		kid := sample(fmt.Sprintf("kid%d", i), "g")
		kid.ParentID = "parent"
		if err := st.CreateSession(kid); err != nil {
			t.Fatal(err)
		}
	}
	first, cursor := pageChildren(t, st, "parent", nil, 3)
	if len(first) != 3 || cursor == nil {
		t.Fatalf("first page = %v, %v; want three rows and a cursor", first, cursor)
	}
	for {
		moved, err := st.ReorderSession("kid5", -1, false)
		if err != nil {
			t.Fatal(err)
		}
		if !moved {
			break
		}
	}
	seen := map[string]int{}
	for _, id := range first {
		seen[id]++
	}
	for cursor != nil {
		var ids []string
		ids, cursor = pageChildren(t, st, "parent", cursor, 3)
		for _, id := range ids {
			seen[id]++
		}
	}
	for i := range 6 {
		if id := fmt.Sprintf("kid%d", i); seen[id] != 1 {
			t.Errorf("%s read %d times, want once: %v", id, seen[id], seen)
		}
	}
}
