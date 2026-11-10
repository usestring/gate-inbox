package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/workspec"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

type ciTestForge struct{ pr forge.PR }

func (f ciTestForge) PRs(refs []workspec.Ref) (map[string]forge.PR, forge.Health) {
	prs := map[string]forge.PR{}
	for _, ref := range refs {
		if ref.Kind == workspec.KindPR {
			prs[ref.Key()] = f.pr
		}
	}
	return prs, forge.Health{OK: true}
}

func TestCIFeedbackOptInUsesBoardRefreshAndDisablingRetiresQueue(t *testing.T) {
	m := buildModel(t)
	st := m.store
	const url = "https://github.com/example/repo/pull/7"
	at := time.Now()
	if err := st.CreateSession(store.Session{ID: "owner", Tool: "claude", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordOpenedPRs("owner", []string{url}, at); err != nil {
		t.Fatal(err)
	}
	tracker := newWorkTracker(m.cfg, st)
	tracker.Now = func() time.Time { return at }
	refresh := func() {
		t.Helper()
		at = at.Add(2 * time.Minute)
		tracker.GitHub = ciTestForge{pr: forge.PR{URL: url, State: forge.PROpen, Checks: forge.ChecksFailing, FetchedAt: at,
			CI: &forge.CISnapshot{HeadSHA: "current", Complete: true, Checks: []forge.CICheck{{
				ID: at.Format(time.RFC3339Nano), Name: "tests", Status: "COMPLETED", Conclusion: "FAILURE",
			}}}}}
		session := worktracker.Session{ID: "owner", Text: url, Live: true}
		tracker.Discover(session)
		tracker.Refresh([]worktracker.Session{session})
	}
	queued := func() int {
		t.Helper()
		n, err := st.QueuedCount("owner")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	toggle := func() {
		t.Helper()
		m.openSettings()
		m.settings.field = settingsFieldExperimental
		m.handleSettingsKey(key("enter"))
		m.settings.experimentalCursor = slices.IndexFunc(experiments, func(feature experiment) bool {
			return feature.setting == store.CIFeedbackSetting
		})
		if m.settings.experimentalCursor < 0 {
			t.Fatal("CI feedback experiment is missing")
		}
		m.handleSettingsKey(key("right"))
		m.handleSettingsKey(key("esc"))
		m.handleSettingsKey(key("esc"))
	}
	refresh()
	if n := queued(); n != 0 {
		t.Fatalf("default board sent CI feedback: %d messages", n)
	}
	toggle()
	m.openSettings()
	if !m.settings.ciFeedback {
		t.Fatal("CI feedback selection did not persist")
	}
	m.handleSettingsKey(key("esc"))
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	if !storedExperiment(reopened, store.CIFeedbackSetting) {
		t.Fatal("CI feedback opt-in did not survive reopening the board store")
	}
	reopened.Close()
	refresh()
	if n := queued(); n != 1 {
		t.Fatalf("enabled board did not queue feedback: %d messages", n)
	}
	toggle()
	if n := queued(); n != 0 {
		t.Fatalf("disabling left feedback queued: %d messages", n)
	}
	refresh()
	if n := queued(); n != 0 {
		t.Fatalf("existing tracker queued feedback after disabling: %d messages", n)
	}
}
