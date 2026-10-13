//go:build darwin || linux

package codexbind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCachedResumeRoutesOnlyToTheNewExclusiveBridge(t *testing.T) {
	dir := t.TempDir()
	endpoint, err := os.MkdirTemp("/tmp", "gi-codex-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(endpoint) })
	binding := filepath.Join(endpoint, "caller.sock") + "#" + strings.Repeat("a", 64)
	first := &bridge{dir: dir, identity: Identity{Daemon: Pin{PID: 123, Started: 456}}}
	if err = first.claim("thread"); err != nil {
		t.Fatal(err)
	}
	if err = first.saveBinding(binding); err != nil {
		t.Fatal(err)
	}
	if err = first.routeBinding(binding, "/tmp/first.sock"); err != nil {
		t.Fatal(err)
	}
	second := &bridge{dir: dir, identity: first.identity, launches: map[string]string{}}
	request := []byte(`{"id":1,"method":"thread/resume","params":{"threadId":"thread"}}`)
	if _, err = second.prepare(request, "/tmp/second.sock"); err == nil {
		t.Fatal("live owner was displaced")
	}
	first.lock.Close()
	data, err := second.prepare(request, "/tmp/second.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer second.lock.Close()
	var m message
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	set := m.Params["config"].(map[string]any)["shell_environment_policy"].(map[string]any)["set"].(map[string]any)
	if set[Env] != binding {
		t.Fatal("cached runtime capability changed on resume")
	}
	first.disconnect()
	path, _, _ := strings.Cut(binding, "#")
	if target, _ := os.Readlink(path); target != "/tmp/second.sock" {
		t.Fatal("old disconnect revoked the new live owner")
	}
	second.disconnect()
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("disconnected runtime retained a live endpoint")
	}
}

func TestSavedRuntimeCannotAliasAnArbitraryPath(t *testing.T) {
	b := &bridge{}
	if err := b.routeBinding("/tmp/foreign/caller.sock#"+strings.Repeat("a", 64), "/tmp/destination.sock"); err == nil {
		t.Fatal("foreign runtime directory accepted")
	}
}
