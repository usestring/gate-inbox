// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"os"
	"path/filepath"
	"strings"
)

// maxPathSuggestions bounds how much of a directory the listing holds. It is
// far more than fits on screen because the dropdown scrolls: a directory with
// a hundred children is walked, not retyped.
const maxPathSuggestions = 200

type pathComplete struct {
	suggestions []string
	index       int
	chosen      bool
	// auto marks a listing that opened because the field took focus rather
	// than because somebody typed. Until an entry is picked out of it, the
	// keys that leave the field still leave it.
	auto bool
	// browsing marks the arrow keys as walking the tree rather than the text.
	// It outlives an empty listing so that stepping into a directory with no
	// children of its own is not a one-way door.
	browsing bool
}

func (pc *pathComplete) reset() {
	pc.suggestions = nil
	pc.index = 0
	pc.chosen = false
	pc.auto = false
	pc.browsing = false
}

func (pc *pathComplete) active() bool { return len(pc.suggestions) > 0 }

// showing reports whether the dropdown has anything to draw: a listing, or
// the note that the directory being walked holds no others.
func (pc *pathComplete) showing() bool { return pc.active() || pc.browsing }

// capturing reports whether the dropdown owns the keys that would otherwise
// leave the field. A listing somebody typed towards owns them from the start;
// one that opened by itself owns them only once the tree is being walked, so
// tab and esc keep working on a field that was merely focused.
func (pc *pathComplete) capturing() bool {
	return pc.browsing || (pc.active() && !pc.auto)
}

// move advances within the suggestions without wrapping. It returns false
// when the cursor is already at the requested edge so the containing form can
// continue focus navigation instead of trapping the user in this list.
func (pc *pathComplete) move(delta int) bool {
	if !pc.active() {
		return false
	}
	if !pc.chosen {
		if delta < 0 {
			return false
		}
		pc.index = 0
		pc.chosen = true
		pc.browsing = true
		return true
	}
	next := pc.index + delta
	if next < 0 || next >= len(pc.suggestions) {
		return false
	}
	pc.index = next
	pc.chosen = true
	pc.browsing = true
	return true
}

func (pc *pathComplete) selected() string {
	if !pc.active() {
		return ""
	}
	return pc.suggestions[pc.index]
}

func (pc *pathComplete) recompute(typed string) {
	pc.suggestions = completeDirs(typed)
	pc.index = 0
	pc.chosen = false
	pc.auto = false
	pc.browsing = false
}

// browse opens the listing for a path field that was just focused. The field
// arrives holding a directory rather than a prefix somebody is part way
// through typing, so what belongs under it is that directory's own children.
func (pc *pathComplete) browse(value string) {
	pc.recompute(listingInput(value))
	pc.auto = true
}

// relist points the listing at a directory the tree walk moved to, keeping
// the named child highlighted so stepping out lands back where it left.
func (pc *pathComplete) relist(dir, keep string) {
	pc.recompute(dir + "/")
	pc.auto = true
	pc.browsing = true
	for i, path := range pc.suggestions {
		if path == keep {
			pc.index, pc.chosen = i, true
			return
		}
	}
	if pc.active() {
		pc.index, pc.chosen = 0, true
	}
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// listingInput turns a field value into what completion should list: a value
// that already names a directory lists what is inside it, and anything else
// is left alone to be matched as a prefix.
func listingInput(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasSuffix(trimmed, "/") {
		return trimmed
	}
	if isDir(expandHome(trimmed)) {
		return trimmed + "/"
	}
	return trimmed
}

// listingDir is the directory a field value is browsing: the value itself
// when it names one, else the parent the partial name is being matched in.
func listingDir(value string) string {
	trimmed := expandHome(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}
	if strings.HasSuffix(trimmed, "/") || isDir(trimmed) {
		return filepath.Clean(trimmed)
	}
	return filepath.Dir(trimmed)
}

// completeDirs matches shell completion: everything after the last slash
// is a partial name matched against directories inside its parent.
func completeDirs(typed string) []string {
	typed = expandHome(strings.TrimSpace(typed))
	if typed == "" || !strings.Contains(typed, "/") {
		return nil
	}
	parent, partial := filepath.Split(typed)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	partialLower := strings.ToLower(partial)
	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(partial, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), partialLower) {
			continue
		}
		if !isDirEntry(parent, entry) {
			continue
		}
		matches = append(matches, filepath.Join(parent, name))
		if len(matches) == maxPathSuggestions {
			break
		}
	}
	return matches
}

// isDirEntry treats symlinks to directories as directories (e.g. /tmp on macOS).
func isDirEntry(parent string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(parent, entry.Name()))
	return err == nil && info.IsDir()
}

// pathFieldValue is the path field of whichever form is open.
func (m *Model) pathFieldValue() string {
	switch m.mode {
	case modeForm:
		return m.form.dir.Value()
	case modeGroupForm:
		return m.groupForm.path.Value()
	}
	return ""
}

// setPathField writes a path into whichever form is open, and records that
// the value is now somebody's choice rather than a default the form filled
// in, so re-picking a parent group does not overwrite it.
func (m *Model) setPathField(path string) {
	switch m.mode {
	case modeForm:
		m.form.dir.SetValue(path)
		m.form.dir.CursorEnd()
		m.form.dirAuto = false
	case modeGroupForm:
		m.groupForm.path.SetValue(path)
		m.groupForm.path.CursorEnd()
		m.groupForm.pathAuto = false
	}
}

// commitPathSuggestion takes the highlighted directory as the field's answer
// and closes the listing. It is what enter does, against tab and → which both
// step into the directory and keep the walk open: with the directory the last
// thing the card asks for, the operator needs a key that ends the walk, or
// the enter meant to create the session only ever opens the next level down.
func (m *Model) commitPathSuggestion() {
	if path := m.pathSugg.selected(); path != "" {
		m.setPathField(path)
	}
	m.pathSugg.reset()
}

func (m *Model) applyPathSuggestion() {
	path := m.pathSugg.selected() + "/"
	m.setPathField(path)
	m.pathSugg.recompute(path)
}

// descendPath steps into the highlighted directory: it becomes the field's
// value, and the listing becomes what is inside it. A directory with no
// directories of its own is still a pick, so the walk stays open on it
// rather than closing over an empty listing.
func (m *Model) descendPath() {
	target := m.pathSugg.selected()
	if target == "" {
		return
	}
	m.setPathField(target + "/")
	m.pathSugg.relist(target, "")
}

// ascendPath steps out to the parent of the directory being browsed, landing
// on the directory just left so a wrong turn costs one keypress.
func (m *Model) ascendPath() {
	current := listingDir(m.pathFieldValue())
	if current == "" {
		return
	}
	parent := filepath.Dir(current)
	if parent == current {
		return
	}
	m.setPathField(parent + "/")
	m.pathSugg.relist(parent, current)
}
