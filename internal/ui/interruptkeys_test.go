package ui

import (
	"reflect"
	"testing"
)

type recordingSender struct{ calls [][]string }

func (r *recordingSender) SendKeys(id string, keys ...string) error {
	r.calls = append(r.calls, keys)
	return nil
}

// OpenCode's double Escape must leave as two writes, or its parser reads one
// alt+Escape and the turn keeps running.
func TestSendInterruptKeysWritesEachKeySeparately(t *testing.T) {
	r := &recordingSender{}
	if err := sendInterruptKeys(r, "s", []string{"Escape", "Escape"}); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"Escape"}, {"Escape"}}; !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %v, want %v", r.calls, want)
	}
}
