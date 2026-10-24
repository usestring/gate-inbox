// Package gatechannel lets one registered consumer outside the board, such as
// a voice front end, answer a Claude Code AskUserQuestion call before its
// dialog is drawn.
//
// The consumer holds a lease. While that lease is live and says the consumer
// can deliver right now, the ask-pending hook holds the call: it leaves a hold
// for the consumer to read and waits for an answer. The hold ends the moment
// the lease lapses, is released or turns undeliverable, so a paused or muted
// consumer never delays a dialog. With no lease at all, the hook does nothing
// it did not already do.
//
// Everything lives in files under the config dir, 0700 with 0600 files, so
// nothing here touches state.db. A sandboxed agent can write neither the lease
// nor an answer, since the config dir is outside its writable paths. The
// commands also refuse to run as a managed session. An unsandboxed process
// under the same user can still act as the consumer; docs/parent-channel.md
// states the same limit for the sealing keys.
package gatechannel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
)

// Version is the protocol a hold and an answer speak. A consumer that reads
// another version leaves the hold alone.
const Version = 1

// DirName is the directory under the config dir that holds the channel.
const DirName = "gate-channel"

// MaxHold bounds how long one call is held. Claude Code gives a command hook
// 60 seconds; the hold ends well inside that so the hook can still answer.
const MaxHold = 50 * time.Second

// MaxLeaseTTL bounds how far ahead one renewal can push a lease, so a
// consumer that dies stops holding calls within that long.
const MaxLeaseTTL = 30 * time.Second

// poll is how often a hold rereads the lease and looks for an answer.
const poll = 100 * time.Millisecond

// receiptWait is how long Answer waits for the hook to take the answer before
// it reports the answer as undelivered.
const receiptWait = 3 * time.Second

var (
	validName    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	validSession = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	validToolUse = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// Lease is the one consumer allowed to answer held calls.
type Lease struct {
	Consumer    string    `json:"consumer"`
	Token       string    `json:"token"`
	Deliverable bool      `json:"deliverable"`
	HoldSeconds int       `json:"hold_seconds"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// live is whether the lease still lets a call be held at now.
func (l Lease) live(now time.Time) bool {
	return l.Token != "" && l.Deliverable && now.Before(l.ExpiresAt)
}

// Hold is one call waiting for the consumer.
type Hold struct {
	Version   int                 `json:"version"`
	Session   string              `json:"session"`
	ToolUseID string              `json:"tool_use_id"`
	Revision  string              `json:"revision"`
	Questions []convo.AskQuestion `json:"questions"`
	Deadline  time.Time           `json:"deadline"`
}

// Answer is what the consumer hands a hold: an option label per question, or
// a decline of the whole call.
type Answer struct {
	Version   int               `json:"version"`
	Token     string            `json:"token"`
	Session   string            `json:"session"`
	ToolUseID string            `json:"tool_use_id"`
	Revision  string            `json:"revision"`
	Answers   map[string]string `json:"answers,omitempty"`
	Decline   bool              `json:"decline,omitempty"`
}

// Dir is the channel directory for configDir.
func Dir(configDir string) string { return filepath.Join(configDir, DirName) }

func leasePath(configDir string) string { return filepath.Join(Dir(configDir), "lease.json") }

func holdDir(configDir string) string { return filepath.Join(Dir(configDir), "holds") }

// holdBase is the path every file of one hold starts with.
func holdBase(configDir, session, toolUseID string) (string, error) {
	if !validSession.MatchString(session) {
		return "", fmt.Errorf("not a session id: %q", session)
	}
	if !validToolUse.MatchString(toolUseID) {
		return "", fmt.Errorf("not a tool call id: %q", toolUseID)
	}
	return filepath.Join(holdDir(configDir), session+"."+toolUseID), nil
}

// Revision identifies the exact questions of a call: every question, option,
// description, preview and multiSelect flag, in order.
func Revision(questions []convo.AskQuestion) string {
	raw, _ := json.Marshal(questions)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ReadLease is the current lease, or false when there is none.
func ReadLease(configDir string) (Lease, bool) {
	var lease Lease
	raw, err := os.ReadFile(leasePath(configDir))
	if err != nil || json.Unmarshal(raw, &lease) != nil {
		return Lease{}, false
	}
	return lease, true
}

// Renew takes or extends the lease for consumer. The first call mints the
// token and returns it; every later call must present it. Another consumer's
// lease is refused until it expires or is released.
func Renew(configDir, consumer, token string, deliverable bool, hold, ttl time.Duration, now time.Time) (Lease, error) {
	if !validName.MatchString(consumer) {
		return Lease{}, fmt.Errorf("consumer name %q must be lowercase letters, digits and dashes", consumer)
	}
	if ttl <= 0 || ttl > MaxLeaseTTL {
		return Lease{}, fmt.Errorf("lease ttl %s must be above 0 and at most %s", ttl, MaxLeaseTTL)
	}
	if hold <= 0 || hold > MaxHold {
		return Lease{}, fmt.Errorf("hold %s must be above 0 and at most %s", hold, MaxHold)
	}
	current, held := ReadLease(configDir)
	if held && now.Before(current.ExpiresAt) {
		if current.Consumer != consumer {
			return Lease{}, fmt.Errorf("%s holds the gate channel until %s", current.Consumer, current.ExpiresAt.Format(time.RFC3339))
		}
		if token != current.Token {
			return Lease{}, errors.New("the lease token does not match")
		}
	} else if token != "" && (!held || token != current.Token) {
		return Lease{}, errors.New("the lease has lapsed; take a new one without a token")
	}
	if token == "" {
		fresh := make([]byte, 24)
		if _, err := rand.Read(fresh); err != nil {
			return Lease{}, err
		}
		token = hex.EncodeToString(fresh)
	}
	lease := Lease{
		Consumer:    consumer,
		Token:       token,
		Deliverable: deliverable,
		HoldSeconds: int(hold / time.Second),
		ExpiresAt:   now.Add(ttl),
	}
	if lease.HoldSeconds < 1 {
		lease.HoldSeconds = 1
	}
	return lease, writeJSON(leasePath(configDir), lease)
}

// Release drops the lease the token names, which ends every hold at once.
func Release(configDir, token string) error {
	current, held := ReadLease(configDir)
	if !held {
		return nil
	}
	if token != current.Token {
		return errors.New("the lease token does not match")
	}
	err := os.Remove(leasePath(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Pending is every hold still waiting at now, for the lease the token names.
func Pending(configDir, token string, now time.Time) ([]Hold, error) {
	if err := checkToken(configDir, token, now); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(holdDir(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	holds := []Hold{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".hold") {
			continue
		}
		var hold Hold
		raw, err := os.ReadFile(filepath.Join(holdDir(configDir), entry.Name()))
		if err != nil || json.Unmarshal(raw, &hold) != nil || hold.Version != Version || !now.Before(hold.Deadline) {
			continue
		}
		holds = append(holds, hold)
	}
	slices.SortFunc(holds, func(a, b Hold) int { return a.Deadline.Compare(b.Deadline) })
	return holds, nil
}

func checkToken(configDir, token string, now time.Time) error {
	lease, held := ReadLease(configDir)
	if !held || !now.Before(lease.ExpiresAt) {
		return errors.New("no consumer holds the gate channel")
	}
	if token == "" || token != lease.Token {
		return errors.New("the lease token does not match")
	}
	return nil
}

// Deliver hands answer to the hold it names and waits for the hook to take
// it. It claims the hold first, so a hold that already ended, whose dialog is
// on screen, is refused rather than answered twice. A claimed hold whose hook
// never takes the answer is reported as undelivered: the dialog stays up for
// the board or the keyboard to answer.
func Deliver(configDir string, answer Answer, clock Clock) error {
	now := clock.Now
	if answer.Version != Version {
		return fmt.Errorf("answer speaks protocol %d, want %d", answer.Version, Version)
	}
	if err := checkToken(configDir, answer.Token, now()); err != nil {
		return err
	}
	base, err := holdBase(configDir, answer.Session, answer.ToolUseID)
	if err != nil {
		return err
	}
	var hold Hold
	raw, err := os.ReadFile(base + ".hold")
	if err != nil || json.Unmarshal(raw, &hold) != nil {
		return errors.New("that call is no longer held")
	}
	if !now().Before(hold.Deadline) {
		return errors.New("that call's hold has ended")
	}
	if err := validate(hold, answer); err != nil {
		return err
	}
	if err := os.Rename(base+".hold", base+".claimed"); err != nil {
		return errors.New("that call is no longer held")
	}
	if err := writeJSON(base+".answer", answer); err != nil {
		return err
	}
	deadline := now().Add(receiptWait)
	for now().Before(deadline) {
		if _, err := os.Stat(base + ".taken"); err == nil {
			_ = os.Remove(base + ".taken")
			return nil
		}
		clock.Sleep(poll)
	}
	_ = os.Remove(base + ".answer")
	return errors.New("the hook did not take the answer; the dialog stays on screen")
}

// validate checks answer against the hold it names. Every question needs an
// answer drawn from its own options: one label, or for a multiSelect question
// labels joined by ", ". Free text is not accepted yet.
func validate(hold Hold, answer Answer) error {
	if answer.Session != hold.Session || answer.ToolUseID != hold.ToolUseID {
		return errors.New("the answer names another call")
	}
	if answer.Revision != hold.Revision || Revision(hold.Questions) != hold.Revision {
		return errors.New("the call's questions changed since they were read")
	}
	if answer.Decline {
		if len(answer.Answers) != 0 {
			return errors.New("a decline carries no answers")
		}
		return nil
	}
	if len(answer.Answers) != len(hold.Questions) {
		return fmt.Errorf("the call asks %d questions and %d were answered", len(hold.Questions), len(answer.Answers))
	}
	for _, question := range hold.Questions {
		value, ok := answer.Answers[question.Question]
		if !ok {
			return fmt.Errorf("no answer to %q", question.Question)
		}
		labels := []string{value}
		if question.MultiSelect {
			labels = strings.Split(value, ", ")
		}
		seen := map[string]bool{}
		for _, label := range labels {
			if seen[label] || !slices.ContainsFunc(question.Options, func(o convo.AskOption) bool { return o.Label == label }) {
				return fmt.Errorf("%q is not one of the options of %q", value, question.Question)
			}
			seen[label] = true
		}
	}
	return nil
}

// Clock is what a hold reads the time with and sleeps between polls with, so
// a test can drive it.
type Clock struct {
	Now   func() time.Time
	Sleep func(time.Duration)
}

// RealClock is the wall clock.
var RealClock = Clock{Now: time.Now, Sleep: time.Sleep}

// HoldCall holds one AskUserQuestion call while a consumer can deliver it,
// and returns the PreToolUse hook output that answers it, or "" when the
// dialog should be drawn as usual. It returns at once when no live,
// deliverable lease stands, and as soon as the lease lapses, is released or
// turns undeliverable mid-hold.
func HoldCall(configDir, session, toolUseID string, questions []convo.AskQuestion, clock Clock) string {
	if len(questions) == 0 {
		return ""
	}
	lease, held := ReadLease(configDir)
	if !held || !lease.live(clock.Now()) {
		return ""
	}
	base, err := holdBase(configDir, session, toolUseID)
	if err != nil {
		return ""
	}
	limit := time.Duration(lease.HoldSeconds) * time.Second
	if limit <= 0 || limit > MaxHold {
		limit = MaxHold
	}
	hold := Hold{
		Version:   Version,
		Session:   session,
		ToolUseID: toolUseID,
		Revision:  Revision(questions),
		Questions: questions,
		Deadline:  clock.Now().Add(limit),
	}
	if writeJSON(base+".hold", hold) != nil {
		return ""
	}
	defer cleanup(base)
	// A consumer answers one call at a time, so a call that arrives while it
	// holds another would only wait. It writes its own hold before looking,
	// so two calls racing here both draw rather than both wait.
	if anotherHeld(configDir, base, clock.Now()) {
		return ""
	}
	for {
		if output, ok := takeAnswer(base, hold, lease); ok {
			return output
		}
		current, still := ReadLease(configDir)
		if !still || current.Token != lease.Token || !current.live(clock.Now()) || !clock.Now().Before(hold.Deadline) {
			if os.Rename(base+".hold", base+".ended") == nil {
				return ""
			}
			// The consumer claimed the hold first; its answer is on the way.
			return awaitClaimed(base, hold, lease, clock)
		}
		clock.Sleep(poll)
	}
}

// awaitClaimed waits briefly for the answer a consumer claimed the hold for.
// anotherHeld reports whether a call other than base's is held or claimed
// and not past its deadline. A hook that was killed leaves its file behind;
// the deadline is what stops it counting.
func anotherHeld(configDir, base string, now time.Time) bool {
	entries, err := os.ReadDir(holdDir(configDir))
	if err != nil {
		return false
	}
	own := filepath.Base(base)
	for _, entry := range entries {
		name := entry.Name()
		stem := strings.TrimSuffix(strings.TrimSuffix(name, ".hold"), ".claimed")
		if stem == name || stem == own {
			continue
		}
		var hold Hold
		raw, err := os.ReadFile(filepath.Join(holdDir(configDir), name))
		if err == nil && json.Unmarshal(raw, &hold) == nil && now.Before(hold.Deadline) {
			return true
		}
	}
	return false
}

func awaitClaimed(base string, hold Hold, lease Lease, clock Clock) string {
	deadline := clock.Now().Add(receiptWait)
	for clock.Now().Before(deadline) {
		if output, ok := takeAnswer(base, hold, lease); ok {
			return output
		}
		clock.Sleep(poll)
	}
	return ""
}

// takeAnswer reads the answer to a claimed hold, checks it again, leaves the
// receipt Deliver waits for, and builds the hook output.
func takeAnswer(base string, hold Hold, lease Lease) (string, bool) {
	raw, err := os.ReadFile(base + ".answer")
	if err != nil {
		return "", false
	}
	_ = os.Remove(base + ".answer")
	var answer Answer
	if json.Unmarshal(raw, &answer) != nil || answer.Token != lease.Token || validate(hold, answer) != nil {
		return "", false
	}
	output, err := hookOutput(hold, answer, lease.Consumer)
	if err != nil {
		return "", false
	}
	if os.WriteFile(base+".taken", nil, 0o600) != nil {
		return "", false
	}
	return output, true
}

func hookOutput(hold Hold, answer Answer, consumer string) (string, error) {
	decision := map[string]any{"hookEventName": "PreToolUse"}
	if answer.Decline {
		decision["permissionDecision"] = "deny"
		decision["permissionDecisionReason"] = "The user declined this question through " + consumer + "."
	} else {
		decision["permissionDecision"] = "allow"
		decision["permissionDecisionReason"] = "The user answered this question through " + consumer + "."
		decision["updatedInput"] = map[string]any{"questions": hold.Questions, "answers": answer.Answers}
	}
	raw, err := json.Marshal(map[string]any{"hookSpecificOutput": decision})
	return string(raw), err
}

func cleanup(base string) {
	for _, suffix := range []string{".hold", ".claimed", ".ended", ".answer"} {
		_ = os.Remove(base + suffix)
	}
}

// writeJSON writes value to path atomically, 0600 in a 0700 directory.
func writeJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
