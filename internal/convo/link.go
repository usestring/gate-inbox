package convo

import (
	"sort"
	"strings"
)

// Signal is one piece of evidence that a pane is running a conversation.
type Signal string

const (
	// SignalAgentID is the caller already knowing which conversation the pane
	// was launched with. A session the manager started itself chose the id and
	// passed it on the command line, so this is the same kind of identity
	// SignalProcess is, arrived at without reading the pane at all.
	SignalAgentID Signal = "agent-id"
	// SignalProcess is the conversation's own process sitting in the pane's
	// process tree. Claude Code writes a sidecar naming both, so this is an
	// identity rather than a resemblance, and it needs nothing behind it.
	SignalProcess Signal = "process"
	// SignalTmux is the pane id the tool believes it is in. Corroboration
	// only: it is absent on conversations started before the tool recorded it.
	SignalTmux Signal = "tmux"
	// SignalCwd is the conversation and the pane agreeing on a directory.
	// Necessary and nowhere near sufficient -- on this board eighty-odd panes
	// share one.
	SignalCwd Signal = "cwd"
	// SignalText is prose the conversation produced showing in the pane.
	SignalText Signal = "text"
	// SignalSolo is the conversation being the only candidate for the pane and
	// the pane the only candidate for it, once tool and directory have cut the
	// field down. A forced bijection, not a preference.
	SignalSolo Signal = "solo"
)

// Pane is one tmux pane as linking needs it.
type Pane struct {
	// Key identifies the pane to the caller and comes back on the match.
	Key  string
	Tool string
	Cwd  string
	// Text is the pane's visible content, already run through Normalize.
	Text string
	// PIDs are the pane's process and its descendants.
	PIDs []int
	// AgentID is the conversation id the pane was launched with, for a pane
	// whose launch the caller owns. Empty for a pane found rather than
	// started, which is what leaves the evidence below to work it out.
	AgentID string
}

// Match is one pane paired with the conversation it is running.
type Match struct {
	PaneKey      string
	Conversation Conversation
	Signals      []Signal
}

// Assignment is what one linking pass concluded.
//
// Unresolved is as much of the answer as Matched. A caller that treats a
// missing key as "this pane is running nothing" is wrong: a pane can be a
// fresh agent that has not written a transcript yet, or one whose prose has
// scrolled away, and neither is an empty pane.
type Assignment struct {
	// Matched is pane key to the conversation in it.
	Matched map[string]Match
	// Unresolved is the panes nothing could be attributed to, in the order
	// they were given.
	Unresolved []string
}

// For returns the conversation attributed to a pane.
func (a Assignment) For(paneKey string) (Match, bool) {
	match, ok := a.Matched[paneKey]
	return match, ok
}

// Has reports whether a signal contributed to this match.
func (m Match) Has(want Signal) bool {
	for _, sig := range m.Signals {
		if sig == want {
			return true
		}
	}
	return false
}

// excerptEdge is how much of an excerpt is looked for in a pane capture.
//
// The whole message is no good: a terminal rewraps, truncates and redraws, so
// what is on screen is a fragment. Both ends are tried because either can be
// the fragment that survived -- the opening if the message is still being
// written, the closing if it has scrolled up to the top of the screen.
const excerptEdge = 90

// Link decides which conversation each pane is running.
//
// The rule the whole function exists to enforce: a pane with no positive
// evidence gets no conversation. A wrong link puts another session's name on a
// row, which reads exactly like a right one and stays wrong until somebody
// notices, so weak evidence is worth strictly less than none. Directory alone
// is never enough, recency alone is never enough, and no conversation is ever
// handed to two panes.
func Link(panes []Pane, convos []Conversation) Assignment {
	type pairing struct {
		pane    int
		convo   int
		signals []Signal
		rank    int
	}
	soloPane, soloConvo := soloCounts(panes, convos)

	var pairs []pairing
	for pi, pane := range panes {
		for ci, convo := range convos {
			// A pane whose tool the caller could not name is not an agent pane,
			// and a shell that happens to sit in the right directory showing the
			// right words is exactly the wrong thing to hand a name to.
			if pane.Tool == "" || pane.Tool != convo.Tool {
				continue
			}
			signals := evidence(pane, convo)
			if !accepts(signals) {
				bucket := paneBucket(pane.Tool, pane.Cwd)
				if hasSignal(signals, SignalCwd) && soloPane[bucket] == 1 && soloConvo[bucket] == 1 {
					signals = append(signals, SignalSolo)
				} else {
					continue
				}
			}
			pairs = append(pairs, pairing{pane: pi, convo: ci, signals: signals, rank: rank(signals)})
		}
	}
	// Strongest evidence first, and every tie broken on a stable key, so the
	// same board links the same way twice. Taking pairs in this order rather
	// than in pane order is what keeps a pane that merely shares a directory
	// from claiming the conversation an exact process match wanted.
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].rank != pairs[j].rank {
			return pairs[i].rank > pairs[j].rank
		}
		if !convos[pairs[i].convo].UpdatedAt.Equal(convos[pairs[j].convo].UpdatedAt) {
			return convos[pairs[i].convo].UpdatedAt.After(convos[pairs[j].convo].UpdatedAt)
		}
		if panes[pairs[i].pane].Key != panes[pairs[j].pane].Key {
			return panes[pairs[i].pane].Key < panes[pairs[j].pane].Key
		}
		return convos[pairs[i].convo].ID < convos[pairs[j].convo].ID
	})

	matched := map[string]Match{}
	usedPane := map[int]bool{}
	usedConvo := map[int]bool{}
	for _, pair := range pairs {
		if usedPane[pair.pane] || usedConvo[pair.convo] {
			continue
		}
		usedPane[pair.pane] = true
		usedConvo[pair.convo] = true
		matched[panes[pair.pane].Key] = Match{
			PaneKey:      panes[pair.pane].Key,
			Conversation: convos[pair.convo],
			Signals:      pair.signals,
		}
	}
	var unresolved []string
	for _, pane := range panes {
		if _, ok := matched[pane.Key]; !ok {
			unresolved = append(unresolved, pane.Key)
		}
	}
	return Assignment{Matched: matched, Unresolved: unresolved}
}

// evidence is every signal a pane and a conversation share.
func evidence(pane Pane, convo Conversation) []Signal {
	var signals []Signal
	if pane.AgentID != "" && pane.AgentID == convo.ID {
		signals = append(signals, SignalAgentID)
	}
	if convo.PID > 0 && containsPID(pane.PIDs, convo.PID) {
		signals = append(signals, SignalProcess)
	}
	if convo.TmuxHint != "" && strings.HasSuffix(convo.TmuxHint, "."+pane.Key) {
		signals = append(signals, SignalTmux)
	}
	if pane.Cwd != "" && pane.Cwd == convo.Cwd {
		signals = append(signals, SignalCwd)
	}
	if showsExcerpt(pane.Text, convo.Excerpts) {
		signals = append(signals, SignalText)
	}
	return signals
}

// accepts is the confidence rule. A launched-with id and a process match are
// both identities and each stands alone; anything else has to show both that
// the pane is in the right place and that it is showing this conversation's
// words.
func accepts(signals []Signal) bool {
	if hasSignal(signals, SignalAgentID) || hasSignal(signals, SignalProcess) {
		return true
	}
	return hasSignal(signals, SignalCwd) && hasSignal(signals, SignalText)
}

func rank(signals []Signal) int {
	score := 0
	for _, sig := range signals {
		switch sig {
		case SignalAgentID:
			score += 200
		case SignalProcess:
			score += 100
		case SignalTmux:
			score += 20
		case SignalText:
			score += 10
		case SignalCwd:
			score += 5
		case SignalSolo:
			score += 1
		}
	}
	return score
}

func hasSignal(signals []Signal, want Signal) bool {
	for _, sig := range signals {
		if sig == want {
			return true
		}
	}
	return false
}

func containsPID(pids []int, want int) bool {
	for _, pid := range pids {
		if pid == want {
			return true
		}
	}
	return false
}

func showsExcerpt(paneText string, excerpts []string) bool {
	if paneText == "" {
		return false
	}
	for _, excerpt := range excerpts {
		for _, edge := range edges(excerpt) {
			if strings.Contains(paneText, edge) {
				return true
			}
		}
	}
	return false
}

// edges is the head and tail of an excerpt, each cut at a word boundary so a
// half word cannot match a different one.
func edges(excerpt string) []string {
	if len(excerpt) < 24 {
		return nil
	}
	if len(excerpt) <= excerptEdge {
		return []string{excerpt}
	}
	head := excerpt[:excerptEdge]
	if cut := strings.LastIndexByte(head, ' '); cut > 24 {
		head = head[:cut]
	}
	tail := excerpt[len(excerpt)-excerptEdge:]
	if cut := strings.IndexByte(tail, ' '); cut >= 0 && len(tail)-cut > 24 {
		tail = tail[cut+1:]
	}
	return []string{head, tail}
}

func paneBucket(tool, cwd string) string { return tool + "\x00" + cwd }

// soloCounts is how many panes and how many conversations sit in each
// tool-and-directory bucket, which is what makes a bijection provable.
func soloCounts(panes []Pane, convos []Conversation) (map[string]int, map[string]int) {
	panesBy := map[string]int{}
	for _, pane := range panes {
		panesBy[paneBucket(pane.Tool, pane.Cwd)]++
	}
	convosBy := map[string]int{}
	for _, convo := range convos {
		convosBy[paneBucket(convo.Tool, convo.Cwd)]++
	}
	return panesBy, convosBy
}
