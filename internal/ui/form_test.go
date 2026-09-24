// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestNewSessionFormUsesSettingsDefaultTool(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting("default_tool", "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.openForm()
	if got := m.form.toolNames[m.form.toolIndex]; got != "ready-tool" {
		t.Fatalf("new session tool = %q, want settings default", got)
	}
}

func TestNewSessionPreselectsContextGroup(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("alpha/beta", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "seed", dir, "alpha/beta")

	// cursor on the session inside alpha/beta
	m.selectSessionRow(t, "seed")
	m.openForm()
	if got := m.form.groups[m.form.groupIndex].path; got != "alpha/beta" {
		t.Fatalf("form should preselect session's group, got %q", got)
	}
	m.mode = modeList

	// cursor on a group row
	for i, r := range m.rows {
		if r.isGroup && r.group == "alpha" {
			m.cursor = i
		}
	}
	m.openForm()
	if got := m.form.groups[m.form.groupIndex].path; got != "alpha" {
		t.Fatalf("form should preselect the highlighted group, got %q", got)
	}
}

func TestGroupFormCreatesUnderParent(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("projects", ""); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())

	m.openGroupForm()
	pickGroup(t, m, "projects")
	m.groupForm.name.SetValue("sub/one")
	m.groupForm.path.SetValue(t.TempDir())
	_, cmd := m.submitGroupForm()
	if m.mode != modeList {
		t.Fatalf("group form should close, err=%q", m.errBar.text)
	}
	m.applyCmd(t, cmd)

	groups, _ := m.store.Groups()
	found := ""
	for _, g := range groups {
		if strings.HasSuffix(g.Name, "sub-one") {
			found = g.Name
		}
	}
	if found != "projects/sub-one" {
		t.Fatalf("slash should be sanitized and nested under parent, got %q", found)
	}
}

func TestGroupFormShowsNewEmptyGroup(t *testing.T) {
	m := buildModel(t)
	m.hideEmptyGroups = true
	m.search = "does-not-match"
	m.showArchived = true
	m.statusFilter = statusFilterAttention
	m.openGroupForm()
	m.groupForm.name.SetValue("manual")
	m.groupForm.path.SetValue(t.TempDir())

	_, cmd := m.submitGroupForm()
	if m.hideEmptyGroups {
		t.Fatal("creating a group should reveal it when empty groups were hidden")
	}
	if m.search != "" || m.showArchived || m.statusFilter.active() {
		t.Fatalf("creation left list filters active: search=%q archived=%v statusFilter=%v",
			m.search, m.showArchived, m.statusFilter)
	}
	if got := m.groupRowPaths(); !reflect.DeepEqual(got, []string{"manual"}) {
		t.Fatalf("group rows before refresh = %v, want [manual]", got)
	}
	if row, ok := m.selectedRow(); !ok || !row.isGroup || row.group != "manual" {
		t.Fatalf("new group is not selected: %+v", row)
	}

	m.applyCmd(t, cmd)
	if got := m.groupRowPaths(); !reflect.DeepEqual(got, []string{"manual"}) {
		t.Fatalf("group rows after refresh = %v, want [manual]", got)
	}
}

func TestGroupFormExpandsParentToShowNewChild(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("projects", t.TempDir()); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "projects")
	m.collapsed["projects"] = true
	m.openGroupForm()
	m.groupForm.name.SetValue("api")

	_, _ = m.submitGroupForm()
	if m.collapsed["projects"] {
		t.Fatal("parent remained collapsed after creating a child")
	}
	if got := m.groupRowPaths(); !reflect.DeepEqual(got, []string{"projects", "projects/api"}) {
		t.Fatalf("group rows = %v, want parent and child", got)
	}
	if row, ok := m.selectedRow(); !ok || row.group != "projects/api" {
		t.Fatalf("new child is not selected: %+v", row)
	}
}

func TestGroupFormRejectsDuplicateWithoutChangingIt(t *testing.T) {
	m := buildModel(t)
	first := t.TempDir()
	second := t.TempDir()
	if err := m.store.AddGroup("backend", first); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())

	m.openGroupForm()
	pickGroup(t, m, "")
	m.groupForm.name.SetValue("backend")
	m.groupForm.path.SetValue(second)
	_, cmd := m.submitGroupForm()
	if cmd != nil || m.mode != modeGroupForm || !strings.Contains(m.errBar.text, "already exists") {
		t.Fatalf("duplicate submission succeeded: mode=%v err=%q", m.mode, m.errBar.text)
	}

	groups, err := m.store.Groups()
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	if len(groups) != 1 || groups[0].Path != first {
		t.Fatalf("duplicate submission changed group: %+v", groups)
	}
}

func TestGroupParentPickerExcludesArchivedGroups(t *testing.T) {
	m := buildModel(t)
	for _, group := range []string{"active", "archived", "archived/child"} {
		if err := m.store.CreateGroup(group, t.TempDir()); err != nil {
			t.Fatalf("create %s: %v", group, err)
		}
	}
	if err := m.store.SetGroupArchived("archived", true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())

	m.openGroupForm()
	var options []string
	for _, option := range m.form.groups {
		options = append(options, option.path)
	}
	if !reflect.DeepEqual(options, []string{"", "active"}) {
		t.Fatalf("parent options = %v, want root and active", options)
	}
}

func TestGroupFormFieldsTrackCardWidth(t *testing.T) {
	m := buildModel(t)
	m.openGroupForm()
	want := m.formValueWidth() - 3
	if m.groupForm.name.Width() != want || m.groupForm.path.Width() != want {
		t.Fatalf("initial widths = name %d path %d, want %d", m.groupForm.name.Width(), m.groupForm.path.Width(), want)
	}

	m.Update(tea.WindowSizeMsg{Width: 72, Height: m.height})
	want = m.formValueWidth() - 3
	if m.groupForm.name.Width() != want || m.groupForm.path.Width() != want {
		t.Fatalf("resized widths = name %d path %d, want %d", m.groupForm.name.Width(), m.groupForm.path.Width(), want)
	}
}

func TestGroupDefaultPathFillsSessionDir(t *testing.T) {
	m := buildModel(t)
	groupDir := t.TempDir()
	if err := m.store.CreateGroup("workspace", groupDir); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())

	m.openForm()
	pickGroup(t, m, "workspace")
	m.moveGroupCursor(0) // re-resolve dir for the selected group
	if got := m.form.dir.Value(); got != groupDir {
		t.Fatalf("session dir should default to the group path %q, got %q", groupDir, got)
	}
}

func TestFormPromptComposesWithSettings(t *testing.T) {
	m := buildModel(t)
	tool := m.cfg.Tools["claude-hooked"]

	command, _, err := m.buildLaunch("claude", tool, launch.WithPrompt(tool, tool.Command, "fix the bug"), "prompt01", "", "", nil)
	if err != nil {
		t.Fatalf("buildLaunch: %v", err)
	}
	if !strings.HasPrefix(command, tool.Command+" 'fix the bug' --mcp-config '") || !strings.Contains(command, "--settings '") {
		t.Fatalf("command = %q", command)
	}
}

func TestFormLongDirKeepsCursorEndVisible(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	focusFormField(t, m, fieldDir, "fieldDir")
	m.form.dir.SetValue("/very/long/" + strings.Repeat("a", 80) + "/tail-end")
	m.form.dir.CursorEnd()
	view := ansi.Strip(m.viewForm())
	if !strings.Contains(view, "tail-end") {
		t.Fatal("dir field should scroll so the end of a long value stays visible")
	}
}

// focusFormPrompt moves the form's focus to the prompt field, where the
// image keys apply.
func focusFormPrompt(t *testing.T, m *Model) {
	t.Helper()
	focusFormField(t, m, fieldPrompt, "fieldPrompt")
}

// focusFormField walks the card's focus to the field a test wants, by name
// rather than by a count of steps. Counting steps hard-codes the card's field
// order into every test that types into it, so adding a row further down the
// card broke seven tests that had nothing to do with it.
func focusFormField(t *testing.T, m *Model, field int, name string) {
	t.Helper()
	for i := 0; i < fieldCount; i++ {
		if m.form.focus == field {
			return
		}
		m.formFocus(1)
	}
	t.Fatalf("focus = %v, want %s", m.form.focus, name)
}

// pasteFormImage runs a full ctrl+v against a fake clipboard that yields
// the given file, and returns the id of the chip it left behind.
func pasteFormImage(t *testing.T, m *Model, path string) int {
	t.Helper()
	orig := captureClipboardImage
	defer func() { captureClipboardImage = orig }()
	captureClipboardImage = func() (string, error) { return path, nil }

	_, cmd := m.handleFormKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+v should start an async clipboard read")
	}
	id := m.form.prompt.lastImageID
	msg, ok := cmd().(pasteImageMsg)
	if !ok {
		t.Fatalf("clipboard cmd returned %T", cmd())
	}
	updated, _ := m.Update(msg)
	*m = *updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("paste: %q", m.errBar.text)
	}
	return id
}

func TestFormPasteSendsTheImagePathWithTheFirstTask(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	focusFormPrompt(t, m)
	for _, r := range "match this" {
		m.handleFormKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	path := tempImage(t, "mock.png")
	id := pasteFormImage(t, m, path)

	if got, want := m.form.prompt.input.Value(), "match this "+imageToken(id)+" "; got != want {
		t.Fatalf("chip should land at the caret: got %q, want %q", got, want)
	}
	if got, want := m.form.prompt.message(), "match this "+path; got != want {
		t.Fatalf("first task = %q, want %q", got, want)
	}
}

func TestFormPasteChipDeletesAsOneAndReleasesItsImage(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	focusFormPrompt(t, m)

	path := tempImage(t, "mock.png")
	pasteFormImage(t, m, path)

	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := m.form.prompt.input.Value(); got != "" {
		t.Fatalf("backspace next to a chip should take the whole chip: %q", got)
	}
	if len(m.form.prompt.attachments) != 0 {
		t.Fatalf("attachment should be released: %+v", m.form.prompt.attachments)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the image file should be gone, stat err = %v", err)
	}
}

func TestFormCancelReleasesPastedImages(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	focusFormPrompt(t, m)

	path := tempImage(t, "mock.png")
	pasteFormImage(t, m, path)

	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.mode != modeList {
		t.Fatalf("esc should close the form, mode = %v", m.mode)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an abandoned form should release its images, stat err = %v", err)
	}
}

// The rule that keeps submitForm from releasing: the agent opens the path
// after it launches, so a created session's images have to outlive the
// form that named them. The sweep is what takes them, days later.
func TestFormSubmitKeepsThePastedImage(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	focusFormPrompt(t, m)
	m.form.name.SetValue("with-a-picture")
	m.form.dir.SetValue(t.TempDir())

	path := tempImage(t, "mock.png")
	id := pasteFormImage(t, m, path)

	if _, _ = m.submitForm(); m.errBar.text != "" {
		t.Fatalf("submit: %q", m.errBar.text)
	}
	// The form closes and the create lands inside the session it made; this
	// test is about the image the prompt carried, so it steps back out.
	if m.mode != modeFocus {
		t.Fatalf("a created session should land in its pane, mode = %v", m.mode)
	}
	m.leaveFocusForFixture(t)
	if len(m.sessionRows()) != 1 {
		t.Fatalf("want one session, got %v", sessionNames(m))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the agent still has to open this file: %v", err)
	}
	// And the path is what the session launched with, not the chip's text.
	if strings.Contains(m.form.prompt.message(), imageToken(id)) {
		t.Fatalf("the chip should have become its path: %q", m.form.prompt.message())
	}
}

func TestFormChipRendersInTheCard(t *testing.T) {

	m := buildModel(t)
	m.openForm()
	focusFormPrompt(t, m)
	m.form.prompt.attachments = []imageAttachment{{id: 1, path: "/tmp/gate-inbox-pastes/paste-123.png"}}
	m.form.prompt.input.SetValue("match " + imageToken(1))

	view := m.viewForm()
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "match "+imageToken(1)) {
		t.Fatalf("chip should read inline with the first task, got %q", plain)
	}
	if strings.Contains(plain, "paste-123") {
		t.Fatalf("the card should not show the temp path, got %q", plain)
	}
	// Only the opening escape is guaranteed to survive embedding byte for
	// byte: a painted row rewrites a fastStyle render's own trailing reset
	// to keep its background fill going past the chip.
	chipOpen := strings.TrimSuffix(imageChip(imageToken(1)), imageChipStyle.sfx)
	if !strings.Contains(view, chipOpen) {
		t.Fatal("chip should be styled in the rendered card")
	}
	if !strings.Contains(plain, "paste an image") {
		t.Fatalf("the focused prompt should say how to attach one, got %q", plain)
	}
}

func TestFormSubmitWaitsForAPasteStillReading(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	focusFormPrompt(t, m)
	m.form.name.SetValue("half-pasted")
	m.form.dir.SetValue(t.TempDir())
	// The chip a paste reserves before its clipboard read lands.
	m.form.prompt.attachments = []imageAttachment{{id: 1}}
	m.form.prompt.input.SetValue(imageToken(1))

	if _, _ = m.submitForm(); m.errBar.text == "" {
		t.Fatal("a submit with a chip still reading should be refused")
	}
	if m.mode != modeForm {
		t.Fatalf("form should stay open, mode = %v", m.mode)
	}
	if len(m.sessionRows()) != 0 {
		t.Fatalf("no session should be created, got %d", len(m.sessionRows()))
	}
}

func TestFormLongPromptWrapsAcrossRows(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	focusFormPrompt(t, m)
	m.form.prompt.input.SetValue(strings.Repeat("word ", 25) + "finale")
	view := ansi.Strip(m.viewForm())
	if !strings.Contains(view, "finale") {
		t.Fatal("long prompt should wrap onto more rows instead of being clipped")
	}
}

func TestTextareaRowsCountsExactMultipleWrap(t *testing.T) {
	in := promptField().input
	in.SetWidth(12) // content width 10
	in.SetValue("1234567890\nx")
	if rows := textareaRows(in, 10, 5); rows != 3 {
		t.Fatalf("a line filling its row exactly adds a wrap row: want 3, got %d", rows)
	}
}

func TestFormGroupArrowsMoveFocusNotSelection(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("alpha", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.openForm()
	focusFormField(t, m, fieldGroup, "fieldGroup")

	initial := m.form.groupIndex
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.form.groupIndex != initial {
		t.Fatalf("down must not change the group selection, index = %d, want %d", m.form.groupIndex, initial)
	}
	if m.form.focus == fieldGroup {
		t.Fatal("down should move focus off the group field")
	}

	m.form.focus = fieldGroup
	m.form.groupIndex = 0
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.form.groupIndex != 1 {
		t.Fatalf("right should cycle the group selection, index = %d", m.form.groupIndex)
	}
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.form.groupIndex != 0 {
		t.Fatalf("left should cycle back, index = %d", m.form.groupIndex)
	}
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.form.groupIndex != len(m.form.groups)-1 {
		t.Fatalf("left at the first group should wrap to the last, index = %d", m.form.groupIndex)
	}
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.form.groupIndex != 0 {
		t.Fatalf("right at the last group should wrap to the first, index = %d", m.form.groupIndex)
	}
}

func TestGroupFormParentArrowsMoveFocusNotSelection(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("alpha", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.openGroupForm()
	m.groupForm.focus = gfParent

	initial := m.form.groupIndex
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.form.groupIndex != initial {
		t.Fatalf("down must not change the parent selection, index = %d, want %d", m.form.groupIndex, initial)
	}
	if m.groupForm.focus == gfParent {
		t.Fatal("down should move focus off the parent field")
	}

	m.groupForm.focus = gfParent
	m.form.groupIndex = 0
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.form.groupIndex != 1 {
		t.Fatalf("right should cycle the parent selection, index = %d", m.form.groupIndex)
	}
}

func TestFormRejectsDashLeadingPrompt(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	m.form.name.SetValue("flagged")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = 0
	m.form.prompt.input.SetValue("--version")

	if _, _ = m.submitForm(); m.errBar.text == "" {
		t.Fatal("dash-leading prompt should be rejected")
	}
	if m.mode != modeForm {
		t.Fatalf("form should stay open, mode = %v", m.mode)
	}
	if len(m.sessionRows()) != 0 {
		t.Fatalf("no session should be created, got %d", len(m.sessionRows()))
	}
}

// Only a spawn that hands over the rename directive has a name to wait for;
// one the user named itself is already at its final name.
func TestSpawnAwaitsARenameOnlyWhenItAsksForOne(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()

	if err := m.spawnSession("claude", "claude-aaaa", dir, "", "do things", true); err != nil {
		t.Fatalf("auto-named spawn: %v", err)
	}
	if err := m.spawnSession("claude", "custom", dir, "", "do things", false); err != nil {
		t.Fatalf("custom spawn: %v", err)
	}
	for _, sess := range m.sessions {
		awaiting := m.awaitingRename(sess)
		if sess.Name == "claude-aaaa" && !awaiting {
			t.Fatal("an auto-named spawn should wait for the name its agent picks")
		}
		if sess.Name == "custom" && awaiting {
			t.Fatal("a custom-named spawn was never asked to rename")
		}
	}
}

func TestSpawnMarksDeferredDirective(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()

	if err := m.spawnSession("claude", "claude-aaaa", dir, "", "/compact", true); err != nil {
		t.Fatalf("slash spawn: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	slashID := m.sessionRows()[0].ID
	if !sessionHasPendingInput(t, m, slashID, launch.DeferredRenameDirective) {
		t.Fatal("slash-prompt spawn should defer the directive")
	}

	if err := m.spawnSession("claude", "claude-bbbb", dir, "", "do things", true); err != nil {
		t.Fatalf("plain spawn: %v", err)
	}
	if err := m.spawnSession("claude", "custom", dir, "", "/compact", false); err != nil {
		t.Fatalf("custom spawn: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	for _, sess := range m.sessionRows() {
		if sess.ID == slashID {
			continue
		}
		if sessionHasPendingInput(t, m, sess.ID, launch.DeferredRenameDirective) {
			t.Fatalf("session %q should not defer a directive", sess.Name)
		}
	}
}

func TestDeferredDirectiveSentWhenPaneReady(t *testing.T) {
	m := buildModel(t)
	if err := m.spawnSession("ready-tool", "ready-tool-abcd", t.TempDir(), "", "", true); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	sess := m.sessionRows()[0]
	// Launch scripts boot the tool immediately, so the first refresh may
	// already deliver the deferred directive. Either still-pending or
	// already present in the pane is success; a missing mark before any
	// send is not possible after spawnSession.
	deadline := time.Now().Add(5 * time.Second)
	for sessionHasPendingInput(t, m, sess.ID, launch.DeferredRenameDirective) {
		if time.Now().After(deadline) {
			pane, _ := m.tmux.CapturePane(sess.ID)
			t.Fatalf("directive never sent; pane:\n%s", pane)
		}
		time.Sleep(100 * time.Millisecond)
		m.applyCmd(t, m.refreshCmd())
	}
	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	// Whitespace is squashed out of both sides: the pane wraps the directive
	// at its own width, mid-word, so any fragment long enough to identify it
	// is long enough to straddle a line break.
	squash := func(s string) string { return strings.Join(strings.Fields(s), "") }
	if !strings.Contains(squash(pane), squash(`"$GATE_INBOX_BIN" rename "<name>"`)) {
		t.Fatalf("pane should hold the directive, got:\n%s", pane)
	}
}

func TestSendModePromptSurvivesPollerRestart(t *testing.T) {
	m := buildModel(t)
	if err := m.spawnSession("send-tool", "custom", t.TempDir(), "", "do the work", false); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sess := m.sessionRows()[0]
	if !sessionHasPendingInput(t, m, sess.ID, "do the work") {
		t.Fatal("launch prompt was not persisted before delivery")
	}
	old := m.poller
	m.poller = newPoller(m.store, m.tmux, m.engine, m.hooks, m.gitDrv,
		old.statusSources, old.sessionStores, old.mcpStyles, old.shellTools, old.interval)
	m.applyCmd(t, m.refreshCmd())
	deadline := time.Now().Add(5 * time.Second)
	for len(sessionPendingInputs(t, m, sess.ID)) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("startup prompt stayed queued after a poller restart and the input box appeared")
		}
		time.Sleep(100 * time.Millisecond)
		m.applyCmd(t, m.refreshCmd())
	}
	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pane, "do the work") || !strings.Contains(pane, "This session is already named") {
		t.Fatalf("pane did not receive the launch prompt:\n%s", pane)
	}
}

func TestSendModeReconcilesAmbiguousDeliveryWithoutResending(t *testing.T) {
	m := buildModel(t)
	if err := m.spawnSession("send-tool", "custom", t.TempDir(), "", "do not resend", false); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sess := m.sessionRows()[0]
	input := sessionPendingInputs(t, m, sess.ID)[0]
	claimed, err := m.store.ClaimPendingInput(sess.ID, input)
	if err != nil || !claimed {
		t.Fatalf("claim pending input = %v, %v", claimed, err)
	}
	old := m.poller
	m.poller = newPoller(m.store, m.tmux, m.engine, m.hooks, m.gitDrv,
		old.statusSources, old.sessionStores, old.mcpStyles, old.shellTools, old.interval)
	msg := m.poller.refreshOnce()
	gotErr, ok := msg.(errMsg)
	if !ok || !strings.Contains(gotErr.err.Error(), "ambiguous pending input") {
		t.Fatalf("refresh result = %#v, want ambiguous-delivery error", msg)
	}
	if inputs := sessionPendingInputs(t, m, sess.ID); len(inputs) != 0 {
		t.Fatalf("ambiguous input was not reconciled: %q", inputs)
	}
	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pane, "do not resend") {
		t.Fatalf("ambiguous input was resent:\n%s", pane)
	}
}

func TestSendModeSurfacesSendFailureAndDoesNotRetry(t *testing.T) {
	m := buildModel(t)
	if err := m.spawnSession("send-tool", "custom", t.TempDir(), "", "cannot deliver", false); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sess, err := m.store.Get(m.sessionRows()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatal(err)
	}
	sent, err := sendPendingInput(t, m, sess, "❯ ", true)
	if err == nil || !strings.Contains(err.Error(), "send pending input") {
		t.Fatalf("a send into a pane that is gone reported %v: nothing tells the operator the launch prompt never landed", err)
	}
	// True because the pass handed the paste off, which is what the rest of
	// the pass needs to know; whether it landed is the error above and the
	// durable claim below, not this.
	if !sent {
		t.Fatal("the input was not recorded as handed to the pane, so the same pass may write into this session again")
	}
	claimed, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed.PendingInputClaimed {
		t.Fatal("failed delivery was not left in an ambiguous durable state")
	}
	sent, err = sendPendingInput(t, m, claimed, "", false)
	if err == nil || !strings.Contains(err.Error(), "ambiguous pending input") || !sent {
		t.Fatalf("reconcile result = %v, %v", sent, err)
	}
	if inputs := sessionPendingInputs(t, m, sess.ID); len(inputs) != 0 {
		t.Fatalf("ambiguous failed delivery was retried: %q", inputs)
	}
}

// sendPendingInput runs one pending-input delivery through to its recorded
// outcome. The pass hands the paste to a goroutine and moves on
// (asyncsend.go), so a test waits for that send the way a later pass would
// and reads what it cost from where the next pass reads it.
func sendPendingInput(t *testing.T, m *Model, sess store.Session, pane string, agentAlive bool) (bool, error) {
	t.Helper()
	sent, err := m.poller.maybeSendPendingInput(sess, pane, agentAlive)
	if !m.poller.awaitSends(10 * time.Second) {
		t.Fatal("a send handed off by the pass was still in flight ten seconds later")
	}
	m.poller.mu.Lock()
	settled := m.poller.sendErr
	m.poller.sendErr = nil
	m.poller.mu.Unlock()
	return sent, errors.Join(err, settled)
}

func sessionPendingInputs(t *testing.T, m *Model, id string) []string {
	t.Helper()
	sess, err := m.store.Get(id)
	if err != nil {
		t.Fatalf("get session %s: %v", id, err)
	}
	return sess.PendingInputs
}

func sessionHasPendingInput(t *testing.T, m *Model, id, want string) bool {
	t.Helper()
	for _, input := range sessionPendingInputs(t, m, id) {
		if input == want || strings.Contains(input, want) {
			return true
		}
	}
	return false
}

func TestBuildLaunchCarriesSessionID(t *testing.T) {
	m := buildModel(t)
	plain := m.cfg.Tools["claude"]
	_, env, err := m.buildLaunch("plain", plain, plain.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("buildLaunch: %v", err)
	}
	if env[hooks.EnvSessionID] != "abcd1234" {
		t.Fatalf("plain tool env = %v, want session id", env)
	}

	hooked := m.cfg.Tools["claude-hooked"]
	_, env, err = m.buildLaunch("hooked", hooked, hooked.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("buildLaunch hooked: %v", err)
	}
	if env[hooks.EnvSessionID] != "abcd1234" || env[hooks.EnvStatusFile] == "" {
		t.Fatalf("hooked tool env = %v, want session id and status file", env)
	}
}

func TestSortedToolNamesOrder(t *testing.T) {
	cfg := config.Config{Tools: map[string]config.Tool{
		"grok":     {Command: "grok"},
		"gemini":   {Command: "gemini"},
		"codex":    {Command: "codex"},
		"claude":   {Command: "claude"},
		"opencode": {Command: "opencode"},
		"pi":       {Command: "pi"},
		"zephyr":   {Command: "zephyr"},
		"acme":     {Command: "acme"},
	}}
	got := sortedToolNames(cfg)
	want := []string{"claude", "codex", "opencode", "acme", "gemini", "grok", "pi", "zephyr"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedToolNames = %v want %v", got, want)
	}
}

// The poller waits for a prompt the agent has to pick up on its own, so only
// a command-line prompt is stored; a tool typed into gets its prompt as the
// first pending input instead. Read before the first poll, which delivers it.
func TestSpawnStoresOnlyACommandLinePrompt(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()

	if err := m.spawnSession("ready-tool", "ready-tool-abcd", dir, "", "/compact", true); err != nil {
		t.Fatalf("command-line spawn: %v", err)
	}
	if err := m.spawnSession("send-tool", "send-tool-abcd", dir, "", "/compact", true); err != nil {
		t.Fatalf("send spawn: %v", err)
	}

	sessions, err := m.store.ListSessions(true)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, sess := range sessions {
		switch sess.Tool {
		case "ready-tool":
			if sess.LaunchPrompt != "/compact" {
				t.Fatalf("command-line launch prompt = %q, want /compact", sess.LaunchPrompt)
			}
		case "send-tool":
			if sess.LaunchPrompt != "" {
				t.Fatalf("typed prompt stored as a launch prompt: %q", sess.LaunchPrompt)
			}
			if len(sess.PendingInputs) == 0 || sess.PendingInputs[0] != "/compact" {
				t.Fatalf("typed prompt is not the first pending input: %q", sess.PendingInputs)
			}
		}
	}
}

// typeIntoForm sends each rune to the form the way a keyboard would.
func typeIntoForm(m *Model, text string) {
	for _, r := range text {
		m.handleFormKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestNewSessionFormOpensOnTheToolField(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	if m.form.focus != fieldTool {
		t.Fatalf("focus = %d, want fieldTool", m.form.focus)
	}
	if !m.form.toolFilter.Focused() {
		t.Fatal("the tool filter should hold the keyboard when the card opens")
	}
	if m.form.name.Focused() {
		t.Fatal("the name field should not be focused on open")
	}
}

func TestToolDisplayOrderPutsCodexBeforeOpencode(t *testing.T) {
	cfg := config.Config{Tools: map[string]config.Tool{
		"opencode": {Command: "opencode"},
		"codex":    {Command: "codex"},
		"claude":   {Command: "claude"},
	}}
	want := []string{"claude", "codex", "opencode"}
	if got := sortedToolNames(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedToolNames = %v want %v", got, want)
	}
}

func TestMatchToolsPrefersPrefixMatches(t *testing.T) {
	names := []string{"claude", "codex", "opencode"}
	if got := matchTools(names, "co"); !reflect.DeepEqual(got, []string{"codex", "opencode"}) {
		t.Fatalf("matchTools(co) = %v", got)
	}
	if got := matchTools(names, "CLA"); !reflect.DeepEqual(got, []string{"claude"}) {
		t.Fatalf("matchTools(CLA) = %v", got)
	}
	if got := matchTools(names, ""); !reflect.DeepEqual(got, names) {
		t.Fatalf("an empty filter should match everything, got %v", got)
	}
	if got := matchTools(names, "zzz"); len(got) != 0 {
		t.Fatalf("matchTools(zzz) = %v, want no matches", got)
	}
}

func TestTypingTheToolNameSelectsIt(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	typeIntoForm(m, "quiet")
	if got := m.selectedToolName(); got != "quietchat" {
		t.Fatalf("tool = %q, want quietchat", got)
	}
	view := ansi.Strip(m.viewForm())
	if !strings.Contains(view, "quietchat") {
		t.Fatalf("the card should show the typed CLI:\n%s", view)
	}
}

func TestArrowsCycleOnlyTheFilteredTools(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	typeIntoForm(m, "tool")
	first := m.selectedToolName()
	seen := map[string]bool{first: true}
	for range 20 {
		m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyRight})
		seen[m.selectedToolName()] = true
	}
	for name := range seen {
		if !strings.Contains(name, "tool") {
			t.Fatalf("cycling left the filter: reached %q", name)
		}
	}
	if len(seen) < 2 {
		t.Fatalf("cycling should reach every match, saw %v", seen)
	}
}

func TestSubmitRefusesAToolFilterThatMatchesNothing(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	typeIntoForm(m, "zzzz")
	before := len(m.sessions)
	m.submitForm()
	if m.mode != modeForm {
		t.Fatalf("mode = %v, want the card to stay open", m.mode)
	}
	if len(m.sessions) != before {
		t.Fatal("a filter matching no CLI should not create a session")
	}
	if !strings.Contains(m.errBar.text, "zzzz") {
		t.Fatalf("errBar = %q, want it to name the unmatched filter", m.errBar.text)
	}
}

func TestToolFieldStaysInsideTheCard(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 60, 30
	m.openForm()
	m.syncFormFieldWidths()
	for _, line := range strings.Split(ansi.Strip(m.viewForm()), "\n") {
		if got := len([]rune(line)); got > m.width {
			t.Fatalf("row %d cols wide in a %d col terminal: %q", got, m.width, line)
		}
	}
}

func TestBackspacingTheFilterKeepsTheChosenTool(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting("default_tool", "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.openForm()
	typeIntoForm(m, "q")
	if got := m.selectedToolName(); got != "quietchat" {
		t.Fatalf("tool = %q, want quietchat", got)
	}
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := m.selectedToolName(); got != "quietchat" {
		t.Fatalf("tool = %q after clearing the filter, want the typed choice to stand", got)
	}
}

func TestLeavingTheToolFieldDropsTheFilter(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	typeIntoForm(m, "quiet")
	m.formFocus(1)
	if got := m.form.toolFilter.Value(); got != "" {
		t.Fatalf("filter = %q, want it dropped when the field loses focus", got)
	}
	if got := m.selectedToolName(); got != "quietchat" {
		t.Fatalf("tool = %q, want the filter's choice to outlive the filter", got)
	}
}

// TestEveryConfiguredToolIsTwoKeystrokesAway pins the actual cost of picking
// a non-default CLI, against the tool set the shipped config declares. The
// card is only worth opening on the tool field if naming one is cheaper than
// arrowing to it, and "cheaper" has to hold for the whole list rather than
// for the one CLI the change was written against.
func TestEveryConfiguredToolIsTwoKeystrokesAway(t *testing.T) {
	names := sortedToolNames(config.Config{Tools: map[string]config.Tool{
		"claude":   {Command: "claude"},
		"opencode": {Command: "opencode"},
		"codex":    {Command: "codex"},
		"terminal": {Shell: true},
	}})
	for _, name := range names {
		reached := false
		for n := 1; n <= 2 && !reached; n++ {
			if n > len(name) {
				break
			}
			matches := matchTools(names, name[:n])
			reached = len(matches) > 0 && matches[0] == name
		}
		if !reached {
			t.Errorf("%q needs more than two keystrokes: 1=%v 2=%v",
				name, matchTools(names, name[:1]), matchTools(names, name[:min(2, len(name))]))
		}
	}
}

// TestTypingACLIAndPressingEnterCreatesIt is the whole flow the change exists
// for: open the card, type the CLI, press enter. Nothing in between.
func TestTypingACLIAndPressingEnterCreatesIt(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting("default_tool", "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.openForm()
	typeIntoForm(m, "qu")
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	sessions, err := m.store.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want the card to have created one", len(sessions))
	}
	if sessions[0].Tool != "quietchat" {
		t.Fatalf("tool = %q, want the typed CLI rather than the settings default", sessions[0].Tool)
	}
}
