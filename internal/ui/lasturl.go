package ui

import (
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/store"
)

// lastURLHistory is how far above the visible screen the open-last-link key
// looks, so a link that scrolled off a busy pane is still found.
const lastURLHistory = 2000

var urlPattern = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)

// captureForLastURL is the seam a test reads a pane through.
var captureForLastURL = func(m *Model, id string) (string, error) {
	return m.tmux.CaptureJoinedHistory(id, lastURLHistory)
}

// lastURL is the newest http(s) link in text, with the punctuation that
// closes the sentence or the bracket around it trimmed off.
func lastURL(text string) string {
	matches := urlPattern.FindAllString(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		// "https://." trims to a bare scheme, which is no link at all.
		if url := trimURL(matches[i]); !strings.HasSuffix(url, "://") {
			return url
		}
	}
	return ""
}

func trimURL(url string) string {
	for url != "" {
		last := url[len(url)-1]
		switch {
		case strings.IndexByte(".,;:!?*", last) >= 0:
		case last == ')' && strings.Count(url, "(") < strings.Count(url, ")"):
		case last == ']' && strings.Count(url, "[") < strings.Count(url, "]"):
		case last == '}' && strings.Count(url, "{") < strings.Count(url, "}"):
		default:
			return url
		}
		url = url[:len(url)-1]
	}
	return url
}

func (m *Model) openSelectedLastURL() (tea.Model, tea.Cmd) {
	sess, ok := m.selected()
	if !ok || sess.Archived {
		return m, nil
	}
	return m, m.openLastURL(sess)
}

// openLastURL opens the newest link sess's pane printed, through the same
// path a link row opens on, so a remote board copies it instead.
func (m *Model) openLastURL(sess store.Session) tea.Cmd {
	text, err := captureForLastURL(m, sess.ID)
	if err != nil {
		m.errBar.text = "could not read " + m.displayName(sess) + ": " + err.Error()
		return nil
	}
	url := lastURL(text)
	if url == "" {
		m.errBar.text = m.displayName(sess) + " has shown no link"
		return nil
	}
	m.reportDone("opening " + url)
	return openLink(url)
}
