// Package sweep reports the board's stale rows and what to do with each,
// and does none of it. The board's own cleanup only files finished
// children: a top-level session left idle for days, a session parked on a
// question nobody answers, an errored one, a row whose agent never came up
// and a message no session ever took stay on the list, and the board keeps
// checking them on every pass. A sweep lists each with its age, the question it holds, the
// mail waiting for it, and the one call that would clear it, so whoever
// reads the report decides.
package sweep

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Action is what a proposal suggests.
type Action string

const (
	// Archive files a row nobody is using; revive_session brings it back.
	Archive Action = "archive"
	// Revive resumes a session that ended with work or mail still due.
	Revive Action = "revive"
	// Kill stops a session parked on a question for longer than anyone
	// was going to answer it.
	Kill Action = "kill"
	// Check asks what became of a message held for a session that is
	// running: none of the other three is right before message_status says why.
	Check Action = "check"
)

// actionOrder is the order the report lists actions in: the cheapest
// and safest first.
var actionOrder = []Action{Archive, Revive, Kill, Check}

// Thresholds are how long a row sits in each state before the sweep calls
// it stale.
type Thresholds struct {
	Idle    time.Duration // idle or finished
	Waiting time.Duration // on a question or permission dialog
	Errored time.Duration
	Dead    time.Duration // ended but still on the list
	Mail    time.Duration // a message not yet typed in
}

// DefaultThresholds keep a working day's pauses off the report.
var DefaultThresholds = Thresholds{
	Idle:    72 * time.Hour,
	Waiting: 24 * time.Hour,
	Errored: 24 * time.Hour,
	Dead:    time.Hour,
	Mail:    time.Hour,
}

// Question is the dialog a session's pane is holding.
type Question struct {
	Header  string    `json:"header,omitempty"`
	Text    string    `json:"text"`
	AskedAt time.Time `json:"asked_at,omitzero"`
}

// Mail is one waiting message.
type Mail struct {
	ID     int64     `json:"id"`
	From   string    `json:"from"`
	SentAt time.Time `json:"sent_at"`
	Age    string    `json:"age"`
}

// Proposal is one row and the call that would clear it.
type Proposal struct {
	SessionID string    `json:"session_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Archived  bool      `json:"archived,omitempty"`
	Parent    string    `json:"parent,omitempty"`
	Age       string    `json:"age"`
	Since     string    `json:"since"`
	Reason    string    `json:"reason"`
	Question  *Question `json:"question,omitempty"`
	Mail      []Mail    `json:"mail,omitempty"`
	Action    Action    `json:"action"`
	// Calls are the MCP calls that carry the action out, in order.
	Calls []string `json:"calls"`
	age   time.Duration
}

// Report is everything one sweep found.
type Report struct {
	Proposals []Proposal `json:"proposals"`
	// Orphaned counts waiting messages whose recipient has no row at all;
	// no call delivers them.
	Orphaned int            `json:"orphaned_messages,omitempty"`
	Counts   map[Action]int `json:"counts"`
	Rows     int            `json:"rows_scanned"`
}

// Input is what a sweep reads.
type Input struct {
	Sessions []store.Session
	Messages []store.InboxMessage
	// Question returns the dialog a session is holding, if one is recorded.
	Question func(id string) (Question, bool)
	Now      time.Time
}

// Sweep builds the report. It never acts.
func Sweep(in Input, limits Thresholds) Report {
	byID := map[string]store.Session{}
	for _, sess := range in.Sessions {
		byID[sess.ID] = sess
	}
	mail := map[string][]Mail{}
	report := Report{Counts: map[Action]int{}}
	for _, msg := range in.Messages {
		if in.Now.Sub(msg.SentAt) < limits.Mail {
			continue
		}
		if _, ok := byID[msg.SessionID]; !ok {
			report.Orphaned++
			continue
		}
		from := msg.SenderName
		if from == "" {
			from = msg.SenderID
		}
		mail[msg.SessionID] = append(mail[msg.SessionID], Mail{
			ID: msg.ID, From: from, SentAt: msg.SentAt, Age: Age(in.Now.Sub(msg.SentAt)),
		})
	}
	for _, sess := range in.Sessions {
		if sess.Archived && len(mail[sess.ID]) == 0 {
			continue
		}
		report.Rows++
		since := sess.LastStatusAt
		if since.IsZero() {
			since = sess.CreatedAt
		}
		age := in.Now.Sub(since)
		proposal, ok := propose(sess, age, len(mail[sess.ID]) > 0, limits)
		if !ok {
			continue
		}
		proposal.SessionID, proposal.Name, proposal.Status = sess.ID, sess.Name, sess.Status
		proposal.Archived, proposal.Parent = sess.Archived, sess.ParentID
		proposal.Age, proposal.Since, proposal.age = Age(age), since.UTC().Format(time.RFC3339), age
		proposal.Mail = mail[sess.ID]
		if in.Question != nil && sess.Status == status.Waiting {
			if q, ok := in.Question(sess.ID); ok {
				proposal.Question = &q
			}
		}
		if proposal.Action == Check {
			for _, m := range proposal.Mail {
				proposal.Calls = append(proposal.Calls, call("message_status", map[string]any{"message_id": m.ID}))
			}
		}
		report.Proposals = append(report.Proposals, proposal)
		report.Counts[proposal.Action]++
	}
	slices.SortStableFunc(report.Proposals, func(a, b Proposal) int {
		return cmp.Or(
			cmp.Compare(slices.Index(actionOrder, a.Action), slices.Index(actionOrder, b.Action)),
			cmp.Compare(b.age, a.age),
			cmp.Compare(a.SessionID, b.SessionID))
	})
	return report
}

// propose picks the action for one row, or reports it is not stale.
func propose(sess store.Session, age time.Duration, hasMail bool, limits Thresholds) (Proposal, bool) {
	target := map[string]any{"session_id": sess.ID}
	revive := call("revive_session", target)
	archive := call("archive_session", target)
	switch {
	case sess.Archived:
		// An archived row is only here for mail it never got.
		return Proposal{Action: Revive, Reason: "archived with mail it never received",
			Calls: []string{call("archive_session", map[string]any{"session_id": sess.ID, "archived": false}), revive}}, true
	case sess.Status == status.Dead && sess.AgentSessionID == "" && age >= limits.Dead:
		return Proposal{Action: Archive, Reason: "never launched: no agent conversation was ever recorded",
			Calls: []string{archive}}, true
	case sess.Status == status.Dead && hasMail:
		return Proposal{Action: Revive, Reason: "ended with mail it never received", Calls: []string{revive}}, true
	case sess.Status == status.Dead && age >= limits.Dead:
		return Proposal{Action: Archive, Reason: "ended and still on the list", Calls: []string{archive}}, true
	case sess.Status == status.Errored && age >= limits.Errored:
		if sess.AgentSessionID == "" {
			return Proposal{Action: Archive, Reason: "errored with no conversation to resume", Calls: []string{archive}}, true
		}
		return Proposal{Action: Revive, Reason: "errored; reviving resumes its conversation", Calls: []string{revive}}, true
	case sess.Status == status.Waiting && age >= limits.Waiting:
		return Proposal{Action: Kill, Reason: "parked on a dialog nobody answered; answer it instead if the work still matters",
			Calls: []string{call("kill_session", target)}}, true
	case (sess.Status == status.Idle || sess.Status == status.Finished) && age >= limits.Idle:
		reason := sess.Status + " and untouched"
		if sess.ParentID == "" {
			reason = "top-level and " + sess.Status + "; the board's cleanup only files children"
		}
		return Proposal{Action: Archive, Reason: reason, Calls: []string{archive}}, true
	case hasMail && sess.Status == status.Waiting:
		return Proposal{Action: Check, Reason: "mail is held behind its open dialog; answering the dialog lets it in"}, true
	case hasMail:
		return Proposal{Action: Check, Reason: "running, but mail is held for it"}, true
	}
	return Proposal{}, false
}

func call(tool string, args map[string]any) string {
	raw, _ := json.Marshal(args)
	return "mcp__gate-inbox__" + tool + " " + string(raw)
}

// Age is a duration as the report prints it: minutes under an hour, hours
// under three days, days after.
func Age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 72*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Format is the report as text: a summary line, then each proposal with
// the call that carries it out.
func Format(report Report) string {
	var b strings.Builder
	if len(report.Proposals) == 0 {
		fmt.Fprintf(&b, "Nothing stale in %d rows.\n", report.Rows)
	} else {
		var parts []string
		for _, action := range actionOrder {
			if n := report.Counts[action]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d to %s", n, action))
			}
		}
		fmt.Fprintf(&b, "%s, of %d rows. Nothing was changed: each line below is a proposal.\n", strings.Join(parts, ", "), report.Rows)
	}
	if report.Orphaned > 0 {
		fmt.Fprintf(&b, "%d waiting messages name a session with no row; no call delivers them.\n", report.Orphaned)
	}
	for _, p := range report.Proposals {
		where := "top-level"
		if p.Parent != "" {
			where = "child of " + p.Parent
		}
		state := p.Status
		if p.Archived {
			state += ", archived"
		}
		fmt.Fprintf(&b, "\n%-7s %s %s (%s %s, %s): %s\n", p.Action, p.SessionID, p.Name, state, p.Age, where, p.Reason)
		if q := p.Question; q != nil {
			text := q.Text
			if q.Header != "" {
				text = "[" + q.Header + "] " + text
			}
			fmt.Fprintf(&b, "        asks: %s\n", oneLine(text, 160))
		}
		for _, m := range p.Mail {
			fmt.Fprintf(&b, "        mail %d from %s, waiting %s\n", m.ID, m.From, m.Age)
		}
		for _, c := range p.Calls {
			fmt.Fprintf(&b, "        %s\n", c)
		}
	}
	return b.String()
}

func oneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > limit {
		text = text[:limit-1] + "…"
	}
	return text
}
