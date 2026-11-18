package ui

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/workspec"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

// The next-step card is one suggested instruction for a session at rest,
// worked out from what the board already holds about the pull request the
// session opened: its checks, its review threads and whether it merged. No
// model is asked and nothing is fetched for it. It is drawn on the hotkey
// menu's suggestion line and ctrl+y inserts it into the prompt unsent, the
// same surface and key as Auto Suggest, which it comes ahead of.

const nextStepSetting = "experimental_next_step"

func storedNextStep(st *store.Store) bool { return storedExperiment(st, nextStepSetting) }

const (
	// nextStepLocalTTL is how long one answer about a working directory is
	// reused. The card is read by a person who has just landed on the row, so
	// half a minute stale is fine and it bounds the forks to two per
	// directory per window however many frames are drawn.
	nextStepLocalTTL     = 30 * time.Second
	nextStepLocalTimeout = 3 * time.Second
	nextStepNamedChecks  = 3
)

type nextStepKind int

const (
	// nextStepPrompt is text for the agent: ctrl+y inserts it.
	nextStepPrompt nextStepKind = iota
	// nextStepArchive is a hint at a board action. Archiving ends the pane,
	// so the card names the key rather than pressing it for the operator.
	nextStepArchive
)

type nextStep struct {
	kind nextStepKind
	text string
}

// localWork is what the session's checkout holds that no pull request
// carries yet.
type localWork struct {
	known  bool
	branch string
	// ahead is the commits on HEAD the remote's default branch lacks.
	ahead int
	dirty bool
	at    time.Time
}

type nextStepState struct {
	local    map[string]localWork
	inFlight map[string]bool
	// accepted is the card last inserted, so the same line is not offered
	// again over a prompt that already holds it.
	accepted string
}

type nextStepLocalMsg struct {
	dir   string
	facts localWork
}

// nextStepFacts is everything the rule table reads, so the table is a pure
// function of state the board already holds.
type nextStepFacts struct {
	status string
	// prs is the pull requests the session owns that GitHub has answered for
	// in this run, strongest evidence first.
	prs []forge.PR
	// unsettled says some pull request the session refers to has no answer
	// yet, so "this session has no pull request" cannot be claimed.
	unsettled bool
	// trackedPRs is false when the board is not following pull requests at
	// all, in which case their absence means nothing.
	trackedPRs bool
	local      localWork
}

// nextStepFor is the rule table, first match wins:
//
//  1. an owned open pull request has failing checks
//  2. an owned open pull request has unresolved review threads, or changes requested
//  3. an owned pull request is open with nothing for the agent to do: no card
//  4. no pull request, and the checkout has commits or changes: open one
//  5. every owned pull request is over and one merged: archive (a board action)
//
// A session that is not at rest gets no card whatever else is true. A green,
// approved pull request gets none either: merging is the operator's call, and
// a one-key "merge it" prompt would hand that call to the agent.
func nextStepFor(f nextStepFacts) (nextStep, bool) {
	if !atRest(f.status) {
		return nextStep{}, false
	}
	var open, merged []forge.PR
	for _, pr := range f.prs {
		switch pr.State {
		case forge.PROpen, forge.PRDraft:
			open = append(open, pr)
		case forge.PRMerged:
			merged = append(merged, pr)
		}
	}
	for _, pr := range open {
		if pr.Checks == forge.ChecksFailing {
			return nextStep{text: "Fix the failing checks on " + prName(pr) + failingCheckNames(pr)}, true
		}
	}
	for _, pr := range open {
		if n, counted := openReviewThreads(pr); counted && n > 0 {
			noun := "comments"
			if n == 1 {
				noun = "comment"
			}
			return nextStep{text: "Address the " + strconv.Itoa(n) + " open review " + noun + " on " + prName(pr)}, true
		}
		if pr.Review == forge.ReviewChangesRequested {
			return nextStep{text: "Address the requested changes on " + prName(pr)}, true
		}
	}
	if len(open) > 0 {
		return nextStep{}, false
	}
	if len(f.prs) == 0 {
		if !f.trackedPRs || f.unsettled || !f.local.known {
			return nextStep{}, false
		}
		switch {
		case f.local.dirty:
			return nextStep{text: "Commit this work and open a pull request for it"}, true
		case f.local.ahead > 0:
			return nextStep{text: "Open a pull request for this branch"}, true
		}
		return nextStep{}, false
	}
	if len(merged) > 0 && !f.unsettled {
		return nextStep{kind: nextStepArchive, text: prName(merged[0]) + " merged"}, true
	}
	return nextStep{}, false
}

func prName(pr forge.PR) string { return pr.Repo + "#" + strconv.Itoa(pr.Number) }

// safeCheckName is the shape of a check name the card will repeat. The names
// come from the repository's CI configuration and end up in an agent's
// prompt, so anything that reads like prose rather than a job name is left
// out and only counted.
var safeCheckName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._/()+-]{0,39}$`)

// failingCheckNames names the red checks, ": build, lint and 2 more". A pull
// request restored from the last run carries only the count, and says that.
func failingCheckNames(pr forge.PR) string {
	var names []string
	seen := map[string]bool{}
	failing := 0
	if pr.CI != nil {
		for _, check := range pr.CI.Checks {
			switch strings.ToUpper(check.Conclusion) {
			case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ERROR":
			default:
				continue
			}
			failing++
			if seen[check.Name] || !safeCheckName.MatchString(check.Name) {
				continue
			}
			seen[check.Name] = true
			if len(names) < nextStepNamedChecks {
				names = append(names, check.Name)
			}
		}
	}
	if len(names) == 0 {
		if pr.FailingChecks > 0 {
			return " (" + strconv.Itoa(pr.FailingChecks) + " failing)"
		}
		return ""
	}
	text := ": " + strings.Join(names, ", ")
	if rest := failing - len(names); rest > 0 {
		text += " and " + strconv.Itoa(rest) + " more"
	}
	return text
}

// openReviewThreads counts a person's review threads still open on the code
// as it stands. counted is false when the board does not hold the threads:
// they are fetched only with [work] pr_feedback on, and a page that did not
// come back whole is not a count.
func openReviewThreads(pr forge.PR) (n int, counted bool) {
	if pr.Feedback == nil || !pr.Feedback.ThreadsComplete {
		return 0, false
	}
	for _, thread := range pr.Feedback.Threads {
		if !thread.Resolved && !thread.Outdated && thread.AuthorType == "User" {
			n++
		}
	}
	return n, true
}

// nextStepTarget is the session a card would be for: the hotkey menu's
// target, when the feature is on and the menu is the snippet menu.
func (m *Model) nextStepTarget() (store.Session, bool) {
	if !m.nextStepOn || !m.quick.active || m.quick.launch || m.work == nil {
		return store.Session{}, false
	}
	entry, ok := m.selectedRow()
	if !ok || entry.isGroup || entry.sess.Archived || m.isShell(entry.sess.Tool) {
		return store.Session{}, false
	}
	return entry.sess, true
}

func atRest(shown string) bool { return shown == status.Finished || shown == status.Idle }

// ownedPRs sorts what the tracker holds for a session into the pull requests
// it owns. Owning one means the session was seen opening it, or it merges
// from the branch the session's checkout is on; a pull request the session
// only mentioned is somebody else's.
func ownedPRs(work worktracker.Work, branch string) (owned []forge.PR, unsettled bool) {
	for _, ref := range workspec.ByEvidence(work.Refs) {
		if ref.Kind != workspec.KindPR {
			continue
		}
		key := ref.Key()
		pr, resolved := work.PRs[key]
		if !work.Looked[key] {
			// Not asked yet, or only remembered from the last run: neither is
			// state to hand an agent an instruction on.
			unsettled = true
			continue
		}
		if !resolved {
			continue
		}
		if ref.Provenance == workspec.FromCreation || (branch != "" && pr.HeadRef == branch) {
			owned = append(owned, pr)
		}
	}
	return owned, unsettled
}

// nextStepCard is the card for the menu's target, from memory alone: the
// tracker's cache, the row's status and the last answer about its checkout.
// It is called while drawing, so it must not fork or query.
func (m *Model) nextStepCard() (nextStep, bool) {
	sess, ok := m.nextStepTarget()
	if !ok {
		return nextStep{}, false
	}
	shown := m.shownStatus(sess)
	if !atRest(shown) {
		return nextStep{}, false
	}
	local := m.nextStep.local[sess.Cwd]
	prs, unsettled := ownedPRs(m.work.For(sess.ID), local.branch)
	github, _ := m.work.Health()
	step, ok := nextStepFor(nextStepFacts{status: shown, prs: prs, unsettled: unsettled,
		trackedPRs: !github.Off, local: local})
	if !ok || m.nextStep.accepted == nextStepIdentity(sess, step) {
		return nextStep{}, false
	}
	return step, true
}

// nextStepIdentity ties a card to the turn it was offered on: the session's
// next change of status makes the same text a new card.
func nextStepIdentity(sess store.Session, step nextStep) string {
	return sess.ID + "\x00" + strconv.FormatInt(sess.LastStatusAt.UnixNano(), 10) + "\x00" + step.text
}

// nextStepLine is what the suggestion line shows for a card.
func (m *Model) nextStepLine(step nextStep) string {
	if step.kind == nextStepArchive {
		ctx := keymap.ContextList
		if m.quick.fromFocus {
			ctx = keymap.ContextFocus
		}
		line := "next · " + step.text
		if key := m.hintKey(ctx, keymap.Archive); key != "" {
			line += " — esc, then " + key + " archives this session"
		}
		return line
	}
	return "ctrl+y insert · " + step.text
}

// acceptNextStep handles ctrl+y on a card: the text goes into the target's
// prompt unsubmitted, for the operator to edit and send. handled is false
// when there is no prompt card, so the key falls through to Auto Suggest.
func (m *Model) acceptNextStep(send func(string) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd, bool) {
	step, ok := m.nextStepCard()
	if !ok || step.kind != nextStepPrompt {
		return m, nil, false
	}
	sess, _ := m.nextStepTarget()
	model, cmd := send(step.text)
	m.nextStep.accepted = nextStepIdentity(sess, step)
	return model, cmd, true
}

// scheduleNextStepLocal asks, off the event loop, what the target's checkout
// holds, when the card could need it and the last answer has aged out. A
// session the board saw open a pull request is not asked about: its rules
// read the tracker alone. One whose pull request is known only by its branch
// keeps being asked, since the branch is part of the answer.
func (m *Model) scheduleNextStepLocal() tea.Cmd {
	sess, ok := m.nextStepTarget()
	if !ok || sess.Cwd == "" || !atRest(m.shownStatus(sess)) {
		return nil
	}
	dir := sess.Cwd
	if m.nextStep.inFlight[dir] {
		return nil
	}
	if facts, held := m.nextStep.local[dir]; held && time.Since(facts.at) < nextStepLocalTTL {
		return nil
	}
	if prs, _ := ownedPRs(m.work.For(sess.ID), ""); len(prs) > 0 {
		return nil
	}
	if m.nextStep.inFlight == nil {
		m.nextStep.inFlight = map[string]bool{}
	}
	m.nextStep.inFlight[dir] = true
	return func() tea.Msg { return nextStepLocalMsg{dir: dir, facts: readLocalWork(dir)} }
}

func (m *Model) applyNextStepLocal(msg nextStepLocalMsg) {
	delete(m.nextStep.inFlight, msg.dir)
	if m.nextStep.local == nil {
		m.nextStep.local = map[string]localWork{}
	}
	m.nextStep.local[msg.dir] = msg.facts
}

// readLocalWork forks git, so it runs only inside a command. A directory
// that is not a repository, or a git that does not answer in time, comes
// back unknown and is cached as that: no card, and no fork until the answer
// ages out.
var readLocalWork = func(dir string) localWork {
	facts := localWork{at: time.Now()}
	out, ok := nextStepGit(dir, "status", "--porcelain=v2", "--branch")
	if !ok {
		return facts
	}
	facts.known = true
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			if head := strings.TrimPrefix(line, "# branch.head "); head != "(detached)" {
				facts.branch = head
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			facts.dirty = true
		}
	}
	for _, base := range []string{"origin/HEAD", "origin/main", "origin/master"} {
		if count, ok := nextStepGit(dir, "rev-list", "--count", base+"..HEAD"); ok {
			facts.ahead, _ = strconv.Atoi(strings.TrimSpace(count))
			break
		}
	}
	return facts
}

func nextStepGit(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), nextStepLocalTimeout)
	defer cancel()
	// status refreshes the index when it can, and the lock it takes for that
	// would fail a git command the agent runs at the same moment.
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err == nil
}
