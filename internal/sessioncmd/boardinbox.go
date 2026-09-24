package sessioncmd

import "github.com/usestring/gate-inbox/internal/store"

const (
	// boardInboxDefault and boardInboxCap bound one BoardMessages read.
	boardInboxDefault = 50
	boardInboxCap     = 500
)

// BoardMessages is what an agent session has been sent, newest first, read
// on the board's behalf. Messages dropped before delivery are left out.
func (s *Sessions) BoardMessages(targetID string, filter store.InboxFilter) (messages []store.InboxMessage, err error) {
	defer start("sessioncmd.board.messages", sessionAttr(targetID)).done(&err)
	switch {
	case filter.Limit <= 0:
		filter.Limit = boardInboxDefault
	case filter.Limit > boardInboxCap:
		filter.Limit = boardInboxCap
	}
	runtime, err := s.open()
	if err != nil {
		return nil, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return nil, err
	}
	return runtime.store.Inbox(target.ID, filter)
}
