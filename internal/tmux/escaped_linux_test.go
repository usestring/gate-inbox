package tmux

import "testing"

func TestParseStatReadsPastAParenthesisedName(t *testing.T) {
	stat := "1234 (tmux: (server)) S 1 1234 1234 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 10 18446744073709551615"
	ppid, start, ok := parseStat(stat)
	if !ok || ppid != 1 || start != 987654 {
		t.Fatalf("parseStat = %d, %d, %v; want 1, 987654, true", ppid, start, ok)
	}
}
