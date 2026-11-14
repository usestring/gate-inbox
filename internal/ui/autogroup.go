package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/classifier"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

// Auto group files a session that sits loose at the root into a group by
// what it is working on. Once per poll pass, off the event loop, it asks the
// classifier one choice question about one such session: which existing
// group it belongs in, which configured preset it starts, which other loose
// session it shares a one-off project with, or none. The choice is the only
// thing the classifier returns, so names come from the board: an existing
// group keeps its own, a preset is the name in config, and a one-off project
// takes the name of the loose session it was paired with, which its agent
// named after the task. A person renames it with ctrl+alt+r like any group.
//
// A session is asked about once per name: its agent renaming it after the
// task is new information, a second poll pass is not. A session a person
// moved during this run is never filed again.

const autoGroupSetting = "experimental_auto_group"

const (
	autoGroupTimeout = 6 * time.Second
	// autoGroupSettle is how long a session that has not been named yet is
	// left before it is asked about anyway: the name is the best summary of
	// the task, and a fresh spawn's placeholder says nothing.
	autoGroupSettle      = 5 * time.Minute
	autoGroupMaxGroups   = 40
	autoGroupMaxPresets  = 10
	autoGroupMaxPeers    = 10
	autoGroupPromptChars = 1000
	autoGroupNameRunes   = 40
)

const autoGroupQuestion = "Choose where this coding-agent session belongs on a board of sessions grouped by project or topic. " +
	"Pick an existing group only when the session's task is clearly part of that group's project. Pick a new preset group when " +
	"the task fits its topic and no existing group does. Pick another session when both are clearly working on the same specific " +
	"one-off project that no group covers. Choose none when nothing fits clearly; a wrong group is worse than no group."

func storedAutoGroup(st *store.Store) bool { return storedExperiment(st, autoGroupSetting) }

type autoGroupState struct {
	busy bool
	// checked holds the name each session was last asked about under.
	checked map[string]string
	// placed holds sessions a person moved this run.
	placed map[string]bool
}

type autoGroupKind int

const (
	autoGroupExisting autoGroupKind = iota
	autoGroupPreset
	autoGroupPeer
)

// autoGroupOption is one answer the classifier may give.
type autoGroupOption struct {
	kind  autoGroupKind
	group string
	dir   string
	peer  store.Session
}

func (o autoGroupOption) describe() string {
	switch o.kind {
	case autoGroupExisting:
		if o.dir != "" {
			return fmt.Sprintf("Existing group %q (directory %s)", o.group, o.dir)
		}
		return fmt.Sprintf("Existing group %q", o.group)
	case autoGroupPreset:
		return fmt.Sprintf("New group %q", o.group)
	default:
		return fmt.Sprintf("A new one-off project group shared with the session named %q (directory %s)", o.peer.Name, o.peer.Cwd)
	}
}

type autoGroupResultMsg struct {
	id     string
	name   string
	option autoGroupOption
	ok     bool
}

// autoGroupLoose reports whether a row sits at the root on its own: no
// group, no parent, no extension role, and an agent rather than a shell.
func (m *Model) autoGroupLoose(sess store.Session) bool {
	return !sess.Archived && sess.Group == "" && sess.ParentID == "" && sess.Role == "" && !m.isShell(sess.Tool)
}

func autoGroupNamed(sess store.Session, now time.Time) bool {
	switch sess.NameSource {
	case store.SourceAgent, store.SourceUser, store.SourceTitle:
		return true
	}
	return now.Sub(sess.CreatedAt) >= autoGroupSettle
}

func (m *Model) autoGroupCandidate(now time.Time) (store.Session, bool) {
	for _, sess := range m.sessions {
		if !m.autoGroupLoose(sess) || m.autoGroup.placed[sess.ID] || m.autoGroup.checked[sess.ID] == sess.Name {
			continue
		}
		if autoGroupNamed(sess, now) {
			return sess, true
		}
	}
	return store.Session{}, false
}

// autoGroupOptions lists what the session may be filed under. The criteria
// map the classifier takes is capped, so each kind is capped too.
func (m *Model) autoGroupOptions(sess store.Session, now time.Time) []autoGroupOption {
	var options []autoGroupOption
	existing := map[string]bool{}
	for _, group := range m.groups {
		existing[group] = true
		// An archived group is one somebody filed away; filing new work
		// into it would hide that work in the archive.
		if group == rootGroup || m.groupEffectivelyArchived(group) || len(options) >= autoGroupMaxGroups {
			continue
		}
		options = append(options, autoGroupOption{kind: autoGroupExisting, group: group, dir: m.groupPaths[group]})
	}
	presets := 0
	for _, preset := range m.cfg.AutoGroup.Presets {
		name := autoGroupName(preset)
		if name == "" || existing[name] || presets >= autoGroupMaxPresets {
			continue
		}
		existing[name] = true
		presets++
		options = append(options, autoGroupOption{kind: autoGroupPreset, group: name})
	}
	peers := 0
	for _, peer := range m.sessions {
		if peer.ID == sess.ID || !m.autoGroupLoose(peer) || m.autoGroup.placed[peer.ID] || !autoGroupNamed(peer, now) || peers >= autoGroupMaxPeers {
			continue
		}
		peers++
		options = append(options, autoGroupOption{kind: autoGroupPeer, peer: peer})
	}
	return options
}

// autoGroupName turns a session name or preset into a group name: one path
// segment, since the slash nests groups, with no control characters.
func autoGroupName(raw string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r == '/':
			return '-'
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > autoGroupNameRunes {
		name = strings.TrimSpace(string(runes[:autoGroupNameRunes]))
	}
	return name
}

// checkAutoGroup asks about one loose session per poll pass.
func (m *Model) checkAutoGroup() tea.Cmd {
	key := m.jevAPIKey()
	if !m.autoGroupOn || key == "" || m.autoGroup.busy {
		return nil
	}
	now := time.Now()
	sess, ok := m.autoGroupCandidate(now)
	if !ok {
		return nil
	}
	if m.autoGroup.checked == nil {
		m.autoGroup.checked = map[string]string{}
	}
	m.autoGroup.checked[sess.ID] = sess.Name
	options := m.autoGroupOptions(sess, now)
	if len(options) == 0 {
		return nil
	}
	m.autoGroup.busy = true
	provider := m.classifierProvider()
	endpoint := provider.Endpoint()
	var locator *search.Locator
	if m.conversation != nil {
		locator = search.NewLocator(m.conversation.locator.ClaudeHome, m.conversation.locator.CodexRoot)
	}
	tool := historyToolFormats(m.cfg)[sess.Tool]
	database := opencodeDBPath()
	return func() tea.Msg {
		msg := autoGroupResultMsg{id: sess.ID, name: sess.Name}
		task := sess.LaunchPrompt
		if strings.TrimSpace(task) == "" && locator != nil {
			task = autoGroupFirstPrompt(locator, sess, tool, database)
		}
		state := map[string]any{
			"session_name": sess.Name,
			"directory":    sess.Cwd,
			"agent":        sess.Tool,
			"task":         boundedText(task, autoGroupPromptChars),
		}
		descriptions := make([]string, len(options))
		for i, option := range options {
			descriptions[i] = option.describe()
		}
		ctx, cancel := context.WithTimeout(context.Background(), autoGroupTimeout)
		defer cancel()
		choice := askAutoGroup(ctx, provider, jevHTTPClient, endpoint, key, state, descriptions)
		if choice >= 0 {
			msg.option, msg.ok = options[choice], true
		}
		return msg
	}
}

// autoGroupFirstPrompt is the first user turn of the session's transcript,
// for a pane with no launch prompt of its own, such as one the board adopted.
func autoGroupFirstPrompt(locator *search.Locator, sess store.Session, tool, database string) string {
	target, found := locator.Target(sess.ID, tool, sess.Cwd, sess.AgentSessionID)
	if !found {
		return ""
	}
	messages, err := search.ReadMessages(target, database)
	if err != nil {
		return ""
	}
	for _, message := range search.WorkMessages(messages) {
		if message.Role == "user" && strings.TrimSpace(message.Text) != "" {
			return message.Text
		}
	}
	return ""
}

// applyAutoGroup files the session, re-reading both rows first: a person
// may have moved either while the classifier was thinking.
func (m *Model) applyAutoGroup(msg autoGroupResultMsg) {
	m.autoGroup.busy = false
	if !msg.ok || !m.autoGroupOn || m.autoGroup.placed[msg.id] {
		return
	}
	sess, err := m.store.Get(msg.id)
	if err != nil || !m.autoGroupLoose(sess) || sess.Name != msg.name {
		return
	}
	option := msg.option
	group, dir := option.group, ""
	members := []string{sess.ID}
	switch option.kind {
	case autoGroupPreset:
		dir = sess.Cwd
	case autoGroupPeer:
		peer, err := m.store.Get(option.peer.ID)
		if err != nil || !m.autoGroupLoose(peer) || m.autoGroup.placed[peer.ID] {
			return
		}
		group = autoGroupName(peer.Name)
		if group == "" {
			return
		}
		if sess.Cwd == peer.Cwd {
			dir = sess.Cwd
		}
		members = append(members, peer.ID)
	}
	if m.groupEffectivelyArchived(group) {
		return
	}
	if option.kind != autoGroupExisting {
		if err := m.store.AddGroup(group, dir); err != nil && !errors.Is(err, store.ErrGroupExists) {
			m.errBar.text = err.Error()
			return
		}
		m.materializeGroupsLocal([]string{group})
		if m.groupPaths == nil {
			m.groupPaths = map[string]string{}
		}
		if _, known := m.groupPaths[group]; !known {
			m.groupPaths[group] = dir
		}
	}
	for _, id := range members {
		if err := m.store.MoveSession(id, group); err != nil {
			m.errBar.text = err.Error()
			return
		}
		m.relabelSession(id)
	}
	m.requestRefresh()
}

// noteAutoGroupPlaced records a person moving a session, so the pass never
// files it somewhere they did not put it.
func (m *Model) noteAutoGroupPlaced(id string) {
	if m.autoGroup.placed == nil {
		m.autoGroup.placed = map[string]bool{}
	}
	m.autoGroup.placed[id] = true
}

// askAutoGroup returns the index of the chosen option, or -1 for none and
// for any failed request.
func askAutoGroup(ctx context.Context, provider classifier.Provider, client *http.Client, endpoint, key string, state any, options []string) int {
	criteria := map[string]string{"none": "None of these fits clearly; leave the session where it is."}
	for i, option := range options {
		criteria[fmt.Sprintf("g%d", i+1)] = option
	}
	choice := provider.AskChoice(ctx, client, endpoint, key, state, autoGroupQuestion, criteria)
	for i := range options {
		if choice == fmt.Sprintf("g%d", i+1) {
			return i
		}
	}
	return -1
}
