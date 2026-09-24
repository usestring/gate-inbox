package store

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
)

// ListKey is a row's place in ListSessions' order, which a caller paging
// through the list resumes after. Resuming by key rather than by count is
// what keeps a page exact while the list changes under it: a row archived
// or inserted elsewhere moves every later row's index, never its key.
//
// The order is the query's -- group, sort order, creation, then the row's
// insertion order, which is how ties were always broken -- except that
// a migration's linked sessions are drawn together where its first member
// falls (see OrderLinkedSessions). So a row's key is its block's lead's
// place, then its own.
//
// That order is mutable: a reorder, a group move or a seat taken by a
// replacement changes a row's sort order, so a cursor over it can skip or
// repeat a row moved mid-scan. A caller that needs every row exactly once
// pages in creation order instead (see ByCreation), whose key never moves.
type ListKey struct {
	Lead, Row rowKey
	// Creation marks a key in creation order, so a cursor is never read
	// against the other order.
	Creation bool `json:",omitempty"`
}

type rowKey struct {
	Group     string
	SortOrder int
	// Created is the column as stored, not a decoded time: the query orders
	// by the raw value, and older rows hold seconds where newer ones hold
	// nanoseconds.
	Created int64
	RowID   int64
}

func (k rowKey) less(o rowKey) bool {
	switch {
	case k.Group != o.Group:
		return k.Group < o.Group
	case k.SortOrder != o.SortOrder:
		return k.SortOrder < o.SortOrder
	case k.Created != o.Created:
		return k.Created < o.Created
	}
	return k.RowID < o.RowID
}

// After reports whether k comes after o in the list.
func (k ListKey) After(o ListKey) bool {
	if k.Lead != o.Lead {
		return o.Lead.less(k.Lead)
	}
	return o.Row.less(k.Row)
}

// ListKeys is each row's key, for sessions as ListSessions returned them.
func ListKeys(sessions []Session) []ListKey {
	keys := make([]ListKey, 0, len(sessions))
	for _, block := range migrationBlocks(sessions) {
		for _, sess := range block {
			keys = append(keys, ListKey{Lead: block[0].position, Row: sess.position})
		}
	}
	return keys
}

// ByCreation is sessions in creation order -- created_at as stored, then
// rowid -- with each row's key in that order. Neither part changes after
// insert, so a cursor over this order neither skips nor repeats a row
// however the board is rearranged between pages.
func ByCreation(sessions []Session) ([]Session, []ListKey) {
	ordered := slices.Clone(sessions)
	slices.SortStableFunc(ordered, func(a, b Session) int {
		return cmp.Or(cmp.Compare(a.position.Created, b.position.Created), cmp.Compare(a.position.RowID, b.position.RowID))
	})
	keys := make([]ListKey, len(ordered))
	for i, sess := range ordered {
		keys[i] = ListKey{Row: rowKey{Created: sess.position.Created, RowID: sess.position.RowID}, Creation: true}
	}
	return ordered, keys
}

// String is the key as an opaque cursor.
func (k ListKey) String() string {
	raw, _ := json.Marshal(k)
	return base64.RawURLEncoding.EncodeToString(raw)
}

var errBadCursor = errors.New("cursor is not one a list returned")

// ParseListKey reads a cursor String wrote.
func ParseListKey(cursor string) (ListKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return ListKey{}, errBadCursor
	}
	var k ListKey
	if err := json.Unmarshal(raw, &k); err != nil || k.Row.RowID == 0 {
		return ListKey{}, errBadCursor
	}
	return k, nil
}
