package extension_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// spawnWatcher is a former that also hears of spawns from the form, noting
// the order it was told in.
type spawnWatcher struct {
	former
	order     *[]string
	panicking bool
	heard     []map[string]string
}

func (w *spawnWatcher) FormSpawned(_ context.Context, sess extension.SessionInfo, form map[string]string) {
	*w.order = append(*w.order, w.id+" "+sess.ID)
	if w.panicking {
		panic("cannot look today")
	}
	w.heard = append(w.heard, form)
}

// Each observer whose fields were on the form hears of the spawn once, in
// registration order, with only its own values; one with none is not told,
// and one that panics neither stops the rest nor escapes.
func TestFormSpawnedHandsEachExtensionItsOwnValues(t *testing.T) {
	var order []string
	ext1 := &spawnWatcher{former: former{id: "ext1"}, order: &order}
	tally := &spawnWatcher{former: former{id: "tally"}, order: &order, panicking: true}
	extra := &spawnWatcher{former: former{id: "extra"}, order: &order}
	level := &spawnWatcher{former: former{id: "level"}, order: &order}
	hooks := sessionHooks(t, ext1, tally, extra, level)
	form := map[string]string{"ext1/items": "on", "tally/items": "off", "extra/items": "off", "extra/level": "b"}

	err := hooks.FormSpawned(context.Background(), extension.SessionInfo{ID: "s1"}, form)
	if err == nil || !strings.Contains(err.Error(), `extension "tally"`) {
		t.Fatalf("err = %v, want the panic named", err)
	}
	if want := []string{"ext1 s1", "tally s1", "extra s1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("told in order %v, want %v", order, want)
	}
	if want := []map[string]string{{"items": "on"}}; !reflect.DeepEqual(ext1.heard, want) {
		t.Errorf("ext1 heard %v, want %v", ext1.heard, want)
	}
	if want := []map[string]string{{"items": "off", "level": "b"}}; !reflect.DeepEqual(extra.heard, want) {
		t.Errorf("extra heard %v, want %v", extra.heard, want)
	}
	var none *extension.SessionHooks
	if err := none.FormSpawned(context.Background(), extension.SessionInfo{}, form); err != nil {
		t.Fatalf("nil hooks: %v", err)
	}
}
