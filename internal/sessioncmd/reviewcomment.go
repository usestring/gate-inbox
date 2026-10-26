package sessioncmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// ReviewCommentInput is one inline comment an agent drafts on a pull request it was asked to review.
type ReviewCommentInput struct {
	Repo    string
	Number  int
	Path    string
	Line    int
	Side    string
	Body    string
	Kind    string
	Context string
}

// ReviewComment stores a proposed draft on an open review ask. Nothing reaches GitHub until the
// operator submits the review from the pull request's page.
func ReviewComment(configDir, sessionID string, in ReviewCommentInput, now time.Time) (string, error) {
	if err := validSession(sessionID); err != nil {
		return "", err
	}
	in.Side = strings.ToUpper(strings.TrimSpace(in.Side))
	if in.Side == "" {
		in.Side = "RIGHT"
	}
	if in.Kind == "" {
		in.Kind = "finding"
	}
	switch {
	case !strings.Contains(in.Repo, "/") || in.Number <= 0:
		return "", fmt.Errorf("name the pull request as repo owner/name and its number")
	case strings.TrimSpace(in.Path) == "" || in.Line <= 0:
		return "", fmt.Errorf("name the file path and a line number of the diff")
	case in.Side != "RIGHT" && in.Side != "LEFT":
		return "", fmt.Errorf("side %q is not RIGHT (the new file) or LEFT (the old one)", in.Side)
	case strings.TrimSpace(in.Body) == "":
		return "", fmt.Errorf("the comment body is empty")
	case in.Kind != "finding" && in.Kind != "question":
		return "", fmt.Errorf("kind %q is not finding or question", in.Kind)
	}
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		return "", err
	}
	defer st.Close()
	asks, err := st.OpenReviewRequests()
	if err != nil {
		return "", err
	}
	head, asked := "", false
	for _, ask := range asks {
		if ask.Repo == in.Repo && ask.Number == in.Number {
			head, asked = ask.HeadSHA, true
			break
		}
	}
	if !asked {
		return "", fmt.Errorf("%s#%d has no open review ask on this board, so there is no review to draft into", in.Repo, in.Number)
	}
	draft, err := st.AddReviewDraft(store.ReviewDraft{
		Repo: in.Repo, Number: in.Number, HeadSHA: head, Path: in.Path, Line: in.Line, Side: in.Side,
		Body: in.Body, Kind: in.Kind, Context: in.Context, Author: store.DraftByAgent, SessionID: sessionID,
	}, now)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("drafted comment %d on %s:%d of %s#%d; the operator keeps, edits or drops it before the review is submitted",
		draft.ID, in.Path, in.Line, in.Repo, in.Number), nil
}
