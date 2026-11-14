package store

import (
	"testing"
	"time"
)

// The Settings choice outranks config.toml, "off" turns hibernation off over
// a configured period, and an empty or unreadable value leaves the default.
func TestHibernationPolicyPrefersTheStoredChoice(t *testing.T) {
	st := newTestStore(t)
	cases := []struct {
		after, min string
		wantAfter  time.Duration
		wantMin    int
	}{
		{"", "", 2 * time.Hour, 3},
		{"off", "", 0, 3},
		{"30m", "10", 30 * time.Minute, 10},
		{"soon", "-1", 2 * time.Hour, 3},
	}
	for _, tc := range cases {
		if err := st.SetSetting(HibernateAfterSetting, tc.after); err != nil {
			t.Fatal(err)
		}
		if err := st.SetSetting(HibernateMinSessionsSetting, tc.min); err != nil {
			t.Fatal(err)
		}
		after, minSessions, err := st.HibernationPolicy(2*time.Hour, 3)
		if err != nil {
			t.Fatal(err)
		}
		if after != tc.wantAfter || minSessions != tc.wantMin {
			t.Errorf("stored (%q, %q): got (%s, %d), want (%s, %d)", tc.after, tc.min, after, minSessions, tc.wantAfter, tc.wantMin)
		}
	}
}
