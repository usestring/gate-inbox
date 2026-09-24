package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
)

// roleSpecs is an extension that declares the roles it is given, as
// "board/<name>".
type roleSpecs []extension.RoleSpec

func (roleSpecs) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "board"} }
func (roleSpecs) Configure(extension.Config) error { return nil }
func (r roleSpecs) Roles() []extension.RoleSpec    { return r }

// useRoleSpecs makes specs the process's roles for the rest of the test,
// which is therefore never parallel.
func useRoleSpecs(t *testing.T, specs ...extension.RoleSpec) {
	t.Helper()
	registry, err := extension.NewRegistry([]extension.Extension{roleSpecs(specs)})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Configure("", nil); err != nil {
		t.Fatal(err)
	}
	collected, err := registry.SessionHooks("", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sessionhooks.Use(func() (*extension.SessionHooks, error) { return collected, nil }))
}

func withRole(sess store.Session, role string) store.Session {
	sess.Role = role
	return sess
}

// A helper that is there to be looked at stays on screen under a folded
// parent, and the parent's badge counts only the children the fold hides.
func TestAnOnScreenHelperIsNeverFoldedAway(t *testing.T) {
	useRoleSpecs(t, extension.RoleSpec{Name: "reviewer", OnScreen: true})
	m := childModel(t)
	m.sessions = append(m.sessions,
		withRole(childSess("h1", "reviewer", "work", "p1", status.Waiting, 10*time.Minute), "board/reviewer"))
	m.rebuildRows()
	if got := joined(rowIDs(m)); got != "p1,h1,s9" {
		t.Fatalf("rows = %q, want the helper drawn under its folded parent", got)
	}
	if counts := m.childCounts(m.sessions, "p1"); counts[status.Waiting] != 1 {
		t.Fatalf("counts = %v, want only c2 waiting: the helper is not in the badge", counts)
	}

	// A parent whose only child is such a helper has nothing to fold.
	m.sessions = []store.Session{
		childSess("p2", "worker", "work", "", status.Working, time.Hour),
		withRole(childSess("h2", "reviewer", "work", "p2", status.Waiting, time.Minute), "board/reviewer"),
	}
	m.rebuildRows()
	if m.hasChildren("p2") {
		t.Fatal("a parent with only an on-screen helper reads as foldable")
	}
}

// A role that does not ask to stay on screen folds like any child.
func TestAnOrdinaryRoleFoldsLikeAnyChild(t *testing.T) {
	useRoleSpecs(t, extension.RoleSpec{Name: "linter"})
	m := childModel(t)
	m.sessions = append(m.sessions,
		withRole(childSess("h1", "linter", "work", "p1", status.Waiting, 10*time.Minute), "board/linter"))
	m.rebuildRows()
	if got := joined(rowIDs(m)); got != "p1,s9" {
		t.Fatalf("rows = %q, want the linter folded away", got)
	}
}

func TestAnOnScreenHelperIsNoSubagent(t *testing.T) {
	useRoleSpecs(t, extension.RoleSpec{Name: "reviewer", OnScreen: true}, extension.RoleSpec{Name: "linter"})
	if isSubagent(store.Session{ParentID: "run", Role: "board/reviewer"}) {
		t.Fatal("an on-screen helper read as a subagent")
	}
	if !isSubagent(store.Session{ParentID: "run", Role: "board/linter"}) {
		t.Fatal("an ordinary role's child is still a subagent")
	}
}

// The drain hands the operator an on-screen helper's question, which is
// what the helper was launched to hold.
func TestTriageOffersAnOnScreenHelper(t *testing.T) {
	useRoleSpecs(t, extension.RoleSpec{Name: "reviewer", OnScreen: true})
	m := buildModel(t)
	m.cfg.Tools = map[string]config.Tool{"claude": {}}
	m.triage = true
	m.sessions = []store.Session{
		childSess("p1", "worker", "work", "", status.Working, time.Hour),
		withRole(childSess("h1", "reviewer", "work", "p1", status.Waiting, ownedGrace+time.Minute), "board/reviewer"),
	}
	m.rebuildRows()
	i, ok := m.nextTriageInput("", map[string]bool{})
	if !ok {
		t.Fatal("the drain offered nothing, want the helper")
	}
	if got := m.rows[i].sess.ID; got != "h1" {
		t.Fatalf("the drain offered %q, want the helper", got)
	}
}

// A block whose parent has an unarchived floating helper heads its group,
// taking its children with it; the rest keep their order.
func TestAFloatingHelperMovesItsBlockToTheHeadOfItsGroup(t *testing.T) {
	useRoleSpecs(t, extension.RoleSpec{Name: "reviewer", FloatParent: true})
	m := childModel(t)
	m.sessions = []store.Session{
		childSess("a1", "first", "work", "", status.Working, 3*time.Hour),
		childSess("a2", "second", "work", "", status.Idle, 2*time.Hour),
		childSess("p1", "worker", "work", "", status.Working, time.Hour),
		childSess("c1", "probe", "work", "p1", status.Working, 50*time.Minute),
		withRole(childSess("h1", "reviewer", "work", "p1", status.Waiting, 10*time.Minute), "board/reviewer"),
		childSess("s9", "other-group", "elsewhere", "", status.Idle, time.Hour),
	}
	m.groups = []string{"work", "elsewhere"}
	m.setChildrenFolded("p1", false)
	m.rebuildRows()
	if got := joined(rowIDs(m)); got != "p1,c1,h1,a1,a2,s9" {
		t.Fatalf("rows = %q, want p1's block first in its group and the rest in order", got)
	}

	m.sessions[4].Archived = true
	m.rebuildRows()
	if got := joined(rowIDs(m)); got != "a1,a2,p1,c1,s9" {
		t.Fatalf("rows = %q, want the store's order back once the helper is archived", got)
	}
}

// floatBlocks keeps groups where they are in a list that interleaves them,
// and moves a floated block's descendants with it.
func TestFloatBlocksKeepsEachGroupsPositions(t *testing.T) {
	useRoleSpecs(t, extension.RoleSpec{Name: "reviewer", FloatParent: true})
	all := []store.Session{
		{ID: "a1", Group: "x"},
		{ID: "b1", Group: "y"},
		{ID: "a2", Group: "x"},
		{ID: "b2", Group: "y"},
		{ID: "b3", Group: "y", ParentID: "b2"},
		{ID: "h", Group: "y", ParentID: "b3", Role: "board/reviewer"},
	}
	roots := floatedRoots(all)
	if !roots["b2"] || len(roots) != 1 {
		t.Fatalf("roots = %v, want b2: the helper's grandparent is its block's top", roots)
	}
	var got []string
	for _, sess := range floatBlocks(all, all, roots) {
		got = append(got, sess.ID)
	}
	if joined(got) != "a1,b2,a2,b3,h,b1" {
		t.Fatalf("order = %q", joined(got))
	}
}

func TestASilentRoleIsNotRelayedToItsParent(t *testing.T) {
	useRoleSpecs(t, extension.RoleSpec{Name: "reviewer", Silent: true})
	p, st := pollerWithStore(t)
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "worker", Status: status.Working})
	helper := seedSession(t, st, store.Session{ID: "helper01", Name: "reviewer", ParentID: parent.ID, Status: status.Working, Role: "board/reviewer"})

	if err := p.relayChildQuestion(helper, status.Waiting, askPane); err != nil {
		t.Fatal(err)
	}
	if err := p.relayChildRest(helper, status.Finished); err != nil {
		t.Fatal(err)
	}
	if head, found, err := st.HeadMessage(parent.ID); err != nil || found {
		t.Fatalf("the parent was told %+v (%v)", head, err)
	}
}

// A pinned role is read off its status file even on a tool whose status
// comes off the pane, and is never called hookless.
func TestAPinnedRoleReadsItsStatusFileWhateverItsTool(t *testing.T) {
	useRoleSpecs(t, extension.RoleSpec{Name: "reviewer", PinnedStatus: true})
	m := buildModel(t)
	pane := "here is the result\n❯ \n"
	plain := store.Session{ID: "plain001", Tool: "claude"}
	pinned := store.Session{ID: "pinned01", Tool: "claude", Role: "board/reviewer"}
	writeHookStatus(t, m, plain.ID, status.Waiting)
	writeHookStatus(t, m, pinned.ID, status.Waiting)

	if got := deriveStatus(t, m, pinned, pane, true); got != status.Waiting {
		t.Fatalf("pinned status = %q, want the file's %q", got, status.Waiting)
	}
	if got := deriveStatus(t, m, plain, pane, true); got == status.Waiting {
		t.Fatal("a session with no pinned role read a status file its tool never writes")
	}

	unwired := sysstat.ProcStat{OK: true, Procs: 2, ArgvMarkOK: true, ArgvMark: false}
	hooked := store.Session{ID: "pinned02", Tool: "claude-hooked", Role: "board/reviewer"}
	if hooklessTree(hooked, hooks.StatusSourceClaude, true, unwired) {
		t.Fatal("a pinned role was called hookless")
	}
}

// A pass that samples a pinned session before its agent has started reads
// the pane, and keeps the pin for the pass after: the extension may have
// pinned it the moment it launched. A plain hooked session's file is still
// cleared once its agent has gone.
func TestAPinSurvivesAPassBeforeItsAgentStarts(t *testing.T) {
	useRoleSpecs(t, extension.RoleSpec{Name: "reviewer", PinnedStatus: true})
	m := buildModel(t)
	pane := "here is the result\n❯ \n"
	pinned := store.Session{ID: "pinned03", Tool: "claude", Role: "board/reviewer"}
	writeHookStatus(t, m, pinned.ID, status.Waiting)

	if got := deriveStatus(t, m, pinned, pane, false); got == status.Waiting {
		t.Fatal("a pin was read while no agent was running to hold it")
	}
	if got, ok := m.hooks.Read(pinned.ID); !ok || got != status.Waiting {
		t.Fatalf("the pin = %q, %v after a pass with no agent; want it kept", got, ok)
	}
	if got := deriveStatus(t, m, pinned, pane, true); got != status.Waiting {
		t.Fatalf("pinned status = %q once the agent runs, want the pin's %q", got, status.Waiting)
	}

	hooked := store.Session{ID: "hooked01", Tool: "claude-hooked"}
	writeHookStatus(t, m, hooked.ID, status.Waiting)
	deriveStatus(t, m, hooked, pane, false)
	if _, ok := m.hooks.Read(hooked.ID); ok {
		t.Fatal("a dead agent's own status file was kept")
	}
}
