package sessname

import "testing"

func TestShortTitleCountsWords(t *testing.T) {
	for title, want := range map[string]bool{
		"":                           false,
		"Guardian review":            true,
		"Fix the loki grafana panel": true,
		"Build UK business rates overpayment detection system": false,
	} {
		if got := ShortTitle(title); got != want {
			t.Errorf("ShortTitle(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestExactKebabCasesTheWholeName(t *testing.T) {
	got := Exact{}.Compress("Fix the Loki panel")
	if len(got) != 1 || got[0] != "fix-the-loki-panel" {
		t.Fatalf("Compress = %v", got)
	}
	if got := (Exact{}).Compress("  ...  "); got != nil {
		t.Fatalf("Compress of punctuation = %v, want nil", got)
	}
}
