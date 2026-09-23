package envname

import "testing"

func TestGetTreatsABlankValueAsUnset(t *testing.T) {
	t.Setenv(SessionID, " ")
	if got := Get(SessionID); got != "" {
		t.Fatalf("Get = %q, want a blank value read as unset", got)
	}
	t.Setenv(SessionID, "abcd1234")
	if got := Get(SessionID); got != "abcd1234" {
		t.Fatalf("Get = %q, want abcd1234", got)
	}
}
