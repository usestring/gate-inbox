// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const (
	// The CLI comes first because it is the only field that changes what
	// gets launched. Name, directory and prompt all decorate a session that
	// the tool has already decided the shape of, and opening on the name
	// asked for the one answer the operator most often does not have --
	// they let the agent name itself -- before the one they always do.
	fieldTool = iota
	// The model qualifies the CLI above it, so it sits with it rather than
	// among the fields that decorate a session the tool has already shaped.
	fieldModel
	fieldName
	fieldPrompt
	fieldGroup
	// The directory comes last because the group picker above it has already
	// answered it: the common path is to arrive here, see the right directory
	// and press enter. It is also the one field that opens a listing, and last
	// is the only place a listing can grow without pushing the fields under it
	// down the card -- the same reason the group card asks for its path last.
	fieldDir
	fieldCount
)

const (
	gfName = iota
	gfParent
	gfPath
	gfCount
)

type groupOption struct {
	path   string
	depth  int
	sessID string
	name   string
}

type form struct {
	name textinput.Model
	dir  textinput.Model
	// prompt is a composer rather than a plain textarea: a first task is
	// often a screenshot, so the box has to hold pasted images the way the
	// quick prompt does.
	prompt  composer
	dirAuto bool
	// toolFilter is the tool field's text entry. The picker is arrowed
	// through as before, but a CLI can also just be typed: the field is a
	// real input so filtering behaves like every other typed field rather
	// than like a bespoke type-ahead that swallows paste and unicode.
	toolFilter textinput.Model
	// model is free text: what a CLI accepts is the CLI's business, and a
	// picker here would need this program to track every vendor's names.
	model      textinput.Model
	toolNames  []string
	toolIndex  int
	groups     []groupOption
	groupIndex int
	focus      int
	// extra is the fields the extensions add, drawn after the form's own and
	// focused as fieldCount+i.
	extra []extraField
}

// extraField is one field an extension added to the form, and the option
// it is set to.
type extraField struct {
	field  extension.FormField
	choice int
}

func (f extraField) value() string { return f.field.Options[f.choice] }

// fieldTotal is how many fields the form moves through: its own, then the
// extensions'.
func (f form) fieldTotal() int { return fieldCount + len(f.extra) }

// focusedExtra is the extension field under the cursor, if it is on one.
func (f *form) focusedExtra() (*extraField, bool) {
	i := f.focus - fieldCount
	if i < 0 || i >= len(f.extra) {
		return nil, false
	}
	return &f.extra[i], true
}

// extraFields builds the extensions' fields at their defaults.
func extraFields(fields []extension.FormField) []extraField {
	extra := make([]extraField, 0, len(fields))
	for _, field := range fields {
		extra = append(extra, extraField{field: field, choice: max(0, slices.Index(field.Options, field.Default))})
	}
	return extra
}

// formValues is what the extensions' fields are set to, keyed as the
// extensions' hooks key them; nil when there are none.
func (f form) formValues() map[string]string {
	if len(f.extra) == 0 {
		return nil
	}
	values := make(map[string]string, len(f.extra))
	for _, e := range f.extra {
		values[e.field.Key] = e.value()
	}
	return values
}

// cycleExtra steps the focused extension field through its options,
// wrapping at the ends; a toggle's two options make that a flip.
func (m *Model) cycleExtra(delta int) bool {
	e, ok := m.form.focusedExtra()
	if !ok {
		return false
	}
	n := len(e.field.Options)
	e.choice = (e.choice + delta + n) % n
	return true
}

type groupForm struct {
	name     textinput.Model
	path     textinput.Model
	pathAuto bool
	focus    int
	// editing is the group this card is editing, empty while it is
	// creating one -- so submit knows whether to add a group or write
	// this one back over the group it opened on.
	editing string
}

// sessionLabel renders a session's identity for the tmux status bar.
func sessionLabel(group, name string) string {
	if group == "" {
		return name
	}
	return group + " · " + name
}

// resolveExistingDir turns raw field input into a usable directory:
// expand ~, fall back when empty, absolutize, and require it to exist.
// The resolved value returns either way so error messages can show it.
func resolveExistingDir(raw, fallback string) (string, bool) {
	dir := expandHome(strings.TrimSpace(raw))
	if dir == "" {
		dir = fallback
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return dir, isDir(dir)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func textField(placeholder string, limit int) textinput.Model {
	in := textinput.New()
	in.Placeholder = placeholder
	in.CharLimit = limit
	return in
}

const (
	// formLabelColumn is the columns before a field value: marker (2),
	// label (9), separator space (1).
	formLabelColumn = formMarkerWidth + formLabelWidth + 1
	// formMarkerWidth is the cursor mark ahead of a label, and
	// formLabelWidth the columns the label itself is padded to. Every card
	// fits its labels in nine.
	formMarkerWidth   = 2
	formLabelWidth    = 9
	formPromptMaxRows = 4
)

func promptField() composer {
	in := textarea.New()
	in.CharLimit = 2000
	in.Placeholder = "first task (optional)"
	in.ShowLineNumbers = false
	in.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "> "
		}
		return "  "
	})
	styles := in.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	in.SetStyles(styles)
	in.SetHeight(1)
	return composer{input: in, maxRows: formPromptMaxRows}
}

// formValueWidth is the columns a field value can occupy inside the card.
func (m *Model) formValueWidth() int {
	return cardInnerWidth(m.cardWidth()) - formLabelColumn
}

// syncFormFieldWidths fits the field widgets to the card so long values
// scroll (inputs) or wrap (prompt) instead of clipping at the card edge.
// Inputs reserve 3 columns: their "> " prompt plus the cursor cell that
// renders past the last character.
func (m *Model) syncFormFieldWidths() {
	inner := m.formValueWidth()
	m.form.name.SetWidth(inner - 3)
	m.form.dir.SetWidth(inner - 3)
	m.form.model.SetWidth(inner - 3)
	// textinput recomputes its scroll window only inside Update/SetValue/
	// SetCursor, so a width change alone would render a stale window until
	// the next keystroke.
	m.form.name.SetCursor(m.form.name.Position())
	m.form.dir.SetCursor(m.form.dir.Position())
	m.form.model.SetCursor(m.form.model.Position())
	m.form.prompt.input.SetWidth(inner)
	// The filter renders after the picker's arrows on the same row, so it
	// gets what the card has left rather than a fixed width that would push
	// the row past the border on a narrow terminal.
	m.form.toolFilter.SetWidth(max(4, min(20, inner-lipgloss.Width(m.selectedToolName())-8)))
	m.form.toolFilter.SetCursor(m.form.toolFilter.Position())
}

func (m *Model) syncGroupFormFieldWidths() {
	width := m.formValueWidth() - 3
	m.groupForm.name.SetWidth(width)
	m.groupForm.path.SetWidth(width)
	m.groupForm.name.SetCursor(m.groupForm.name.Position())
	m.groupForm.path.SetCursor(m.groupForm.path.Position())
}

// contextGroup is the group the cursor currently sits in: a highlighted
// group row itself, or the group holding a highlighted session.
func (m *Model) contextGroup() string {
	if entry, ok := m.selectedRow(); ok {
		if entry.isGroup {
			return entry.group
		}
		return entry.sess.Group
	}
	return ""
}

// ancestorGroupDir finds the closest configured default path walking up
// from the group to the root; empty when no ancestor has one.
func (m *Model) ancestorGroupDir(group string) string {
	for g := group; g != ""; g = parentGroup(g) {
		if p := m.groupPaths[g]; p != "" && isDir(p) {
			return p
		}
	}
	return ""
}

// groupDefaultDir resolves the working directory for a session in a group:
// the nearest inherited default path, else the current directory.
func (m *Model) groupDefaultDir(group string) string {
	if p := m.ancestorGroupDir(group); p != "" {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// toolDisplayOrder fixes the order tools appear in when creating a session and
// when cycling the quick-spawn tool. Tools outside this list follow, sorted
// alphabetically.
var toolDisplayOrder = []string{"claude", "codex", "opencode"}

// sortedToolNames is every configured agent CLI in picker order. A block
// declaring shell = true is not a CLI to spawn agents with, so it is left
// out; its own key launches it, and a rename still keeps a shell session
// on it.
func sortedToolNames(cfg config.Config) []string {
	names := make([]string, 0, len(cfg.Tools))
	for _, name := range cfg.ToolNames() {
		if !cfg.Tools[name].Shell {
			names = append(names, name)
		}
	}
	rank := make(map[string]int, len(toolDisplayOrder))
	for i, name := range toolDisplayOrder {
		rank[name] = i
	}
	sort.Slice(names, func(i, j int) bool {
		ri, iRanked := rank[names[i]]
		rj, jRanked := rank[names[j]]
		if iRanked && jRanked {
			return ri < rj
		}
		if iRanked != jRanked {
			return iRanked
		}
		return names[i] < names[j]
	})
	return names
}

// enabledToolNames is the create-session picker: configured tools minus any
// the user hid in settings. Existing sessions keep their tool even when hidden.
func (m *Model) enabledToolNames() []string {
	all := sortedToolNames(m.cfg)
	hidden := m.hiddenTools()
	if len(hidden) == 0 {
		return all
	}
	out := make([]string, 0, len(all))
	for _, name := range all {
		if !hidden[name] {
			out = append(out, name)
		}
	}
	return out
}

func (m *Model) openForm() {
	tools, toolIndex := m.defaultToolSelection()
	if len(tools) == 0 {
		m.errBar.text = "no CLIs enabled: open settings (s), then CLIs, to turn some on"
		return
	}

	// Not an example name: a name is the field nobody has to fill in, and one
	// that reads like a suggestion is how "my-session" ended up on rows.
	name := textField("optional: the agent names it", 60)

	dir := textField("", 400)
	prompt := promptField()
	prompt.gen = m.nextComposerGen()
	filter := toolFilterField()
	filter.Focus()

	m.form = form{
		name:       name,
		dir:        dir,
		prompt:     prompt,
		toolFilter: filter,
		dirAuto:    true,
		toolNames:  tools,
		toolIndex:  toolIndex,
		model:      textField("default", 60),
		focus:      fieldTool,
		extra:      extraFields(sessionhooks.FormFields()),
	}
	m.errBar.text = ""
	m.syncFormFieldWidths()
	m.rebuildGroupOptions(m.contextGroup())
	m.form.dir.SetValue(m.groupDefaultDir(m.selectedGroupPath()))
	m.pathSugg.reset()
	m.mode = modeForm
}

func (m *Model) selectedGroupPath() string {
	if m.form.groupIndex >= 0 && m.form.groupIndex < len(m.form.groups) {
		return m.form.groups[m.form.groupIndex].path
	}
	return ""
}

// selectParentOption builds the picker with a group's own parent selected.
// The picker leaves archived groups out, so an archived parent has to be
// put back: without it the picker would open on the root and saving would
// move the group there without having been asked to.
func (m *Model) selectParentOption(parent string) {
	m.rebuildGroupOptions(parent)
	if parent == "" || m.selectedGroupPath() == parent {
		return
	}
	m.form.groups = append(m.form.groups, groupOption{
		path:  parent,
		depth: strings.Count(parent, "/") + 1,
	})
	m.form.groupIndex = len(m.form.groups) - 1
}

// rebuildGroupOptions flattens the group tree into picker rows.
// Index 0 is always the root; selectPath moves the highlight when given.
func (m *Model) rebuildGroupOptions(selectPath string) {
	paths := groupClosure(m.groups, m.sessions)
	for path := range paths {
		if m.groupEffectivelyArchived(path) {
			delete(paths, path)
		}
	}
	children := childIndex(paths, m.groups)

	options := []groupOption{{path: "", depth: 0}}
	var walk func(path string, depth int)
	walk = func(path string, depth int) {
		options = append(options, groupOption{path: path, depth: depth})
		for _, child := range children[path] {
			walk(child, depth+1)
		}
	}
	for _, root := range children[""] {
		walk(root, 1)
	}

	m.form.groups = options
	m.form.groupIndex = 0
	for i, opt := range options {
		if selectPath != "" && opt.path == selectPath {
			m.form.groupIndex = i
			return
		}
	}
}

func (m *Model) handleFormKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	dirSuggesting := m.form.focus == fieldDir && m.pathSugg.active()
	dirCapturing := m.form.focus == fieldDir && m.pathSugg.capturing()
	switch msg.String() {
	case "esc":
		if dirCapturing {
			m.pathSugg.reset()
			return m, nil
		}
		// The form is gone, and with it the only text naming the images it
		// was holding.
		m.form.prompt.release()
		return m.cancelSpawnToGate()
	case "tab":
		if dirCapturing {
			m.applyPathSuggestion()
			return m, nil
		}
		m.formFocus(1)
		return m, nil
	case "shift+tab":
		m.formFocus(-1)
		return m, nil
	case "up":
		if dirSuggesting {
			if !m.pathSugg.move(-1) {
				m.formFocus(-1)
			}
		} else {
			m.formFocus(-1)
		}
		return m, nil
	case "down":
		if dirSuggesting {
			if !m.pathSugg.move(1) {
				m.formFocus(1)
			}
		} else {
			m.formFocus(1)
		}
		return m, nil
	case "left":
		if m.form.focus == fieldDir && m.pathSugg.browsing {
			m.ascendPath()
			return m, nil
		}
		if m.form.focus == fieldTool {
			m.cycleTool(-1)
			return m, nil
		}
		if m.form.focus == fieldGroup {
			m.moveGroupCursor(-1)
			return m, nil
		}
		if m.cycleExtra(-1) {
			return m, nil
		}
	case "right":
		if m.form.focus == fieldDir && m.pathSugg.browsing {
			m.descendPath()
			return m, nil
		}
		if m.form.focus == fieldTool {
			m.cycleTool(1)
			return m, nil
		}
		if m.form.focus == fieldGroup {
			m.moveGroupCursor(1)
			return m, nil
		}
		if m.cycleExtra(1) {
			return m, nil
		}
	case "space":
		if m.cycleExtra(1) {
			return m, nil
		}
	case "enter":
		if dirSuggesting && m.pathSugg.chosen {
			m.commitPathSuggestion()
			return m, nil
		}
		return m.submitForm()
	}

	if m.form.focus == fieldPrompt {
		if cmd, handled := m.composerKey(composerForm, msg); handled {
			return m, cmd
		}
	}

	var cmd tea.Cmd
	switch m.form.focus {
	case fieldTool:
		m.form.toolFilter, cmd = m.form.toolFilter.Update(msg)
		m.snapToolToFilter()
	case fieldModel:
		m.form.model, cmd = m.form.model.Update(msg)
	case fieldName:
		m.form.name, cmd = m.form.name.Update(msg)
	case fieldDir:
		m.form.dir, cmd = m.form.dir.Update(msg)
		m.form.dirAuto = false
		m.pathSugg.recompute(m.form.dir.Value())
	case fieldPrompt:
		cmd = m.form.prompt.typeKey(msg)
	}
	return m, cmd
}

// moveGroupCursor moves within the expanded group picker, wrapping at the
// ends; a delta of 0 re-resolves the dependent defaults in place.
func (m *Model) moveGroupCursor(delta int) {
	count := len(m.form.groups)
	if count == 0 {
		return
	}
	m.form.groupIndex = (m.form.groupIndex + delta + count) % count
	if m.mode == modeForm && m.form.dirAuto {
		m.form.dir.SetValue(m.groupDefaultDir(m.selectedGroupPath()))
	}
	if m.mode == modeGroupForm && m.groupForm.pathAuto {
		m.groupForm.path.SetValue(m.ancestorGroupDir(m.selectedGroupPath()))
	}
}

func (m *Model) formFocus(delta int) {
	m.pathSugg.reset()
	total := m.form.fieldTotal()
	m.form.focus = (m.form.focus + delta + total) % total
	m.form.name.Blur()
	m.form.dir.Blur()
	m.form.prompt.input.Blur()
	m.form.toolFilter.Blur()
	m.form.model.Blur()
	// The filter has already done its work -- the selection snapped to it --
	// so it is dropped on the way out rather than left as text the card no
	// longer draws. Otherwise a filter typed and tabbed past would sit
	// invisible on the field and still be the thing submit refuses over.
	m.form.toolFilter.SetValue("")
	switch m.form.focus {
	case fieldTool:
		m.form.toolFilter.Focus()
	case fieldModel:
		m.form.model.Focus()
	case fieldName:
		m.form.name.Focus()
	case fieldDir:
		m.form.dir.Focus()
		m.pathSugg.browse(m.form.dir.Value())
	case fieldPrompt:
		m.form.prompt.input.Focus()
	}
}

// toolFilterField is the tool picker's typing box. It carries no prompt
// because it renders inline after the picker's own arrows, and no
// placeholder, because an empty filter is the normal state rather than an
// unfilled one.
func toolFilterField() textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 40
	in.SetWidth(20)
	return in
}

// matchTools narrows the picker to the CLIs the typed text names. Prefix
// matches come first so "co" lands on codex rather than on opencode, which
// only contains those letters; an empty filter matches everything, in the
// configured display order.
func matchTools(names []string, filter string) []string {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return names
	}
	var prefix, contains []string
	for _, name := range names {
		lower := strings.ToLower(name)
		switch {
		case strings.HasPrefix(lower, filter):
			prefix = append(prefix, name)
		case strings.Contains(lower, filter):
			contains = append(contains, name)
		}
	}
	return append(prefix, contains...)
}

func (m *Model) formToolMatches() []string {
	return matchTools(m.form.toolNames, m.form.toolFilter.Value())
}

// selectedToolName is the CLI the form would launch, empty only when nothing
// is configured.
func (m *Model) selectedToolName() string {
	if m.form.toolIndex >= 0 && m.form.toolIndex < len(m.form.toolNames) {
		return m.form.toolNames[m.form.toolIndex]
	}
	return ""
}

func (m *Model) selectTool(name string) {
	for i, candidate := range m.form.toolNames {
		if candidate == name {
			m.form.toolIndex = i
			return
		}
	}
}

// snapToolToFilter keeps the selection on the best match for what has been
// typed, so enter creates against the CLI the field is showing without the
// operator confirming the filter first.
//
// Two states deliberately leave the selection alone. A filter that matches
// nothing would otherwise silently launch something the operator never named;
// the field says there is no match instead, and submit refuses. And a filter
// backspaced empty means they gave up on typing, not that they asked for the
// first CLI in the list -- snapping there would throw away both the settings
// default the card opened on and anything they had arrowed to.
func (m *Model) snapToolToFilter() {
	if strings.TrimSpace(m.form.toolFilter.Value()) == "" {
		return
	}
	if matches := m.formToolMatches(); len(matches) > 0 {
		m.selectTool(matches[0])
	}
}

// cycleTool steps through the CLIs the filter allows, wrapping at the ends.
func (m *Model) cycleTool(delta int) {
	matches := m.formToolMatches()
	if len(matches) == 0 {
		return
	}
	at := 0
	current := m.selectedToolName()
	for i, name := range matches {
		if name == current {
			at = i
		}
	}
	m.selectTool(matches[(at+delta+len(matches))%len(matches)])
}

// formSpawnDir is the directory the form would launch in, resolved the
// same way submit resolves it.
func (m *Model) formSpawnDir() string {
	cwd, _ := os.Getwd()
	dir, _ := resolveExistingDir(m.form.dir.Value(), cwd)
	return dir
}

func (m *Model) submitForm() (tea.Model, tea.Cmd) {
	if len(m.form.toolNames) == 0 {
		m.errBar.text = "no tools configured"
		return m.cancelSpawnToGate()
	}
	if len(m.formToolMatches()) == 0 {
		m.errBar.text = "no CLI matches " + strings.TrimSpace(m.form.toolFilter.Value())
		return m, nil
	}
	toolName := m.selectedToolName()
	if m.form.prompt.pasting() {
		m.errBar.text = "still reading the pasted image - try again in a moment"
		return m, nil
	}

	name := strings.TrimSpace(m.form.name.Value())
	autoNamed := name == ""
	if autoNamed {
		name = toolName + "-" + newID()[:4]
	}
	cwd, _ := os.Getwd()
	dir, ok := resolveExistingDir(m.form.dir.Value(), cwd)
	if !ok {
		m.errBar.text = "working directory does not exist: " + dir
		return m, nil
	}
	group := m.selectedGroupPath()
	// Chips become the paths of the images they stand for, so a first task
	// reaches the agent with its screenshot named where it was pasted.
	prompt := m.form.prompt.message()
	if strings.HasPrefix(prompt, "-") {
		m.errBar.text = `prompt cannot start with "-": the tool would read it as a flag`
		return m, nil
	}

	values := m.form.formValues()
	id, err := m.spawnSessionWith(toolName, m.formModel(), name, dir, group, prompt, autoNamed, store.SourceUser, values)
	if err != nil {
		m.reportLaunchError(err)
		// A spawn the hint dialog refused takes the form off screen with it,
		// so its images go the way esc sends them. An error reported in the
		// bar leaves the form up, and the prompt still names them.
		if m.mode == modeLaunchHint {
			m.form.prompt.release()
		}
		return m, nil
	}
	// New sessions start as starting, which attention excludes; clear so
	// the row the form just created is on screen.
	m.statusFilter = statusFilterAll
	// Set before focusing, not instead of it: this is where a focus that
	// refuses -- a row filtered off the tree, a pane already gone -- leaves
	// the operator, and it must not be the form.
	m.mode = modeList
	// The extensions hear of it before the board focuses the new pane, so
	// one that opens a view of its own on the session gets there first,
	// and the board leaves the operator in that view instead.
	if m.formSpawned(id, values) {
		m.rebuildRows()
		m.focusSession(id)
		return m, m.refreshCmd()
	}
	return m.landInNewSession(id)
}

// formSpawned tells the extensions that id was spawned from the form with
// values, and reports whether one of them opened a view of its own while
// it was told.
func (m *Model) formSpawned(id string, values map[string]string) bool {
	if len(values) == 0 {
		return false
	}
	for _, sess := range m.sessions {
		if sess.ID == id {
			before := m.extensionViewsOpened()
			sessionhooks.FormSpawned(sess, values)
			return m.extensionViewsOpened() != before
		}
	}
	return false
}

// extensionViewsOpened counts the views extensions have opened: those noted
// through noteExtensionViewOpened, and every UIHost.Open the bridge took,
// counted when it was called rather than when the view reaches the board.
func (m *Model) extensionViewsOpened() uint64 {
	n := m.extensionViews
	if m.extBridge != nil {
		n += uint64(m.extBridge.opened())
	}
	return n
}

// noteExtensionViewOpened records that an extension opened a view of its
// own on the board. Whatever lets an extension open a view calls it, so a
// spawn from the form knows to leave the operator in that view rather than
// focus the new pane over it.
func (m *Model) noteExtensionViewOpened() { m.extensionViews++ }

// spawnSession creates the tmux session and its store record for both
// the New Session form and quick spawn. autoNamed marks sessions whose
// name is a generated placeholder; those are asked to rename once.
// Custom-named sessions only get a short note that rename is available later.
func (m *Model) spawnSession(toolName, name, dir, group, prompt string, autoNamed bool) error {
	_, err := m.spawnSessionAs(toolName, "", name, dir, group, prompt, autoNamed, store.SourceUser)
	return err
}

// spawnSessionAs is spawnSession with the provenance of the name spelled out,
// and the id of what it created. A name the manager derived rather than took
// from anybody is the one kind the naming pass may replace, so the instant
// spawn -- whose name is a stand-in until the agent's own conversation title
// lands -- is the caller that says so.
// formModel is the model the card asks for, empty for the CLI's default.
func (m *Model) formModel() string { return strings.TrimSpace(m.form.model.Value()) }

func (m *Model) spawnSessionAs(toolName, model, name, dir, group, prompt string, autoNamed bool, nameSource string) (string, error) {
	return m.spawnSessionWith(toolName, model, name, dir, group, prompt, autoNamed, nameSource, nil)
}

// spawnSessionWith is spawnSessionAs carrying the values of the extensions'
// form fields, which only the new-session form has.
func (m *Model) spawnSessionWith(toolName, model, name, dir, group, prompt string, autoNamed bool, nameSource string, form map[string]string) (string, error) {
	tool := m.cfg.Tools[toolName]
	id := newID()
	account, err := accounts.Select(m.store, tool, "", id)
	if err != nil {
		return "", err
	}
	plan, err := launch.Assemble(toolName, tool, prompt, "", autoNamed, model, account)
	if err != nil {
		return "", err
	}
	if err := m.launchNewSessionWith(store.Session{
		ID:         id,
		Name:       name,
		Tool:       toolName,
		Cwd:        dir,
		Group:      group,
		NameSource: nameSource,
		Model:      plan.Model,
		Account:    plan.Account,
		// Starting until the agent first draws to its pane, so the row shows
		// a launch state immediately; the poller flips it to the real status.
		Status:         status.Starting,
		AgentSessionID: plan.AgentSessionID,
		PendingInputs:  plan.PendingInputs,
		LaunchPrompt:   plan.LaunchPrompt,
	}, tool, plan.Command, form); err != nil {
		return "", err
	}
	// The CLI is remembered once the launch has actually happened, so a
	// spawn that failed does not move the box the next one opens on.
	m.rememberTool(toolName)
	// The directive went out with the launch, so the row waits for the name
	// the agent picks instead of showing the one generated for it.
	if autoNamed {
		if m.awaitedRenames == nil {
			m.awaitedRenames = map[string]awaitedRename{}
		}
		m.awaitedRenames[id] = awaitedRename{generated: name, prompt: prompt}
	}
	return id, nil
}

func (m *Model) buildLaunch(toolName string, tool config.Tool, baseCommand, id, model, account string, contributed map[string]string) (string, map[string]string, error) {
	return launch.Environment(m.hooks, toolName, tool, baseCommand, id, model, account, contributed)
}

func (m *Model) openGroupForm() {
	name := textField("group-name", 60)
	name.Focus()
	m.groupForm = groupForm{
		name:     name,
		path:     textField("default working directory", 400),
		pathAuto: true,
		focus:    gfName,
	}
	m.rebuildGroupOptions(m.contextGroup())
	m.groupForm.path.SetValue(m.groupDefaultDir(m.selectedGroupPath()))
	m.syncGroupFormFieldWidths()
	m.pathSugg.reset()
	m.mode = modeGroupForm
	m.errBar.text = ""
}

// openGroupEditForm opens the group card on a group that already exists.
// The path field carries the group's own default rather than an inherited
// one, and the parent picker drops the group's own subtree: a group cannot
// be filed under itself.
func (m *Model) openGroupEditForm(group string) {
	name := textField("group-name", 60)
	name.SetValue(baseName(group))
	name.CursorEnd()
	name.Focus()
	path := textField("default working directory", 400)
	value := m.groupPaths[group]
	if value == "" {
		value = m.groupDefaultDir(group)
	}
	path.SetValue(value)
	path.CursorEnd()
	// pathAuto stays false: the value is the group's own, so re-picking a
	// parent must not overwrite it with that parent's default.
	m.groupForm = groupForm{
		name:    name,
		path:    path,
		focus:   gfName,
		editing: group,
	}
	m.selectParentOption(parentGroup(group))
	m.pruneMoveTargets(group)
	m.syncGroupFormFieldWidths()
	m.pathSugg.reset()
	m.mode = modeGroupForm
	m.errBar.text = ""
}

func (m *Model) handleGroupFormKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pathSuggesting := m.groupForm.focus == gfPath && m.pathSugg.active()
	pathCapturing := m.groupForm.focus == gfPath && m.pathSugg.capturing()
	switch msg.String() {
	case "esc":
		if pathCapturing {
			m.pathSugg.reset()
			return m, nil
		}
		m.mode = modeList
		return m, nil
	case "tab":
		if pathCapturing {
			m.applyPathSuggestion()
			return m, nil
		}
		m.groupFormFocus(1)
		return m, nil
	case "shift+tab":
		m.groupFormFocus(-1)
		return m, nil
	case "up":
		if pathSuggesting {
			if !m.pathSugg.move(-1) {
				m.groupFormFocus(-1)
			}
		} else {
			m.groupFormFocus(-1)
		}
		return m, nil
	case "down":
		if pathSuggesting {
			if !m.pathSugg.move(1) {
				m.groupFormFocus(1)
			}
		} else {
			m.groupFormFocus(1)
		}
		return m, nil
	case "left":
		if m.groupForm.focus == gfPath && m.pathSugg.browsing {
			m.ascendPath()
			return m, nil
		}
		if m.groupForm.focus == gfParent {
			m.moveGroupCursor(-1)
			return m, nil
		}
	case "right":
		if m.groupForm.focus == gfPath && m.pathSugg.browsing {
			m.descendPath()
			return m, nil
		}
		if m.groupForm.focus == gfParent {
			m.moveGroupCursor(1)
			return m, nil
		}
	case "enter":
		if pathSuggesting && m.pathSugg.chosen {
			m.commitPathSuggestion()
			return m, nil
		}
		return m.submitGroupForm()
	}

	var cmd tea.Cmd
	switch m.groupForm.focus {
	case gfName:
		m.groupForm.name, cmd = m.groupForm.name.Update(msg)
	case gfPath:
		m.groupForm.path, cmd = m.groupForm.path.Update(msg)
		m.groupForm.pathAuto = false
		m.pathSugg.recompute(m.groupForm.path.Value())
	}
	return m, cmd
}

func (m *Model) groupFormFocus(delta int) {
	m.pathSugg.reset()
	m.groupForm.focus = (m.groupForm.focus + delta + gfCount) % gfCount
	m.groupForm.name.Blur()
	m.groupForm.path.Blur()
	switch m.groupForm.focus {
	case gfName:
		m.groupForm.name.Focus()
	case gfPath:
		m.groupForm.path.Focus()
		m.pathSugg.browse(m.groupForm.path.Value())
	}
}

func (m *Model) submitGroupForm() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.groupForm.name.Value())
	name = strings.ReplaceAll(name, "/", "-")
	if name == "" {
		m.errBar.text = "group name cannot be empty"
		return m, nil
	}
	parent := m.selectedGroupPath()
	full := name
	if parent != "" {
		full = parent + "/" + name
	}
	path, ok := resolveExistingDir(m.groupForm.path.Value(), m.groupDefaultDir(parent))
	if !ok {
		m.errBar.text = "default path does not exist: " + path
		return m, nil
	}
	if old := m.groupForm.editing; old != "" {
		return m.saveGroupEdit(old, full, path)
	}
	if err := m.store.AddGroup(full, path); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	m.materializeGroupsLocal([]string{full})
	if m.groupPaths == nil {
		m.groupPaths = map[string]string{}
	}
	m.groupPaths[full] = path
	for group := parent; group != ""; group = parentGroup(group) {
		delete(m.collapsed, group)
	}
	m.persistCollapsed()
	m.search = ""
	m.searching = false
	m.showArchived = false
	m.hideEmptyGroups = false
	m.statusFilter = statusFilterAll
	m.errBar.text = ""
	m.mode = modeList
	m.rebuildRows()
	m.focusGroupRow(full)
	return m, m.refreshCmd()
}

// saveGroupEdit writes the card back onto a group that already exists. A
// changed name or parent moves the whole subtree, and the default path is
// set on wherever it lands.
func (m *Model) saveGroupEdit(old, full, path string) (tea.Model, tea.Cmd) {
	if full != old {
		if err := m.store.RenameGroup(old, full); err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
	}
	// CreateGroup upserts, so it doubles as the default-path setter.
	if err := m.store.CreateGroup(full, path); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	m.renameGroupLocally(old, full, path)
	m.relabelSubtree(full)
	// A group moved under a folded parent would otherwise vanish from the
	// list the moment it was saved.
	for group := parentGroup(full); group != ""; group = parentGroup(group) {
		delete(m.collapsed, group)
	}
	m.persistCollapsed()
	m.errBar.text = ""
	m.mode = modeList
	m.rebuildRows()
	m.focusGroupRow(full)
	return m, m.refreshCmd()
}

// focusGroupRow puts the cursor back on a group after the tree is rebuilt
// around it, so the row the card was about stays the selected one.
func (m *Model) focusGroupRow(group string) {
	for i, row := range m.rows {
		if row.isGroup && row.group == group {
			m.cursor = i
			return
		}
	}
}
