package hooks

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/singleton"
)

// Global Codex hooks.
//
// A codex the board adopted never loaded anything the board would have put
// on its command line, and interactive codex runs its agent loops, MCP
// servers and hooks inside one shared app-server daemon, whose environment
// is that of whichever pane started it. So the pane tree and $TMUX_PANE that
// the Claude Code global hooks key on name the wrong pane here. What a codex
// hook does know is its thread: every payload carries the thread id as
// session_id. The board therefore keeps two hook entries, UserPromptSubmit
// and Stop, in the user's own config.toml, and everything they do is keyed
// on that thread id (see codexbind.go).
//
// Codex asks the user to trust a hook the first time it sees it, and again
// whenever its command changes: it records a hash of the entry under
// [hooks.state] in config.toml. That review is codex's consent step and this
// package never answers it or writes the trust record itself. It keeps the
// command byte-stable instead -- it names only the board's home and the
// installed binary under it, and its text is pinned by a test -- so a user
// trusts it once and a board upgrade does not ask again.
//
// The entries are inert until the board needs them. The prelude is shell
// builtins only, and ends the hook with no output and exit 0 unless the
// board holds a codex row (hooks/codex/watch exists), the board's lock names
// a live process, and the installed binary is still there. Only then does it
// hand the payload to `gate-inbox hook codex <event>`, which still says and
// writes nothing for a thread the board does not hold.

// codexTag opens every codex hook command, so this package can find the
// entries it wrote in a file that is otherwise the operator's.
const codexTag = ": gate-inbox-codex-hook"

// codexEntryComment is the line each entry carries under its event header,
// for the operator reading config.toml.
const codexEntryComment = "# Added by Gate Inbox for the sessions its board adopts; silent everywhere else. Remove with: gate-inbox codex-hooks uninstall"

// CodexEvents are the codex hook events the board registers.
var CodexEvents = []string{"UserPromptSubmit", "Stop"}

// codexHookTimeout is the timeout, in seconds, each entry asks codex for.
const codexHookTimeout = 30

// codexDisabledName keeps the board from registering the codex hooks again
// after an operator removed them.
const codexDisabledName = "codex-hooks.disabled"

// CodexConfigPath is the config.toml every codex reads: under CODEX_HOME
// when it is set, as codex does, and ~/.codex otherwise.
func CodexConfigPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return filepath.Join(dir, "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func codexMark(configDir string) string {
	return codexTag + " " + shellQuote(configDir) + "; "
}

// CodexWatchFile is the file whose presence lets the prelude go on: the
// board writes it while it holds a codex row.
func CodexWatchFile(configDir string) string {
	return filepath.Join(configDir, "hooks", codexDirName, "watch")
}

// codexCommand is the one command registered for event. Its bytes are what
// codex's trust record hashes, so any change here asks every user to review
// the hooks again: change it only on purpose.
func codexCommand(configDir, bin, event string) string {
	return codexMark(configDir) + `exec 2>/dev/null; ` +
		`[ -f ` + shellQuote(CodexWatchFile(configDir)) + ` ] || exit 0; ` +
		`read -r b < ` + shellQuote(filepath.Join(configDir, singleton.FileName)) + `; [ -n "$b" ] && kill -0 "$b" || exit 0; ` +
		`[ -x ` + shellQuote(bin) + ` ] || exit 0; ` +
		config.HomeEnv + `=` + shellQuote(configDir) + ` ` + shellQuote(bin) + ` hook codex ` + event + `; exit 0`
}

// codexBlock is the TOML for one event's entry.
func codexBlock(configDir, bin, event string) string {
	return "[[hooks." + event + "]]\n" +
		codexEntryComment + "\n" +
		"[[hooks." + event + ".hooks]]\n" +
		"type = \"command\"\n" +
		"command = " + tomlString(codexCommand(configDir, bin, event)) + "\n" +
		fmt.Sprintf("timeout = %d\n", codexHookTimeout)
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// RegisterCodex puts this board's codex hooks into the config.toml at path.
// Entries this board wrote before are left exactly where they are when they
// already match, so codex's trust record for them stays valid; otherwise
// they are taken out and the current ones appended. Every other byte of the
// file -- comments, ordering, the operator's own hooks, codex's trust
// records -- is kept. changed is false when nothing was written.
func RegisterCodex(path, configDir, bin string) (changed bool, err error) {
	return rewriteCodex(path, func(raw []byte) ([]byte, error) {
		return mergeCodex(raw, codexMark(configDir), configDir, bin)
	})
}

// UnregisterCodex removes the codex hooks of the board at configDir, or of
// every board when configDir is empty. The trust records codex wrote for
// them are codex's and stay; they hash commands that are no longer there,
// so they vouch for nothing.
func UnregisterCodex(path, configDir string) (changed bool, err error) {
	mark := codexTag + " "
	if configDir != "" {
		mark = codexMark(configDir)
	}
	return rewriteCodex(path, func(raw []byte) ([]byte, error) {
		return mergeCodex(raw, mark, "", "")
	})
}

// CodexRegistered reports whether the config at path carries this board's
// codex hooks exactly as this build would write them.
func CodexRegistered(path, configDir, bin string) (bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	merged, err := mergeCodex(raw, codexMark(configDir), configDir, bin)
	if err != nil {
		return false, err
	}
	return bytes.Equal(merged, raw), nil
}

// CodexDisabled reports that an operator removed the codex hooks and the
// board must not put them back.
func (m *Manager) CodexDisabled() bool {
	_, err := os.Stat(filepath.Join(m.dir, codexDisabledName))
	return err == nil
}

// SetCodexDisabled records, or clears, the operator's removal.
func (m *Manager) SetCodexDisabled(disabled bool) error {
	path := filepath.Join(m.dir, codexDisabledName)
	if !disabled {
		return removeIfExists(path)
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}

// rewriteCodex applies merge to the file at path and writes the result back
// when it differs, through a link and keeping the file's mode, as
// rewriteSettings does for Claude Code's.
func rewriteCodex(path string, merge func([]byte) ([]byte, error)) (bool, error) {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	merged, err := merge(raw)
	if err != nil {
		return false, err
	}
	if bytes.Equal(merged, raw) {
		return false, nil
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		probe, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return false, fmt.Errorf("hooks: %s is not writable: %w", path, err)
		}
		probe.Close()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(merged); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}

// tomlSegment is one table of a TOML file as text: its header line and every
// line up to the next header. The lines before the first header have an
// empty header.
type tomlSegment struct {
	header string
	text   string
}

// splitTOML cuts raw into segments at table headers. A line inside a
// multi-line string is never taken for a header. Joining the segments' text
// gives raw back byte for byte.
func splitTOML(raw string) []tomlSegment {
	var segments []tomlSegment
	current := tomlSegment{}
	inString := ""
	for len(raw) > 0 {
		line := raw
		if nl := strings.IndexByte(raw, '\n'); nl >= 0 {
			line = raw[:nl+1]
		}
		raw = raw[len(line):]
		trimmed := strings.TrimSpace(line)
		if inString == "" && strings.HasPrefix(trimmed, "[") {
			segments = append(segments, current)
			current = tomlSegment{header: headerName(trimmed)}
		}
		current.text += line
		for _, quote := range []string{`"""`, `'''`} {
			if inString != "" && inString != quote {
				continue
			}
			if strings.Count(line, quote)%2 == 1 {
				if inString == "" {
					inString = quote
				} else {
					inString = ""
				}
			}
		}
	}
	return append(segments, current)
}

// headerName is a header line without its trailing comment or whitespace:
// "[[hooks.Stop]]  # x" is "[[hooks.Stop]]".
func headerName(line string) string {
	end := strings.LastIndexByte(line, ']')
	if end < 0 {
		return line
	}
	return strings.TrimSpace(line[:end+1])
}

// segmentCommand is the command a [[hooks.<Event>.hooks]] segment runs, or
// "" when it has none that decodes.
func segmentCommand(text string) string {
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "command" {
			continue
		}
		var decoded struct{ V string }
		if _, err := toml.Decode("V = "+strings.TrimSpace(value), &decoded); err == nil {
			return decoded.V
		}
	}
	return ""
}

// onlyComments reports whether text after its header line is nothing but
// blank lines and comments.
func onlyComments(text string) bool {
	_, body, _ := strings.Cut(text, "\n")
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return false
		}
	}
	return true
}

// mergeCodex is raw with every hook entry whose command starts with mark
// taken out, then, when bin is set, this board's entries in place. Entries
// that already match exactly are left alone, so the merge of a file that is
// up to date returns raw byte for byte. A file that is not valid TOML is
// refused, and so is a merge that would make it invalid: the operator's
// file is never left broken.
func mergeCodex(raw []byte, mark, configDir, bin string) ([]byte, error) {
	var probe map[string]any
	if _, err := toml.Decode(string(raw), &probe); err != nil {
		return nil, fmt.Errorf("hooks: the codex config is not valid TOML: %w", err)
	}
	segments := splitTOML(string(raw))
	ours := map[int]bool{}
	found := map[string]string{}
	for i, seg := range segments {
		event, ok := strings.CutSuffix(strings.TrimPrefix(seg.header, "[[hooks."), ".hooks]]")
		if !ok || !strings.HasPrefix(seg.header, "[[hooks.") || !strings.HasPrefix(segmentCommand(seg.text), mark) {
			continue
		}
		ours[i] = true
		parent := i - 1
		if parent >= 0 && segments[parent].header == "[[hooks."+event+"]]" && onlyComments(segments[parent].text) &&
			(i+1 >= len(segments) || segments[i+1].header != seg.header) {
			ours[parent] = true
			found[event] += segments[parent].text
		}
		found[event] += seg.text
	}
	if bin != "" {
		current := len(found) == len(CodexEvents)
		for _, event := range CodexEvents {
			if strings.TrimRight(found[event], "\n") != strings.TrimRight(codexBlock(configDir, bin, event), "\n") {
				current = false
			}
		}
		if current {
			return raw, nil
		}
	}
	if len(ours) == 0 && bin == "" {
		return raw, nil
	}
	var out strings.Builder
	removedTail := false
	for i, seg := range segments {
		if ours[i] {
			removedTail = true
			continue
		}
		if seg.text != "" {
			removedTail = false
		}
		out.WriteString(seg.text)
	}
	text := out.String()
	// An entry appended at the end brought a blank line to part it from
	// what was there; taking it out again takes that line too.
	if removedTail && strings.HasSuffix(text, "\n\n") {
		text = text[:len(text)-1]
	}
	if bin != "" {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		for _, event := range CodexEvents {
			if text != "" && !strings.HasSuffix(text, "\n\n") {
				text += "\n"
			}
			text += codexBlock(configDir, bin, event)
		}
	}
	var check map[string]any
	if _, err := toml.Decode(text, &check); err != nil {
		return nil, fmt.Errorf("hooks: Gate Inbox's codex hooks cannot be merged into this config: %w", err)
	}
	return []byte(text), nil
}
