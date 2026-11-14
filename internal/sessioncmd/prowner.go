package sessioncmd

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

type PROwnership struct {
	Status     string   `json:"status"`
	URL        string   `json:"url"`
	SessionID  string   `json:"session_id,omitempty"`
	SessionIDs []string `json:"session_ids,omitempty"`
}

func (r *runtime) prAgentTools() []string {
	var names []string
	for name, tool := range r.cfg.Tools {
		if !tool.Shell {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func (r *runtime) prOwnerLive(sess store.Session) (bool, error) {
	if err := r.reach(sess); err != nil {
		return false, err
	}
	// A failed tmux query is not evidence that no worker owns the PR.
	scan, err := r.driver.ScanPanes()
	if err != nil {
		return false, err
	}
	_, live := scan.PIDs[sess.ID]
	return live, nil
}

func (r *runtime) requirePRBoard() error {
	awake, err := r.managerAwake(time.Now())
	if err != nil {
		return err
	}
	if !awake {
		return errors.New("PR owner board is unavailable: no current poller heartbeat")
	}
	return nil
}

// PROwner reads recorded creation evidence and explicit handoffs. A URL
// mention, shared checkout or migration still running beside its source
// grants no custody. It never starts a pane or changes a session.
func (s *Sessions) PROwner(url string) (PROwnership, error) {
	url, err := store.CanonicalPRURL(url)
	if err != nil {
		return PROwnership{}, err
	}
	r, err := s.open()
	if err != nil {
		return PROwnership{}, err
	}
	defer r.store.Close()
	if err := r.requirePRBoard(); err != nil {
		return PROwnership{}, err
	}
	owners, err := r.store.PROwners(url, r.prAgentTools())
	if err != nil {
		return PROwnership{}, err
	}
	result := PROwnership{Status: "none", URL: url}
	for _, owner := range owners {
		live, err := r.prOwnerLive(owner)
		if err != nil {
			return PROwnership{}, err
		}
		if live {
			result.SessionIDs = append(result.SessionIDs, owner.ID)
		}
	}
	switch len(result.SessionIDs) {
	case 0:
	case 1:
		result.Status, result.SessionID = "owned", result.SessionIDs[0]
		result.SessionIDs = nil
	default:
		result.Status = "ambiguous"
	}
	return result, nil
}

func (s *Sessions) PRFeedback(url, eventKey, expectedID, body string) (store.PRDelivery, error) {
	url, err := store.CanonicalPRURL(url)
	if err != nil {
		return store.PRDelivery{}, err
	}
	_, _, err = checkMessage(body, "", maxMessageBytes)
	if err != nil {
		return store.PRDelivery{}, err
	}
	r, err := s.open()
	if err != nil {
		return store.PRDelivery{}, err
	}
	defer r.store.Close()
	boardErr := r.requirePRBoard()
	return r.store.EnqueuePRFeedback(url, eventKey, expectedID, body, r.prAgentTools(), func(sess store.Session) (bool, error) {
		if boardErr != nil {
			return false, boardErr
		}
		live, err := r.prOwnerLive(sess)
		if err != nil || !live {
			return live, err
		}
		if err := r.deliverable(sess); err != nil {
			return false, fmt.Errorf("PR feedback recipient: %w", err)
		}
		return true, nil
	}, time.Now())
}
