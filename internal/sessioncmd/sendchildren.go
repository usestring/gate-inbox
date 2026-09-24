package sessioncmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Telling a whole fan-out one thing.
//
// Some corrections are about the task, not the worker: the branch moved, the
// endpoint changed, stop using that field. A parent holding nine children has
// to send nine messages to say it, and the ninth is written twenty minutes
// after the first, by which time the first four have acted on the old
// instruction. Nothing here is new capability -- it is send_session nine
// times, in one call, at one moment.
//
// Opt-in on purpose, and not the default: most messages are for one session,
// and a parent that reaches for the whole subtree by accident interrupts eight
// agents mid-turn to tell them something about the ninth. The caller says
// which it means.

// ChildDelivery is one child's outcome, so a partial send says which part.
type ChildDelivery struct {
	SessionID string `json:"session_id" jsonschema:"child session the message was addressed to"`
	Name      string `json:"name" jsonschema:"that session's name"`
	MessageID int64  `json:"message_id,omitempty" jsonschema:"queued message id, when it was queued"`
	Skipped   string `json:"skipped,omitempty" jsonschema:"why this child was not sent to; empty when it was queued"`
}

// ChildSend is what one call did to the caller's fan-out.
type ChildSend struct {
	Queued       int             `json:"queued" jsonschema:"how many children the message was queued for"`
	Skipped      int             `json:"skipped" jsonschema:"how many were passed over, with a reason on each"`
	ManagerAwake bool            `json:"manager_awake" jsonschema:"whether Gate Inbox is running to deliver what was queued"`
	Deliveries   []ChildDelivery `json:"deliveries" jsonschema:"per-child outcome, in board order"`
}

// SendChildren queues message for every session the caller spawned.
//
// A child that cannot take it is reported rather than failing the call: a full
// queue on one child is not a reason to leave the other eight uninstructed,
// and the caller can see exactly which one to chase.
func (s *Sessions) SendChildren(sessionID, message string) (ChildSend, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return ChildSend{}, errors.New("message is empty")
	}
	if len(message) > maxMessageBytes {
		return ChildSend{}, fmt.Errorf(
			"message is %d bytes, over the %d byte limit; shorten it to the instruction and point the agents at a file or a task for the detail",
			len(message), maxMessageBytes)
	}
	runtime, err := s.open()
	if err != nil {
		return ChildSend{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return ChildSend{}, err
	}
	sessions, err := runtime.store.ListSessions(true)
	if err != nil {
		return ChildSend{}, err
	}
	now := time.Now()
	stamp := fingerprint(message)
	result := ChildSend{}
	for _, child := range sessions {
		// spawned_by, not parent_id: a caller that is itself a child has its
		// own spawns filed beside it under their shared root, so reading the
		// fan-out off the tree told such a caller it had no children at all.
		if store.SpawnerOf(child) != caller.ID {
			continue
		}
		delivery := ChildDelivery{SessionID: child.ID, Name: child.Name}
		switch {
		case child.Archived:
			delivery.Skipped = "archived"
		case child.Status == status.Dead:
			delivery.Skipped = "dead"
		// A terminal is a shell the caller opened to watch, not an agent with
		// an inbox, so it is not part of the fan-out this is instructing.
		case runtime.cfg.Tools[child.Tool].Shell:
			delivery.Skipped = "a terminal, not an agent"
		// Nor is a helper an extension launched under the caller for a role
		// that asks to be left out: it works for the extension, not for the
		// task the caller is instructing.
		case skipsSendChildren(child) != "":
			delivery.Skipped = skipsSendChildren(child)
		default:
			if err := runtime.deliverable(child); err != nil {
				delivery.Skipped = err.Error()
				break
			}
			id, _, err := runtime.store.Enqueue(store.InboxMessage{
				SessionID:   child.ID,
				SenderID:    caller.ID,
				SenderName:  caller.Name,
				Body:        message,
				Fingerprint: stamp,
				SentAt:      now,
			}, store.DefaultInboxLimits)
			if err != nil {
				delivery.Skipped = err.Error()
				break
			}
			delivery.MessageID = id
		}
		if delivery.Skipped == "" {
			result.Queued++
		} else {
			result.Skipped++
		}
		result.Deliveries = append(result.Deliveries, delivery)
	}
	if len(result.Deliveries) == 0 {
		return ChildSend{}, errors.New(
			"this session has no children to send to; spawn them with create_session, or send to one session by id")
	}
	awake, err := runtime.managerAwake(now)
	if err != nil {
		return ChildSend{}, err
	}
	result.ManagerAwake = awake
	logging.Info("message queued for a whole fan-out",
		"caller", caller.ID, "queued", result.Queued, "skipped", result.Skipped)
	return result, nil
}

// FormatChildSend leads with the count, then names only what did not go
// through: a caller reading nine successes learns nothing it did not expect.
func FormatChildSend(sent ChildSend) string {
	line := fmt.Sprintf("queued for %d of %d children", sent.Queued, len(sent.Deliveries))
	if !sent.ManagerAwake {
		line += "; Gate Inbox is not running, so they wait until the user opens it"
	}
	for _, delivery := range sent.Deliveries {
		if delivery.Skipped != "" {
			line += fmt.Sprintf("\n- %s (%s) not sent to: %s", delivery.Name, delivery.SessionID, delivery.Skipped)
		}
	}
	return line
}
