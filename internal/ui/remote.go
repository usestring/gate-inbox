package ui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/remotehost"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

const (
	remoteRefreshInterval = 5 * time.Second
	remoteSnapshotTTL     = 3 * remoteRefreshInterval
)

type remoteBrowser struct {
	hosts      map[string]remoteHostState
	selected   remoteSessionKey
	generation uint64
}

type remoteSessionKey struct {
	host, id string
}

type remoteHostState struct {
	snapshot sessioncmd.HostSnapshot
	lastSeen time.Time
	pending  bool
	checked  bool
	err      error
}

type remotePollTick uint64

type remotePollResult struct {
	host     string
	snapshot sessioncmd.HostSnapshot
	at       time.Time
	err      error
}

type remoteAttachDone struct {
	host string
	err  error
}

func remoteTick(generation uint64) tea.Cmd {
	return tea.Tick(remoteRefreshInterval, func(time.Time) tea.Msg {
		return remotePollTick(generation)
	})
}

func (m *Model) openRemoteSessions() tea.Cmd {
	m.mode = modeRemoteSessions
	m.errBar.text = ""
	m.remote.generation++
	if m.remote.hosts == nil {
		m.remote.hosts = make(map[string]remoteHostState, len(m.cfg.Hosts))
	}
	// A cached row is still useful on return, but the operator must see
	// a fresh snapshot before its terminal can be opened again.
	for name, state := range m.remote.hosts {
		state.checked = false
		m.remote.hosts[name] = state
	}
	if len(m.cfg.Hosts) == 0 {
		return nil
	}
	return tea.Batch(m.pollRemoteHosts(), remoteTick(m.remote.generation))
}

func (m *Model) pollRemoteHosts() tea.Cmd {
	if m.mode != modeRemoteSessions {
		return nil
	}
	var cmds []tea.Cmd
	for name, host := range m.cfg.Hosts {
		state := m.remote.hosts[name]
		if state.pending {
			continue
		}
		state.pending = true
		m.remote.hosts[name] = state
		cmds = append(cmds, func() tea.Msg {
			snapshot, err := remotehost.Sessions(host)
			return remotePollResult{host: name, snapshot: snapshot, at: time.Now(), err: err}
		})
	}
	return tea.Batch(cmds...)
}

func (m *Model) applyRemoteSnapshot(msg remotePollResult) {
	state := m.remote.hosts[msg.host]
	state.pending = false
	state.checked = true
	state.err = msg.err
	if msg.err == nil {
		state.lastSeen = msg.at
		state.snapshot = msg.snapshot
	}
	m.remote.hosts[msg.host] = state
	if m.remote.selected == (remoteSessionKey{}) {
		m.stepRemoteSelection(1)
	}
}

type remoteRow struct {
	key   remoteSessionKey
	label string
}

func (m *Model) remoteRows() []remoteRow {
	var rows []remoteRow
	for _, name := range slices.Sorted(maps.Keys(m.cfg.Hosts)) {
		state := m.remote.hosts[name]
		rows = append(rows, remoteRow{label: name + " · " + state.summary()})
		hostLabel := name
		if !state.fresh() {
			hostLabel += " (stale)"
		}
		for _, session := range state.snapshot.Sessions {
			title := session.Name
			if title == "" {
				title = session.ID
			}
			status := session.Status
			if !session.Running {
				status += " (no live pane)"
			}
			rows = append(rows, remoteRow{
				key:   remoteSessionKey{host: name, id: session.ID},
				label: hostLabel + " · " + status + " · " + title,
			})
		}
	}
	return rows
}

func (s remoteHostState) summary() string {
	if !s.checked {
		return "checking SSH"
	}
	if s.err != nil {
		if s.lastSeen.IsZero() {
			return "offline · " + s.err.Error()
		}
		return "offline · last seen " + textfmt.Age(time.Since(s.lastSeen)) + " ago · " + s.err.Error()
	}
	if !s.fresh() {
		return "stale · last seen " + textfmt.Age(time.Since(s.lastSeen)) + " ago"
	}
	if listed := len(s.snapshot.Sessions); listed < s.snapshot.Matched {
		return fmt.Sprintf("connected · showing %d of %d sessions", listed, s.snapshot.Matched)
	}
	if len(s.snapshot.Sessions) == 0 {
		return "connected · no sessions"
	}
	return "connected"
}

func (s remoteHostState) fresh() bool {
	return s.checked && s.err == nil && time.Since(s.lastSeen) <= remoteSnapshotTTL
}

func (m *Model) stepRemoteSelection(delta int) {
	var choices []remoteSessionKey
	for _, row := range m.remoteRows() {
		if row.key.id != "" {
			choices = append(choices, row.key)
		}
	}
	if len(choices) == 0 {
		return
	}
	i := slices.Index(choices, m.remote.selected)
	m.remote.selected = choices[min(max(i+delta, 0), len(choices)-1)]
}

func (m *Model) handleRemoteSessionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.errBar.text = ""
	case "ctrl+c":
		return m, tea.Quit
	case "r":
		m.errBar.text = ""
		return m, m.pollRemoteHosts()
	case "up", "k":
		m.stepRemoteSelection(-1)
		m.errBar.text = ""
	case "down", "j":
		m.stepRemoteSelection(1)
		m.errBar.text = ""
	case "enter":
		return m, m.attachRemoteSession()
	}
	return m, nil
}

func (m *Model) attachRemoteSession() tea.Cmd {
	key := m.remote.selected
	if key.id == "" {
		return nil
	}
	host := m.remote.hosts[key.host]
	if !host.fresh() {
		m.errBar.text = key.host + " has no current session list"
		return nil
	}
	for _, session := range host.snapshot.Sessions {
		if session.ID != key.id {
			continue
		}
		if !session.Running {
			m.errBar.text = "this session has no live pane"
			return nil
		}
		m.errBar.text = ""
		return tea.ExecProcess(remotehost.Attach(m.cfg.Hosts[key.host], key.id), func(err error) tea.Msg {
			return remoteAttachDone{host: key.host, err: err}
		})
	}
	m.errBar.text = "this session is no longer listed"
	return nil
}

func (m *Model) finishRemoteAttach(msg remoteAttachDone) tea.Cmd {
	SyncTerminalBackground()
	if msg.err != nil {
		m.errBar.text = fmt.Sprintf("could not attach to %s: %v", msg.host, msg.err)
	}
	return m.pollRemoteHosts()
}

func (m *Model) viewRemoteSessions() string {
	width := max(24, min(96, m.width-4))
	inner := cardInnerWidth(width)
	hints := [][2]string{{"↑↓", "pick"}, {"↵", "attach"}, {"r", "refresh"}, {"esc", "back"}}
	rows := m.remoteRows()
	if len(rows) == 0 {
		return m.cardSized(width, "Remote sessions",
			textfmt.TruncateWidth("Add a [hosts.<name>] section in config.toml.", inner, "…"), hints)
	}

	visible := max(1, min(15, m.height-10))
	start := 0
	i := slices.IndexFunc(rows, func(row remoteRow) bool {
		return row.key.id != "" && row.key == m.remote.selected
	})
	if i >= visible {
		start = i - visible + 1
	}
	var lines []string
	for _, row := range rows[start:min(len(rows), start+visible)] {
		label := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, row.label)), " ")
		label = textfmt.TruncateWidth(label, max(1, inner-2), "…")
		switch {
		case row.key.id == "":
			lines = append(lines, "  "+keyStyle.Render(label))
		case row.key == m.remote.selected:
			lines = append(lines, keyStyle.Render("❯ ")+valueStyle.Render(label))
		default:
			lines = append(lines, "  "+mutedStyle.Render(label))
		}
	}
	return m.cardSized(width, "Remote sessions", strings.Join(lines, "\n"), hints)
}
