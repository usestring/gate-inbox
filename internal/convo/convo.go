// Package convo reads what the agent CLIs already know about their own
// conversations, and works out which tmux pane each one is sitting in.
//
// The pane-to-conversation mapping is the package's product and is deliberately
// not private to any one caller. Two things in the manager need it -- naming a
// row after the conversation in it, and reading a session's own usage records
// to decide how warm its prompt cache is -- and they must not answer it
// differently. Link is the single answer: it takes panes, returns the
// assignments it is confident about and the panes it could not resolve, and
// carries the transcript path so a caller that wants the file has it.
//
// The contract Link enforces, for every caller:
//
//   - No conversation is ever assigned to two panes.
//   - A pane with no positive evidence is returned unresolved, never guessed.
//   - Sharing a working directory is not evidence. On the board this was built
//     against, 23 of 26 agent panes sit in one directory, so a rule that
//     resolves on cwd resolves nothing and refuses everything.
//
// Both tools on this board keep a title written by a frontier model that had
// the whole conversation in front of it. Claude Code writes an ai-title record
// into its JSONL transcript; opencode keeps a title column in its sqlite
// database. Nothing here generates a title, and nothing here calls a model:
// the good name already exists and the job is to find it and attribute it to
// the right row.
//
// Two rules shape the package. Everything it touches belongs to another
// running program, so every read is read-only and none of it happens on the
// UI thread -- the opencode database in particular is live and is opened
// mode=ro. And a wrong attribution is worse than none: it puts another
// session's name on a row, which is indistinguishable from a right one and
// stays wrong until somebody notices. So a pane with no positive evidence
// keeps the name it had.
package convo

import (
	"sync"
	"time"
)

// Conversation is one agent conversation as its own tool recorded it.
type Conversation struct {
	// Tool is the manager's name for the CLI: "claude", "opencode".
	Tool string
	// ID is the tool's own conversation id, which is also what revive resumes.
	ID string
	// Cwd is where the conversation is running.
	Cwd string
	// Title is the model-written title. Empty for a conversation too young to
	// have been given one.
	Title string
	// Prompts are recent user messages, most recent last. The title barely
	// moves and these move every turn, which is the whole of the drift signal.
	Prompts []string
	// FirstPrompts are the first user messages of the conversation, oldest
	// first, at most firstPromptCount of them. They never change once
	// written, which is what makes them the thing to say what a session
	// was started for after its title and Prompts have drifted on.
	FirstPrompts []string
	// Excerpts are recent assistant text, normalised for comparison against a
	// pane capture.
	Excerpts  []string
	UpdatedAt time.Time
	// PID is the agent process, when the tool records one. A pid found in a
	// pane's own process tree is an exact attribution and needs no heuristic
	// behind it.
	PID int
	// ProcStart is the pid's start time as the tool saw it, so a recycled pid
	// cannot impersonate a conversation that has ended.
	ProcStart string
	// TmuxHint is the pane the tool believes it is in, "session:@win.%pane".
	// Corroboration only: it is empty for conversations that predate the tool
	// recording it, and it is not rewritten when a pane is moved.
	TmuxHint string

	// TranscriptPath is the file this conversation was read out of: the
	// Claude Code JSONL. Empty for a tool that keeps its history in a
	// database rather than a file, which opencode does.
	//
	// Exported because attribution is the expensive half of the problem and
	// the file is what a second caller wants. A pane's transcript carries its
	// own message.usage records, so anything that needs to know how warm a
	// session's prompt cache is, or how big its context has grown, reads them
	// from here rather than solving the mapping again.
	TranscriptPath string
}

// Cost is what one refresh spent, so the price of the ticker is a number
// somebody can look at rather than a guess.
type Cost struct {
	Files int
	Bytes int64
	// Heads is how many transcript openings were read this pass. Counted
	// apart from Files because a head is read once per transcript and then
	// never again, where a tail is re-read on every append.
	Heads     int
	Cached    int
	Elapsed   time.Duration
	OpenCodeQ int

	// The phases of a refresh, because "the sweep takes 1.7s" is not a fact
	// anybody can act on. Sidecars is reading ~/.claude/sessions and proving
	// each pid; Paths is finding the transcripts worth reading; Tails is
	// reading and parsing them; OpenCode is the sqlite side.
	Sidecars time.Duration
	Paths    time.Duration
	Tails    time.Duration
	OpenCode time.Duration
}

// Index holds the conversations found by the last refresh.
//
// Refresh does the I/O and must run off the UI thread; Conversations and Cost
// are safe to call from a render path, which is the same division the work
// tracker draws.
type Index struct {
	claude   string
	opencode string

	// refreshMu serialises refreshes. The naming ticker re-arms without
	// waiting for the pass it started, so on a loaded machine a second pass
	// can begin while the first is still walking the transcripts, and they
	// share the tail cache.
	refreshMu sync.Mutex

	mu     sync.RWMutex
	convos []Conversation
	cost   Cost
	// tails caches a parsed transcript against the size and mtime it had when
	// it was read. There are 1259 transcripts on this machine and 1.4GB of
	// them; re-reading on a ticker is not available.
	tails map[string]tail
	// heads caches a transcript's opening prompts. A head that has all of
	// them is final; one still short of the count is re-read once the file
	// has grown.
	heads map[string]head
}

// New builds an index over a Claude Code home directory and an opencode
// database. Either may be empty, which simply contributes no conversations.
func New(claudeHome, opencodeDB string) *Index {
	return &Index{claude: claudeHome, opencode: opencodeDB, tails: map[string]tail{}, heads: map[string]head{}}
}

// Refresh re-reads the agents' state. It does file and database I/O and must
// not be called from Update or a render path.
//
// dirs are working directories worth looking at beyond the ones a live agent
// process already names, which is how a pane whose tool keeps no pid sidecar
// still gets candidates.
func (ix *Index) Refresh(dirs []string) error {
	ix.refreshMu.Lock()
	defer ix.refreshMu.Unlock()

	started := time.Now()
	var cost Cost
	convos := ix.claudeConversations(dirs, &cost)
	ocStart := time.Now()
	oc, err := ix.opencodeConversations(&cost)
	cost.OpenCode = time.Since(ocStart)
	convos = append(convos, oc...)
	cost.Elapsed = time.Since(started)
	ix.pruneTails(convos)

	// What was found is published even when something failed, so one tool
	// being unreadable does not blind the caller to the other. The error is
	// still returned; it is a report, not a reason to discard the pass.
	ix.mu.Lock()
	ix.convos = convos
	ix.cost = cost
	ix.mu.Unlock()
	return err
}

// Conversations is what the last refresh found.
func (ix *Index) Conversations() []Conversation {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return append([]Conversation{}, ix.convos...)
}

// pruneTails drops cached transcripts no live conversation points at, so a
// manager left running for a week is not holding the last words of every
// session that has since closed.
func (ix *Index) pruneTails(convos []Conversation) {
	live := make(map[string]bool, len(convos))
	for _, convo := range convos {
		live[convo.TranscriptPath] = true
	}
	for path := range ix.tails {
		if !live[path] {
			delete(ix.tails, path)
		}
	}
	for path := range ix.heads {
		if !live[path] {
			delete(ix.heads, path)
		}
	}
}

// Cost is what the last refresh spent.
func (ix *Index) Cost() Cost {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.cost
}
