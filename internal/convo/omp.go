package convo

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// titleSlotType is the type of the line an omp session file opens with: a
// fixed-width record omp rewrites in place each time the session is named,
// so the file stays append-only below it.
const titleSlotType = "title"

// titleSlotBytes is the width omp gives that line, newline included
// (SESSION_TITLE_SLOT_BYTES in omp's session-title-slot.ts).
const titleSlotBytes = 256

// OmpSessionsDir is where omp keeps conversations, resolved the way omp
// resolves it for its default profile: PI_CODING_AGENT_SESSION_DIR, else the
// sessions directory under PI_CODING_AGENT_DIR, else under PI_CONFIG_DIR's
// agent directory, else ~/.omp/agent/sessions. omp reads pi's variable names
// (its own OMP_* names are mirrored onto them only inside .env files). Empty
// when there is no home directory to resolve against.
func OmpSessionsDir() string {
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_SESSION_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	if dir := strings.TrimSpace(os.Getenv("PI_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "agent", "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".omp", "agent", "sessions")
}

// OmpProjectDirs are the folder names omp may file cwd's sessions under,
// most likely first (getDefaultSessionDirName in omp's session-paths.ts). A
// directory under the temporary root is "-tmp-" and the path below it, one
// under home is "-" and the path below it, and anything else is pi's own
// "--path--" name. omp takes the temporary root from the runtime, TMPDIR or
// /tmp, and the pane omp ran in may have had another TMPDIR than this
// process: this one's comes first, then home, then /tmp as a guess. Pi's
// name is always last, since omp migrated older folders from it.
func OmpProjectDirs(cwd string) []string {
	clean := filepath.Clean(cwd)
	var dirs []string
	add := func(prefix, root string) {
		if rel, ok := within(filepath.Clean(root), clean); ok && root != "" {
			if dir := ompRelativeDir(prefix, rel); !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	add("-tmp", strings.TrimSpace(os.Getenv("TMPDIR")))
	if home, err := os.UserHomeDir(); err == nil {
		add("-", home)
	}
	add("-tmp", "/tmp")
	return append(dirs, PiProjectDir(clean))
}

// within is target relative to root when it is root or below it.
func within(root, target string) (string, bool) {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	if rel == "." {
		rel = ""
	}
	return rel, true
}

// ompRelativeDir is omp's encodeRelativeSessionDirName.
func ompRelativeDir(prefix, rel string) string {
	encoded := strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(rel)
	if encoded == "" {
		return prefix
	}
	if strings.HasSuffix(prefix, "-") {
		return prefix + encoded
	}
	return prefix + "-" + encoded
}

// readTitleSlot reads the title an omp session file's first line holds. ok
// is false for a file that does not open with one, which is every pi file.
func readTitleSlot(file io.ReaderAt) (string, bool) {
	line, err := bufio.NewReaderSize(io.NewSectionReader(file, 0, titleSlotBytes*2), titleSlotBytes*2).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return "", false
	}
	var slot struct {
		Type  string `json:"type"`
		Title string `json:"title"`
	}
	if json.Unmarshal(line, &slot) != nil || slot.Type != titleSlotType {
		return "", false
	}
	return strings.TrimSpace(slot.Title), true
}

// PiSession is one session file a pi-family store keeps for a directory.
type PiSession struct {
	Path    string
	Header  PiHeader
	ModTime time.Time
}

// SessionsFor lists the conversations the store keeps for cwd whose files
// were written at or after since, newest first. It is what capture reads for
// a CLI that mints its own conversation id (omp has no --session-id).
func (s PiStore) SessionsFor(cwd string, since time.Time) []PiSession {
	var out []PiSession
	for _, path := range s.filesFor(cwd, since, 0) {
		header, ok := ReadPiHeader(path)
		if !ok || !sameDir(header.Cwd, cwd) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		out = append(out, PiSession{Path: path, Header: header, ModTime: info.ModTime()})
	}
	return out
}
