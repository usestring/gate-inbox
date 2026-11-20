package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The flags that widen an apply mean nothing without it, and an apply acts
// on live sessions, which a copied state.db does not have. Both are refused
// before anything is opened.
func TestSweepRefusesApplyFlagsThatCannotMeanAnything(t *testing.T) {
	for args, want := range map[string]string{
		"--force":           "do nothing without it",
		"--kill-waiting":    "do nothing without it",
		"--revive --json":   "do nothing without it",
		"--apply --db x.db": "cannot be pointed at another state.db",
	} {
		var out bytes.Buffer
		err := runSweep(&out, strings.Fields(args), "", t.TempDir())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("sweep %s: err = %v, want %q", args, err, want)
		}
	}
}
