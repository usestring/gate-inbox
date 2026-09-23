// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

func TestWaitReturnsAsSoonAsTheSessionRests(t *testing.T) {
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Create leaves the row on starting; the manager would move it on. Here
	// the test plays the manager.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = h.store.UpdateStatus(created.ID, status.Finished)
	}()

	started := time.Now()
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !result.Reached || result.Session.Status != status.Finished {
		t.Fatalf("wait result = %+v", result)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("wait did not return promptly: %s", elapsed)
	}
}

func TestWaitTimesOutWithTheCurrentStateRatherThanAnError(t *testing.T) {
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.store.UpdateStatus(created.ID, status.Working); err != nil {
		t.Fatal(err)
	}
	// Shorter than the poll interval, so a wait that only wakes on the tick
	// overruns the timeout its caller asked for.
	asked := 300 * time.Millisecond
	started := time.Now()
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Timeout: asked})
	if err != nil {
		t.Fatalf("a timeout must not be an error: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a %s wait took %s", asked, elapsed)
	}
	if result.Reached || result.Outcome != WaitTimedOut || result.Session.Status != status.Working {
		t.Fatalf("timeout result = %+v", result)
	}
	for _, timeout := range []time.Duration{-time.Second, MaxWaitTimeout + time.Second} {
		if _, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Timeout: timeout}); err == nil ||
			!strings.Contains(err.Error(), "outside 0 to "+MaxWaitTimeout.String()) {
			t.Fatalf("a %s wait = %v, want a refusal naming the bound", timeout, err)
		}
	}
}

func TestWaitSeesAKilledSessionAsDeadWithoutTheManager(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.store.UpdateStatus(created.ID, status.Working); err != nil {
		t.Fatal(err)
	}
	if err := h.driver.Kill(created.ID); err != nil {
		t.Fatal(err)
	}
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Until: []string{"dead"}, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !result.Reached || result.Session.Running {
		t.Fatalf("killed session result = %+v", result)
	}
}

// A worker that crashes mid-turn is dead from the first tick, and its
// stored status will never move again. Parking the whole timeout to say
// "timed out" hides a death the wait had already seen.
func TestWaitReportsAnObservedDeathWithoutWaitingOut(t *testing.T) {
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.store.UpdateStatus(created.ID, status.Working); err != nil {
		t.Fatal(err)
	}
	if err := h.driver.Kill(created.ID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Until: []string{"finished"}, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Outcome != WaitDied || result.Reached {
		t.Fatalf("a session that died while working = %+v", result)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("the wait sat on a death it could already see for %s", elapsed)
	}
	// Waiting for the death itself is still an ordinary arrival.
	awaited, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Until: []string{"dead"}, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Wait for dead: %v", err)
	}
	if awaited.Outcome != WaitReached || !awaited.Reached {
		t.Fatalf("awaiting dead = %+v", awaited)
	}
}

func TestWaitRefusesSelfAndUnknownStates(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{h.caller.ID}, Timeout: time.Second}); err == nil ||
		!strings.Contains(err.Error(), "cannot wait on itself") {
		t.Fatalf("self wait error = %v", err)
	}
	if _, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Until: []string{"done"}, Timeout: time.Second}); err == nil ||
		!strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("unknown state error = %v", err)
	}
}

func TestWaitHonoursCancellation(t *testing.T) {
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.store.UpdateStatus(created.ID, status.Working); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	if _, err := h.sessions.Wait(ctx, h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Timeout: time.Minute}); err == nil {
		t.Fatal("a cancelled wait should report the cancellation")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancellation was not honoured promptly: %s", elapsed)
	}
}

func TestWaitSeparatesADeathFromATimeout(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.store.UpdateStatus(created.ID, status.Finished); err != nil {
		t.Fatal(err)
	}
	if err := h.driver.Kill(created.ID); err != nil {
		t.Fatal(err)
	}
	// The stored status says finished, which is awaited, but the pane is gone.
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Until: []string{"finished"}, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Reached || result.Outcome != WaitDied {
		t.Fatalf("a session that died before the awaited state = %+v", result)
	}
}

// spawnChildren gives the caller a fan-out to wait on, since every test
// below is about the set rather than about any one member of it.
func spawnChildren(t *testing.T, h *sessionHarness, names ...string) []Session {
	t.Helper()
	children := make([]Session, 0, len(names))
	for _, name := range names {
		created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: name})
		if err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		if created.ParentID != h.caller.ID {
			t.Fatalf("%s landed under %q, not under the caller", name, created.ParentID)
		}
		if err := h.store.UpdateStatus(created.ID, status.Working); err != nil {
			t.Fatal(err)
		}
		children = append(children, created)
	}
	return children
}

func standingFor(result WaitResult, id string) (WaitStanding, bool) {
	for _, standing := range result.Standing {
		if standing.Session.ID == id {
			return standing, true
		}
	}
	return WaitStanding{}, false
}

// The shape the fan-out actually needs: one call over five children that
// comes back on the first one to arrive, carrying what the other four are
// doing. A parent that had to ask afterwards would be back to a list per
// wait, which is the cost this replaces.
func TestWaitOnChildrenEndsOnTheFirstArrivalAndReportsTheWholeSet(t *testing.T) {
	h := newSessionHarness(t)
	children := spawnChildren(t, h, "worker-one", "worker-two", "worker-three")
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = h.store.UpdateStatus(children[1].ID, status.Finished)
	}()

	started := time.Now()
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{Children: true, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("the wait sat through an arrival it could see for %s", elapsed)
	}
	if !result.Reached || result.Outcome != WaitReached {
		t.Fatalf("wait over the fan-out = %+v", result)
	}
	// The set's answer leads with whoever moved, not with whoever the
	// board happened to list first.
	if result.Session.ID != children[1].ID || result.Session.Status != status.Finished {
		t.Fatalf("the call was decided by %+v, want %s", result.Session, children[1].ID)
	}
	if len(result.Standing) != len(children) {
		t.Fatalf("standing covers %d of %d children: %+v", len(result.Standing), len(children), result.Standing)
	}
	for index, child := range children {
		standing, ok := standingFor(result, child.ID)
		if !ok {
			t.Fatalf("child %s is missing from the standing: %+v", child.ID, result.Standing)
		}
		want := WaitTimedOut
		if index == 1 {
			want = WaitReached
		}
		if standing.Outcome != want {
			t.Fatalf("child %s stands at %q, want %q", child.ID, standing.Outcome, want)
		}
		// The delta is only worth having if it says what the others are
		// doing, not merely that they are not done.
		if want == WaitTimedOut && standing.Session.Status != status.Working {
			t.Fatalf("child %s carries status %q rather than what it was doing", child.ID, standing.Session.Status)
		}
	}
}

// One child dying is not a reason to stop waiting on the four still
// working, and a wave that leaves a dead row behind would otherwise make
// every later children wait useless: it would return that same death the
// instant it was asked, forever. The death is reported, not answered with.
func TestADeadSiblingDoesNotEndAWaitTheRestAreStillRunning(t *testing.T) {
	h := newSessionHarness(t)
	children := spawnChildren(t, h, "worker-one", "worker-two")
	if err := h.driver.Kill(children[0].ID); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(400 * time.Millisecond)
		_ = h.store.UpdateStatus(children[1].ID, status.Finished)
	}()
	started := time.Now()
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{
		SessionIDs: []string{children[0].ID, children[1].ID},
		Until:      []string{"finished"},
		Timeout:    10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// It waited past the death for the sibling that was still working, and
	// still came back on that sibling rather than on the timeout.
	if elapsed := time.Since(started); elapsed < 300*time.Millisecond {
		t.Fatalf("the death ended the wait after %s, before the sibling could arrive", elapsed)
	}
	if !result.Reached || result.Session.ID != children[1].ID {
		t.Fatalf("a set holding a death = %+v", result)
	}
	dead, _ := standingFor(result, children[0].ID)
	if dead.Outcome != WaitDied || dead.Session.Running {
		t.Fatalf("the death was not reported alongside the arrival: %+v", dead)
	}
}

// A single named target is the other half of that rule: with nothing else
// in the set, "everything settled" is "this one died", so the wait still
// returns the moment its pane goes.
func TestWaitOnASetEndsWhenEveryTargetHasSettled(t *testing.T) {
	h := newSessionHarness(t)
	children := spawnChildren(t, h, "worker-one", "worker-two")
	for _, child := range children {
		if err := h.driver.Kill(child.ID); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{
		Children: true,
		Until:    []string{"finished"},
		Timeout:  30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("a wave with nothing left running parked for %s", elapsed)
	}
	if result.Reached || result.Outcome != WaitDied || len(result.Standing) != 2 {
		t.Fatalf("a wave that died out = %+v", result)
	}
	for _, standing := range result.Standing {
		if standing.Outcome != WaitDied {
			t.Fatalf("a member of a dead wave stands at %+v", standing)
		}
	}
}

// An arrival outranks a death: one child crashing must not hide the one
// that produced the result the parent is waiting for.
func TestWaitOnASetPrefersAnArrivalToADeath(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	children := spawnChildren(t, h, "worker-one", "worker-two")
	if err := h.driver.Kill(children[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpdateStatus(children[1].ID, status.Finished); err != nil {
		t.Fatal(err)
	}
	// Named states rather than the default resting set, which counts dead
	// as an arrival and would make this test agree with itself.
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{
		Children: true,
		Until:    []string{"finished"},
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !result.Reached || result.Session.ID != children[1].ID {
		t.Fatalf("an arrival beside a death = %+v", result)
	}
	dead, _ := standingFor(result, children[0].ID)
	if dead.Outcome != WaitDied {
		t.Fatalf("the death was not reported alongside the arrival: %+v", result.Standing)
	}
}

// A timeout over a set is the same normal answer it is over one session,
// and it is the answer that has to carry the most: nobody arrived, so the
// whole value of the call is what each of them was doing instead.
func TestWaitOnASetTimesOutCarryingEveryMembersState(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	children := spawnChildren(t, h, "worker-one", "worker-two")
	if err := h.store.UpdateStatus(children[1].ID, status.Starting); err != nil {
		t.Fatal(err)
	}
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{
		Children: true,
		Until:    []string{"finished"},
		Timeout:  700 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("a timeout must not be an error: %v", err)
	}
	if result.Reached || result.Outcome != WaitTimedOut || len(result.Standing) != 2 {
		t.Fatalf("timeout over a set = %+v", result)
	}
	working, _ := standingFor(result, children[0].ID)
	starting, _ := standingFor(result, children[1].ID)
	if working.Session.Status != status.Working || starting.Session.Status != status.Starting {
		t.Fatalf("the standing does not carry each state: %+v", result.Standing)
	}
	if !strings.Contains(FormatWaitResult(result), "0 of 2 reached, 2 still working, 0 died") {
		t.Fatalf("the sentence does not summarise the set: %q", FormatWaitResult(result))
	}
}

// An archived child is off the wave rather than a reason to refuse the
// wait, while an archived id somebody named is still the mistake it was.
func TestWaitOnChildrenSkipsAnArchivedChild(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	children := spawnChildren(t, h, "worker-one", "worker-two")
	if _, err := h.sessions.Archive(h.caller.ID, children[0].ID, true); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{
		Children: true,
		Until:    []string{"finished"},
		Timeout:  600 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(result.Standing) != 1 || result.Standing[0].Session.ID != children[1].ID {
		t.Fatalf("an archived child is still in the wave: %+v", result.Standing)
	}
	if _, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{
		SessionIDs: []string{children[0].ID},
		Timeout:    time.Second,
	}); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("naming an archived session = %v", err)
	}
}

func TestWaitRefusesASetItCannotResolve(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	children := spawnChildren(t, h, "worker-one")
	for _, refusal := range []struct {
		name string
		opts WaitOptions
		want string
	}{
		{"nothing named", WaitOptions{Timeout: time.Second}, "nothing to wait on"},
		{"both at once", WaitOptions{Children: true, SessionIDs: []string{children[0].ID}, Timeout: time.Second}, "not both"},
		{"itself among them", WaitOptions{SessionIDs: []string{children[0].ID, h.caller.ID}, Timeout: time.Second}, "cannot wait on itself"},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			if _, err := h.sessions.Wait(context.Background(), h.caller.ID, refusal.opts); err == nil ||
				!strings.Contains(err.Error(), refusal.want) {
				t.Fatalf("error = %v, want one saying %q", err, refusal.want)
			}
		})
	}
	// A session with no fan-out is told what to do instead of being told a
	// wait failed.
	other, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "childless-agent"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Wait(context.Background(), other.ID, WaitOptions{Children: true, Timeout: time.Second}); err == nil ||
		!strings.Contains(err.Error(), "no children to wait on") {
		t.Fatalf("waiting on an empty fan-out = %v", err)
	}
}

// The same id twice is one session, and a set that counted it twice would
// report a three-child wave as four.
func TestWaitDedupesNamedSessions(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	children := spawnChildren(t, h, "worker-one")
	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{
		SessionIDs: []string{children[0].ID, children[0].ID},
		Until:      []string{"finished"},
		Timeout:    600 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(result.Standing) != 1 {
		t.Fatalf("one session named twice = %+v", result.Standing)
	}
}
