package sessioncmd

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A parent granting its child a permission.
//
// A child blocked by its own permission system cannot be unblocked by its
// parent's say-so, and should not be: an answer keyed in by another agent is
// not its user's approval, and the auto-mode classifier refuses a sandbox
// bypass on one. Before this, the only way through was the user editing the
// child's settings by hand. Now the parent asks its user one question Gate
// Inbox wrote, word for word, and Gate Inbox writes the one permission the
// user approved into that child's own settings and restarts it on them.
//
// What is checked, and why:
//
//   - only the session that spawned the child may grant it anything, the same
//     ownership answer_session reads (runtime.child);
//   - the grant passes grant.Check, which refuses wildcards and anything that
//     reaches the permission system or a credential, with no way past it;
//   - the parent's own transcript holds an AskUserQuestion headed Approval
//     whose question and options are ApprovalQuestion's word for word,
//     answered with grantOption by its user -- not by the session that spawned
//     the parent -- and not already spent on another grant. The question names
//     the child and the exact permission, so one approval cannot be stretched
//     to another child or a wider rule.
//
// Where it is written: a settings file of the child's own (hooks'
// SessionSettingsPath), never a project's settings.local.json, which
// every session in that directory reads.

const (
	grantOption  = "Grant"
	refuseOption = "Don't grant"
)

// Every grant is temporary. A parent says how long its child needs the
// permission, the user approves that window along with the permission, and
// the board revokes it when the window closes (ExpireGrants).
const (
	DefaultGrantTTL = 2 * time.Hour
	MaxGrantTTL     = 24 * time.Hour
)

// GrantRequest is one grant or revoke a parent asks for.
type GrantRequest struct {
	Kind   string
	Value  string
	Revoke bool
	// ExpiresIn is how long the grant lasts; zero is DefaultGrantTTL.
	ExpiresIn time.Duration
	// Restart ends and resumes a running child so the change takes effect;
	// without it a working child is left alone and picks the change up at
	// its next launch.
	Restart bool
}

// GrantInfo is one permission on a session, as list_sessions and the grant
// tools report it.
type GrantInfo struct {
	Kind      string    `json:"kind"`
	Value     string    `json:"value"`
	Granted   string    `json:"granted"`
	By        string    `json:"by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	RevokedAt time.Time `json:"revoked_at,omitzero"`
	// RevokedBy is the session that revoked it, or "expired".
	RevokedBy string `json:"revoked_by,omitempty"`
}

// GrantResult is what a grant or revoke did.
type GrantResult struct {
	Target    Session     `json:"target"`
	Action    string      `json:"action"`
	Grant     GrantInfo   `json:"grant"`
	Settings  string      `json:"settings_file,omitempty"`
	Restarted bool        `json:"restarted"`
	Note      string      `json:"note"`
	Grants    []GrantInfo `json:"grants"`
}

// ApprovalNeeded is the refusal of a grant no approval settles yet. It
// carries the question to put to the user, verbatim.
type ApprovalNeeded struct {
	Question convo.AskQuestion
	Reason   string
}

func (e *ApprovalNeeded) Error() string {
	payload, _ := json.Marshal(map[string]any{"questions": []convo.AskQuestion{e.Question}})
	return fmt.Sprintf("this grant needs your user's approval, and %s. Ask your user exactly this "+
		"with your own question tool -- the same header, question and options, as one question -- "+
		"and call grant_permission again once they choose %q: %s", e.Reason, grantOption, payload)
}

// ApprovalQuestion is the question a parent puts to its user to approve g for
// target for ttl. Gate Inbox writes it so the check is an exact match, not a
// reading of somebody's paraphrase, and the window is in it so the user
// approves how long as well as what.
func ApprovalQuestion(target store.Session, g grant.Grant, ttl time.Duration) convo.AskQuestion {
	g = grant.Normalise(g)
	applies := "Gate Inbox writes it into that session's own settings only, restarts it to apply, and revokes it " +
		"when the time is up."
	if target.TmuxPaneID != "" {
		applies = "That session was started outside Gate Inbox, so Gate Inbox applies it to that session only, " +
			"through its hooks where they can carry it and otherwise once it is resumed, and revokes it when the " +
			"time is up."
	}
	return convo.AskQuestion{
		Header: dialog.ApprovalHeader,
		Question: fmt.Sprintf("Grant child session %q (%s), for %s, %s? %s",
			target.Name, target.ID, window(ttl), grant.Describe(g), applies),
		Options: []convo.AskOption{
			{Label: grantOption, Description: "Write this one permission for that session; it can be revoked later"},
			{Label: refuseOption, Description: "Leave the session's permissions as they are"},
		},
	}
}

// window is a grant's lifetime in words.
func window(ttl time.Duration) string {
	if ttl%time.Hour == 0 {
		if ttl == time.Hour {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", int(ttl/time.Hour))
	}
	return fmt.Sprintf("%d minutes", int(ttl.Round(time.Minute)/time.Minute))
}

// grantTTL is the window a request asks for, checked.
func grantTTL(req GrantRequest) (time.Duration, error) {
	ttl := req.ExpiresIn
	if ttl == 0 {
		ttl = DefaultGrantTTL
	}
	if ttl < time.Minute || ttl > MaxGrantTTL {
		return 0, fmt.Errorf("a grant lasts between 1 minute and %s; ask again for a window in that range", window(MaxGrantTTL))
	}
	return ttl.Round(time.Minute), nil
}

// Grant gives a child of the caller one permission its user approved, for a
// window the user approved too, or revokes one.
func (s *Sessions) Grant(sessionID, targetID string, req GrantRequest) (GrantResult, error) {
	runtime, err := s.open()
	if err != nil {
		return GrantResult{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return GrantResult{}, err
	}
	if strings.TrimSpace(targetID) == caller.ID {
		return GrantResult{}, errors.New("that is this session; a session cannot grant itself a permission")
	}
	target, err := runtime.child(caller, targetID)
	if err != nil {
		return GrantResult{}, err
	}
	tool, known := runtime.cfg.Tools[target.Tool]
	if !known {
		return GrantResult{}, fmt.Errorf("tool %s is no longer configured", target.Tool)
	}
	if tool.StatusSource != hooks.StatusSourceClaude {
		return GrantResult{}, unsupportedGrant(target.Tool)
	}
	g := grant.Normalise(grant.Grant{Kind: grant.Kind(req.Kind), Value: req.Value})
	if err := grant.Check(g); err != nil {
		return GrantResult{}, err
	}
	result := GrantResult{Grant: GrantInfo{Kind: string(g.Kind), Value: g.Value, Granted: grant.Describe(g)}}
	if req.Revoke {
		n, err := runtime.store.RevokeGrant(target.ID, string(g.Kind), g.Value, caller.ID)
		if err != nil {
			return GrantResult{}, err
		}
		if n == 0 {
			return GrantResult{}, fmt.Errorf("session %s holds no %s %q to revoke", target.ID, g.Kind, g.Value)
		}
		result.Action = "revoked"
	} else {
		active, err := runtime.store.Grants(target.ID, true)
		if err != nil {
			return GrantResult{}, err
		}
		held := false
		for _, have := range active {
			if have.Kind == string(g.Kind) && have.Value == g.Value {
				held = true
			}
		}
		switch {
		case held && req.Restart:
			// Nothing new is granted, so no approval is spent: this resumes
			// the session on what it already holds, which is how the part of
			// a grant an adopted session could not take live reaches it.
			result.Action = "reapplied"
		case held:
			return GrantResult{}, fmt.Errorf("session %s already holds that permission", target.ID)
		}
	}
	if !req.Revoke && result.Action == "" {
		ttl, err := grantTTL(req)
		if err != nil {
			return GrantResult{}, err
		}
		evidence, hash, err := s.grantApproval(runtime.store, caller, target, g, ttl)
		if err != nil {
			return GrantResult{}, err
		}
		now := time.Now()
		if _, err := runtime.store.RecordGrant(store.PermissionGrant{
			SessionID: target.ID, Kind: string(g.Kind), Value: g.Value,
			GrantedBy: caller.ID, EvidenceToolUseID: evidence, QuestionHash: hash,
			CreatedAt: now, ExpiresAt: now.Add(ttl),
		}); err != nil {
			return GrantResult{}, err
		}
		result.Action = "granted"
	}
	applied, err := s.applyGrants(runtime, target, req.Restart)
	if err != nil {
		return GrantResult{}, err
	}
	result.Settings, result.Restarted, result.Note = applied.settings, applied.restarted, applied.note
	if applied.adopted {
		result.Note = adoptedGrantNote(g, req.Revoke)
	}
	result.Target = runtime.sessionInfo(applied.target, applied.running, false)
	result.Grants, err = grantInfos(runtime.store, target.ID)
	for _, info := range result.Grants {
		if info.Kind == result.Grant.Kind && info.Value == result.Grant.Value {
			result.Grant = info
		}
	}
	return result, err
}

// applied is what applyGrants did.
type applied struct {
	target    store.Session
	settings  string
	running   bool
	restarted bool
	// adopted is a running session adopted from outside the board, left
	// running: its grants apply through its hooks (AdoptedGrantDecision).
	adopted bool
	note    string
}

// applyGrants rewrites target's own settings file from the grants in force on
// it and restarts it on them. Claude Code 2.1.286 does not reload a --settings
// file in a running session (measured: a sandbox exclusion removed from the
// file still applied until the restart), so the restart is what makes a grant
// take effect and, as much, what makes a revoke or an expiry take it away. A
// working session is restarted only when force is set: ending a turn midway
// loses it.
func (s *Sessions) applyGrants(runtime *runtime, target store.Session, force bool) (applied, error) {
	out := applied{target: target}
	rows, err := runtime.store.Grants(target.ID, true)
	if err != nil {
		return out, err
	}
	grants := make([]grant.Grant, len(rows))
	for i, row := range rows {
		grants[i] = grant.Grant{Kind: grant.Kind(row.Kind), Value: row.Value}
	}
	path, err := hooks.NewManager(s.configDir).WriteSessionSettings(target.ID, grants)
	if err != nil {
		return out, fmt.Errorf("the change is recorded but the settings file could not be written: %w", err)
	}
	if len(grants) > 0 {
		out.settings = path
	}
	adopted := target.TmuxPaneID != ""
	if adopted {
		if err := runtime.reach(target); err != nil {
			return out, err
		}
	}
	out.running = runtime.driver.Exists(target.ID)
	switch {
	case !out.running:
		out.note = "the session is not running; it launches with this at its next revive"
	case adopted && !force:
		// Restarting it would move its conversation out of the user's
		// own pane. What its hooks can carry applies from its next call;
		// the rest waits for a resume the caller asks for.
		out.adopted = true
		out.note = "the session was started outside Gate Inbox and keeps running; its hooks apply its grants"
	case target.Status == status.Working && !force:
		out.note = "the session is working, so it was left alone: the change applies when it is next " +
			"restarted; call again with restart true to restart it now"
	default:
		if err := s.endSession(runtime, target, store.EndKilled); err != nil {
			return out, err
		}
		if adopted {
			// Its pane is gone, so it comes back as a board session on its
			// own conversation, launched with its settings file -- which is
			// the resume a sandbox or domain grant needed.
			runtime.driver.Release(target.ID)
			if err := runtime.store.PromoteAdopted(target.ID, target.Cwd, target.AgentSessionID); err != nil {
				return out, err
			}
			target.TmuxSocket, target.TmuxPaneID = "", ""
			out.target = target
		}
		relaunched, err := s.relaunch(runtime, target, "")
		if err != nil {
			return out, fmt.Errorf("the change is written but the session did not come back: %w", err)
		}
		out.target.Status = relaunched.DefaultStatus
		out.restarted = true
		out.note = "the session was restarted on its own conversation with the change applied; it is " +
			"idle, so tell it with send_session what to do next"
	}
	return out, nil
}

// ExpireGrants revokes every grant whose window has closed and restarts its
// session without it. A session in the middle of a turn keeps the grant until
// it rests, when the next pass takes it: revoking it in the record while the
// process still holds it would have the board say a permission is gone that
// the session can still use. It reports how many grants it revoked.
func (s *Sessions) ExpireGrants(now time.Time) (int, error) {
	runtime, err := s.open()
	if err != nil {
		return 0, err
	}
	defer runtime.store.Close()
	expired, err := runtime.store.ExpiredGrants(now)
	if err != nil {
		return 0, err
	}
	byTarget := map[string][]store.PermissionGrant{}
	for _, g := range expired {
		byTarget[g.SessionID] = append(byTarget[g.SessionID], g)
	}
	revoked := 0
	var errs []error
	for id, grants := range byTarget {
		target, err := runtime.store.Get(id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// The session is gone, so there is nothing to restart, but the
			// grant still has to leave the record or it is retried forever.
			target = store.Session{ID: id}
		case err != nil:
			errs = append(errs, err)
			continue
		case target.Status == status.Working && target.TmuxPaneID == "" && runtime.driver.Exists(target.ID):
			// An adopted session's hooks stop honouring a grant the moment
			// it lapses, so only a launched one keeps it until it rests.
			continue
		}
		for _, g := range grants {
			n, err := runtime.store.RevokeGrant(id, g.Kind, g.Value, store.GrantExpired)
			if err != nil {
				errs = append(errs, err)
			}
			revoked += int(n)
		}
		if _, err := s.applyGrants(runtime, target, false); err != nil {
			errs = append(errs, fmt.Errorf("session %s: %w", id, err))
		}
	}
	return revoked, errors.Join(errs...)
}

// Grants lists the permissions granted to a child of the caller, revoked ones
// included.
func (s *Sessions) Grants(sessionID, targetID string) ([]GrantInfo, error) {
	runtime, err := s.open()
	if err != nil {
		return nil, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return nil, err
	}
	target, err := runtime.child(caller, targetID)
	if err != nil {
		return nil, err
	}
	return grantInfos(runtime.store, target.ID)
}

func grantInfos(st *store.Store, id string) ([]GrantInfo, error) {
	rows, err := st.Grants(id, false)
	if err != nil {
		return nil, err
	}
	out := make([]GrantInfo, len(rows))
	for i, row := range rows {
		out[i] = GrantInfo{
			Kind: row.Kind, Value: row.Value, By: row.GrantedBy, CreatedAt: row.CreatedAt,
			ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt, RevokedBy: row.RevokedBy,
			Granted: grant.Describe(grant.Grant{Kind: grant.Kind(row.Kind), Value: row.Value}),
		}
	}
	return out, nil
}

// grantApproval finds the caller's own dialog in which its user approved g for
// target, and returns its tool_use id and the hash of the question in it. The
// checks are relay.go's verify, held to Gate Inbox's own question rather than
// the child's.
func (s *Sessions) grantApproval(st *store.Store, caller, target store.Session, g grant.Grant, ttl time.Duration) (string, string, error) {
	want := ApprovalQuestion(target, g, ttl)
	hash := questionHash(want)
	need := func(reason string) error { return &ApprovalNeeded{Question: want, Reason: reason} }
	path := s.transcriptOf(caller)
	if path == "" {
		return "", "", need("this session's own transcript cannot be found, so there is no dialog of your user's to match")
	}
	asks, err := convo.AnsweredAsks(path, time.Time{})
	if err != nil {
		return "", "", need(fmt.Sprintf("this session's own transcript cannot be read (%v)", err))
	}
	reason := "no dialog of yours asks it yet"
	for i := len(asks) - 1; i >= 0; i-- {
		ask := asks[i]
		for _, q := range ask.Questions {
			if !sameAsk(q, want) || !dialog.IsApproval(q.Header) {
				continue
			}
			given, ok := answerFor(ask.Answers, q.Question)
			switch {
			case !ok:
				reason = "your dialog asking it holds no answer"
				continue
			case normalise(given) != grantOption:
				reason = fmt.Sprintf("your user answered %q", given)
				continue
			}
			used, err := st.GrantEvidenceUsed(ask.ToolUseID, hash)
			if err != nil {
				return "", "", err
			}
			if used {
				reason = "your user's approval in that dialog has already settled a grant; ask them again"
				continue
			}
			typed, err := st.AgentAnswered(caller.ID, ask.ToolUseID)
			if err != nil {
				return "", "", err
			}
			if typed {
				reason = "that answer in your own dialog was typed by the session that spawned you, not by your user"
				continue
			}
			return ask.ToolUseID, hash, nil
		}
	}
	return "", "", need(reason)
}

// adoptedGrantNote says what a grant or revoke did for a running session
// adopted from outside the board, which was not restarted: what its hooks
// apply from its next tool call, and plainly what needs a resume.
func adoptedGrantNote(g grant.Grant, revoked bool) string {
	if revoked {
		return "the session was started outside Gate Inbox, so it was not restarted: its hooks stop applying " +
			"the permission from its next tool call"
	}
	live, resume := grant.Live(g)
	var parts []string
	if live != "" {
		parts = append(parts, "the session was started outside Gate Inbox, so it was not restarted; from its "+
			"next tool call its hooks apply "+live)
	}
	if resume != "" {
		parts = append(parts, "NEEDS A RESUME: "+resume+" cannot reach a session Gate Inbox did not launch while it "+
			"runs, so that part applies only once the session is resumed on the board. Call grant_permission "+
			"again with the same kind and value and restart true to resume it now -- its conversation moves "+
			"out of the user's pane into a board session -- or leave it until it is taken over")
	}
	return strings.Join(parts, ". ")
}

// unsupportedGrant says why a CLI other than Claude Code cannot be granted a
// permission this way, and what that CLI has instead.
func unsupportedGrant(tool string) error {
	switch tool {
	case "codex":
		return errors.New("codex sessions cannot be granted a permission this way yet: Codex reads its " +
			"prefix rules from a rules directory every Codex session on the machine shares, and its sandbox from launch " +
			"flags Gate Inbox does not yet vary per grant; a person changes them")
	case "opencode":
		return errors.New("opencode sessions cannot be granted a permission this way yet; a person " +
			"changes the permission rules in opencode.json")
	}
	return fmt.Errorf("%s sessions cannot be granted a permission: only Claude Code sessions launch with a "+
		"settings file Gate Inbox writes", tool)
}

// FormatGrant is a grant's result as text.
func FormatGrant(r GrantResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n", r.Action, r.Target.ID, r.Grant.Granted)
	if r.Settings != "" {
		fmt.Fprintf(&b, "settings file: %s\n", r.Settings)
	}
	fmt.Fprintf(&b, "%s\n", r.Note)
	active := 0
	for _, g := range r.Grants {
		if g.RevokedAt.IsZero() {
			active++
			fmt.Fprintf(&b, "  in force: %s %q until %s\n", g.Kind, g.Value, g.ExpiresAt.Format("15:04"))
		}
	}
	if active == 0 {
		b.WriteString("  no permissions in force\n")
	}
	return b.String()
}
