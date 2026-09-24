// Package adopt finds agent sessions the manager did not start.
//
// An agent somebody already had running may be on a different tmux server
// entirely. It cannot be moved: tmux has no way to pass a window between
// servers. So adoption reaches across instead, and everything here is about
// deciding what is safe to reach for.
//
// Two rules shape it. Identification must be able to say "I don't know", because
// the cost of a wrong guess is not a missing row — it is the manager typing a
// sentence at whatever that pane really is. And nothing here writes: scanning
// somebody's live tmux server must not be able to change it.
package adopt

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/tmuxguard"
)

// Candidate is one pane on some tmux server, before anything is known about
// what is running in it.
type Candidate struct {
	// Socket is the tmux server this pane lives on, always named rather than
	// implied, so the pane can be addressed again later from anywhere.
	Socket string
	// PaneID is tmux's own "%12". It is stable for the pane's life and is a
	// valid -t target for capture, send-keys, resize and display, which is what
	// lets an adopted pane be driven without ever knowing a session name.
	PaneID  string
	Session string
	Cwd     string
	// Command is the pane's foreground process as tmux reports it. Useful but
	// never sufficient: an agent started from a shell often reads as "node".
	Command string
	PID     int32
}

// Signal is one piece of evidence that a pane is running a given tool.
type Signal string

const (
	// SignalCommand is the definitive one: a process in the pane's own tree
	// was launched as the tool. A pane whose process is claude is a claude
	// agent; there is nothing else it could be.
	SignalCommand Signal = "command"
	// SignalPrompt is the pane drawing that tool's own input marker. The weak
	// one: pane text is whatever happens to be on the screen, so a shell with
	// a transcript in its scrollback draws it as convincingly as an agent.
	SignalPrompt Signal = "prompt"
)

// Match is a candidate that looks like a tool, and why.
type Match struct {
	Candidate Candidate
	Tool      string
	Signals   []Signal
}

func (m Match) has(want Signal) bool {
	for _, s := range m.Signals {
		if s == want {
			return true
		}
	}
	return false
}

// Confident reports whether this match may be adopted without being shown to
// anybody first.
//
// The two signals are not equally good evidence, so the rule is asymmetric on
// purpose. A pane whose process tree runs the tool IS that tool; nothing else
// it could be. Demanding the marker as well refuses every agent with nothing
// on screen right now -- mid-turn, holding a dialog, scrolled back into its
// own history -- which is most of what a triage board exists to surface.
//
// The marker alone never suffices, and that half is not symmetry for its own
// sake: a plain shell draws it perfectly from a transcript left in its
// scrollback, and the cost of adopting one is not a spurious row, it is a
// sentence eventually typed at somebody's shell.
func (m Match) Confident() bool {
	return m.has(SignalCommand)
}

// strength orders matches for the tie-break in Identify, from the same
// evidence Confident weighs. Higher is better corroborated.
func (m Match) strength() int {
	score := 0
	if m.has(SignalCommand) {
		score += 2
	}
	if m.has(SignalPrompt) {
		score++
	}
	return score
}

// Tool is what identification needs to know about one configured tool: the
// subset of config.Tool that says what the thing looks like while running.
type Tool struct {
	Name string
	// Command is the launch command as configured; only its program name is
	// compared, so flags and quoting in the config do not matter here.
	Command string
	// Prompt is the tool's input marker, the same expression the status engine
	// uses to find the start of the live prompt.
	Prompt *regexp.Regexp
	// Shell marks a block that opens a plain shell. Never adoptable as an
	// agent: a sentence typed at a shell is a command.
	Shell bool
}

// Identify decides which tool, if any, a pane is running.
//
// pane is the pane's visible text and procs is the process table; the caller
// supplies both, because capturing is the one thing here that has to talk to a
// specific tmux server, and because a scan asks about many panes and must not
// re-read the process table for each of them.
func Identify(c Candidate, tools []Tool, pane string, procs *ProcTable) (Match, bool) {
	argv := procs.Cmdlines(c.PID)
	argv = append(argv, c.Command)

	best := Match{}
	for _, tool := range tools {
		if tool.Shell {
			continue
		}
		var signals []Signal
		if program := programName(tool.Command); program != "" && namesProgram(argv, program) {
			signals = append(signals, SignalCommand)
		}
		if tool.Prompt != nil && pane != "" && tool.Prompt.MatchString(pane) {
			signals = append(signals, SignalPrompt)
		}
		if len(signals) == 0 {
			continue
		}
		candidate := Match{Candidate: c, Tool: tool.Name, Signals: signals}
		// Ranked by how well corroborated the evidence is, not by how many
		// pieces of it there are: now that either signal alone can identify a
		// tool, two tools matching one signal each have to be separated by
		// which signal it was, or a prompt marker two tools share would
		// decide a running process by map iteration order.
		if candidate.strength() > best.strength() {
			best = candidate
		}
	}
	return best, best.Tool != ""
}

// programName reduces a configured command to the program it runs, so
// "claude --dangerously-skip-permissions" and "/usr/local/bin/claude" both
// compare as "claude".
func programName(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// interpreters are the programs that run a script named by a later argv
// field. Only after one of these does a non-leading field name the program
// that is really running.
var interpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
	"env": true, "node": true, "bun": true, "deno": true, "python": true,
	"python3": true, "perl": true, "ruby": true,
}

// namesProgram reports whether any command line in the pane runs program.
//
// Only the program the line actually runs counts, never every field in it. A
// CLI installed as a shebang script -- how agent CLIs ship -- is reported by
// /proc as its interpreter followed by its own path, so the tool is argv[1]
// and the pane running it reads as running /bin/sh; refusing later fields
// outright refuses the fixture agent in adopt_test.go. Accepting them
// unconditionally is what this replaced, and it made any argument whose
// basename happened to match an identification: "ls /home/pi" adopted a plain
// shell as a pi agent, and the manager eventually types into an adopted pane.
func namesProgram(argv []string, program string) bool {
	for _, line := range argv {
		if filepath.Base(lineProgram(strings.Fields(line))) == program {
			return true
		}
	}
	return false
}

// lineProgram is the program one command line runs: its first field, or, when
// that is an interpreter, the first field after it that is neither a flag, an
// environment assignment, nor another interpreter. That last step is what
// reads "env node /opt/pi" as pi, and it is deliberately the *first* such
// field: "sh -c ls /home/pi" runs ls, and stopping there is what keeps a
// path-shaped argument from naming the program.
func lineProgram(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	if !interpreters[filepath.Base(fields[0])] {
		return fields[0]
	}
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") || strings.Contains(field, "=") {
			continue
		}
		if interpreters[filepath.Base(field)] {
			continue
		}
		return field
	}
	return fields[0]
}

// paneFormat is what a scan asks tmux for, in the order Panes parses it.
const paneFormat = "#{pane_id}\t#{session_name}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}"

// Panes lists every pane on one tmux server.
//
// A server that is not running is not an error worth reporting: sockets come and
// go, and a scan runs on a timer.
func Panes(socket string) []Candidate {
	out, err := tmuxOutput(socket, "list-panes", "-a", "-F", paneFormat)
	if err != nil {
		return nil
	}

	if socket == "" {
		socket = DefaultSocket
	}
	var found []Candidate
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 5 || parts[0] == "" {
			continue
		}
		pid, err := strconv.ParseInt(parts[2], 10, 32)
		if err != nil {
			continue
		}
		found = append(found, Candidate{
			Socket:  socket,
			PaneID:  parts[0],
			Session: parts[1],
			PID:     int32(pid),
			Command: parts[3],
			Cwd:     parts[4],
		})
	}
	return found
}

// Capture reads a pane's visible text, for the prompt signal.
func Capture(socket, paneID string) (string, error) {
	out, err := tmuxOutput(socket, "capture-pane", "-p", "-t", paneID)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// tmuxOutput runs one tmux read and records it, so the scan's own forks are
// in the log beside the driver's rather than being the one tmux traffic this
// program makes and never accounts for. Panes in particular drops its error
// on purpose, which without a line here leaves an empty scan unexplained.
func tmuxOutput(socket string, args ...string) ([]byte, error) {
	start := time.Now()
	tmuxguard.Enforce(tmuxArgs(socket, args...))
	out, err := exec.Command("tmux", tmuxArgs(socket, args...)...).Output()
	if err != nil {
		if logging.Enabled(logging.LevelWarn) {
			logging.Warn("tmux command failed", "socket", socket,
				"cmd", strings.Join(args, " "),
				"took", time.Since(start).Round(time.Microsecond).String(),
				logging.Err(err))
		}
		return nil, err
	}
	if logging.Enabled(logging.LevelDebug) {
		logging.Debug("tmux command", "socket", socket,
			"cmd", strings.Join(args, " "),
			"took", time.Since(start).Round(time.Microsecond).String())
	}
	return out, nil
}

// DefaultSocket is tmux's own default server, where a pane somebody started by
// hand lives unless they chose otherwise.
const DefaultSocket = "default"

// tmuxArgs prefixes a command with the server to run it against.
//
// The server is always named, never left to tmux to infer. A bare tmux command
// run from inside a pane takes its server from $TMUX, so the manager -- which
// runs inside a pane itself -- would scan the server it is sitting in rather
// than the one its operator's agents are on. That reads as "found nothing"
// rather than as an error, and it only misbehaves when running under tmux,
// which is to say only in production.
func tmuxArgs(socket string, command ...string) []string {
	if socket == "" {
		socket = DefaultSocket
	}
	return append([]string{"-L", socket}, command...)
}

// CaptureLines reads a pane's text together with some of its scrollback.
//
// The visible screen alone is a poor thing to match a conversation against: an
// agent that has just printed a tool result has pushed its own prose off the
// top. lines is how far back to reach. Soft wraps must not split the transcript
// words used for matching.
func CaptureLines(socket, paneID string, lines int) (string, error) {
	args := []string{"capture-pane", "-p", "-J"}
	if lines > 0 {
		args = append(args, "-S", "-"+strconv.Itoa(lines))
	}
	args = append(args, "-t", paneID)
	out, err := tmuxOutput(socket, args...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
