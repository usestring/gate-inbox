package artifacts

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
)

// configured returns the extension configured from this section, with
// key_command set to a binary that certainly exists so a test turns on what
// it means to turn on rather than on whether gcloud is installed.
func configured(t *testing.T, mutate func(map[string]any)) *Extension {
	t.Helper()
	section := map[string]any{
		"enabled":     true,
		"base_url":    "https://artifacts.example.test",
		"key_secret":  "ARTIFACT_KEY",
		"key_command": "sh -c true",
		"link_ttl":    "1h",
		"max_bytes":   int64(1024),
	}
	if mutate != nil {
		mutate(section)
	}
	ext := New()
	if err := ext.Configure(extension.NewConfig(section)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	return ext
}

// Off until asked for, like a plugin: a config that predates the section
// keeps the tool list it had.
func TestOffUntilEnabled(t *testing.T) {
	ext := configured(t, func(s map[string]any) { delete(s, "enabled") })
	if ext.Enabled() {
		t.Fatal("the artifact store is on without anyone turning it on")
	}
	absent := New()
	if err := absent.Configure(extension.NewConfig(nil)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if absent.Enabled() {
		t.Fatal("an absent section turned the artifact store on")
	}
}

func TestEnabledMatrix(t *testing.T) {
	cases := map[string]struct {
		mutate func(map[string]any)
		want   bool
	}{
		"turned on":  {nil, true},
		"turned off": {func(s map[string]any) { s["enabled"] = false }, false},
		// Enabled is not enough: with no worker or no key command there is
		// no store, and nothing fills one in.
		"blank base url":      {func(s map[string]any) { s["base_url"] = "" }, false},
		"no base url":         {func(s map[string]any) { delete(s, "base_url") }, false},
		"whitespace base url": {func(s map[string]any) { s["base_url"] = "   " }, false},
		"no key command":      {func(s map[string]any) { delete(s, "key_command") }, false},
		"no way to read keys": {func(s map[string]any) { s["key_command"] = "definitely-not-on-this-path" }, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := configured(t, tc.mutate).Enabled(); got != tc.want {
				t.Fatalf("enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// Only the limits have defaults. The worker, the key and the publisher's
// identity belong to whoever deployed the store, so an operator who named
// none of them gets none of them -- never somebody else's store.
func TestConfigureFillsOnlyTheLimits(t *testing.T) {
	ext := New()
	if err := ext.Configure(extension.NewConfig(map[string]any{"enabled": true, "base_url": "https://elsewhere.test/"})); err != nil {
		t.Fatalf("configure: %v", err)
	}
	got := ext.settings
	if got.BaseURL != "https://elsewhere.test" {
		t.Fatalf("base_url kept its trailing slash: %q", got.BaseURL)
	}
	if got.KeySecret != "" || got.KeyCommand != "" || got.IdentityCommand != "" {
		t.Fatalf("key settings were filled in for an operator who named none: %+v", got)
	}
	if got.LinkTTL.Duration != defaultLinkTTL || got.MaxBytes != defaultMaxBytes {
		t.Fatalf("limits not filled: %+v", got)
	}
	if err := ext.Configure(extension.NewConfig(nil)); err != nil {
		t.Fatalf("configure absent: %v", err)
	}
	if ext.settings.BaseURL != "" {
		t.Fatalf("an absent section publishes to %q", ext.settings.BaseURL)
	}
}

// A misspelt key is refused rather than dropped: ignored, it reads to the
// operator like a setting that does nothing.
func TestConfigureRefusesUnknownKeys(t *testing.T) {
	err := New().Configure(extension.NewConfig(map[string]any{"enabled": true, "base_ulr": "https://x.test"}))
	if err == nil || !strings.Contains(err.Error(), "base_ulr") {
		t.Fatalf("a misspelt key was accepted: %v", err)
	}
}

// Enabled runs at the start of every session the manager spawns, so it must
// not reach the network. A secret read here is a secret read on every
// agent's launch path.
func TestEnabledDoesNotReadTheSecret(t *testing.T) {
	stubCommand(t, func(string) (string, error) {
		t.Fatal("Enabled read the signing key")
		return "", nil
	})
	configured(t, nil).Enabled()
}

func TestPublishRequiresTitleAndContent(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("an invalid publish reached the network")
	}))
	cases := map[string]publishArgs{
		"no content":    {Title: "Something"},
		"blank content": {Title: "Something", Content: "   "},
		"no title":      {Content: "<p>hi</p>"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := publish(context.Background(), client, publisher{session: "session"}, args); err == nil {
				t.Fatal("published it anyway")
			}
		})
	}
}

// The store serves what it is given straight back to a browser, so the
// media type is an allowlist rather than a hint.
func TestPublishRefusesUnservableTypes(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("a disallowed type reached the network")
	}))
	for _, contentType := range []string{"application/octet-stream", "application/x-shellscript", "text/html; charset=utf-8"} {
		_, err := publish(context.Background(), client, publisher{session: "session"}, publishArgs{
			Title: "T", Content: "x", ContentType: contentType,
		})
		if err == nil {
			t.Fatalf("accepted %s", contentType)
		}
	}
}

func TestPublishDefaultsToHTMLAndMintsAnID(t *testing.T) {
	var seenPath, seenType string
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenType = r.Header.Get("Content-Type")
		w.Write([]byte(`{}`))
	}))

	out, err := publish(context.Background(), client, publisher{session: "session-1"}, publishArgs{Title: "Report", Content: "<p>hi</p>"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if seenType != "text/html" {
		t.Fatalf("content type %q", seenType)
	}
	id := strings.TrimPrefix(seenPath, "/a/")
	if len(id) < 16 {
		t.Fatalf("minted a weak id: %q", id)
	}
	if !strings.Contains(out, id) || !strings.Contains(out, "Report") {
		t.Fatalf("result does not report the id and title: %s", out)
	}
}

// Revising an artifact in place is what keeps a link already handed out
// pointing at current content.
func TestPublishReusesAGivenID(t *testing.T) {
	var seenPath string
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.Write([]byte(`{}`))
	}))
	if _, err := publish(context.Background(), client, publisher{session: "s"}, publishArgs{
		Title: "Report", Content: "<p>v2</p>", ArtifactID: testID,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if seenPath != "/a/"+testID {
		t.Fatalf("published to %s", seenPath)
	}
}

// A link may leave the team; who published it must not leave with it, so
// attribution is stored beside the artifact rather than in the link.
func TestAttributionIsStoredNotPutInTheLink(t *testing.T) {
	var header string
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("X-Artifact-Meta")
		w.Write([]byte(`{}`))
	}))
	by := publisher{email: "alice@example.test", session: "session-abc"}
	out, err := publish(context.Background(), client, by, publishArgs{Title: "T", Content: "x"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	meta, err := decodeMeta(header)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if meta.Email != "alice@example.test" || meta.Session != "session-abc" {
		t.Fatalf("attribution stored as email=%q session=%q", meta.Email, meta.Session)
	}
	for _, leaked := range []string{"alice@example.test", "session-abc"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("the link carries %q: %s", leaked, out)
		}
	}
}

func TestListFormatsEmptyAndPopulated(t *testing.T) {
	if got := formatList(nil); !strings.Contains(got, "no artifacts") {
		t.Fatalf("empty list reads as %q", got)
	}
	got := formatList([]Meta{{ID: "abc", Title: "First", Email: "alice@example.test", PublishedAt: "2026-09-17T00:00:00Z"}})
	for _, want := range []string{"abc", "First", "by alice@example.test"} {
		if !strings.Contains(got, want) {
			t.Fatalf("list reads as %q, missing %q", got, want)
		}
	}
}

// An artifact whose publisher could not be read says so, rather than
// printing a blank that reads like a formatting bug.
func TestListNamesAnUnknownPublisher(t *testing.T) {
	if got := formatList([]Meta{{ID: "abc", Title: "First"}}); !strings.Contains(got, "by unknown publisher") {
		t.Fatalf("list reads as %q", got)
	}
}

func TestListShowsWhoLastChangedARevisedArtifact(t *testing.T) {
	got := formatList([]Meta{{ID: "abc", Title: "Report", Email: "maker@example.test", UpdatedBy: "fixer@example.test", Revisions: 3}})
	if !strings.Contains(got, "by maker@example.test") || !strings.Contains(got, "3 versions, last by fixer@example.test") {
		t.Fatalf("list reads as %q", got)
	}
	if got := formatList([]Meta{{ID: "abc", Title: "Once", Email: "maker@example.test", Revisions: 1}}); strings.Contains(got, "versions") {
		t.Fatalf("an unrevised artifact reads as %q", got)
	}
}

// Every artifacts setting a distribution supplies is left empty by an absent
// section and by an enabled one that names none of them, and the store stays
// off: enabled alone must never reach a worker nobody named.
func TestTheCoreShipsNoSuppliedArtifactSetting(t *testing.T) {
	sections := map[string]map[string]any{"absent": nil, "enabled only": {"enabled": true}}
	for label, section := range sections {
		ext := New()
		if err := ext.Configure(extension.NewConfig(section)); err != nil {
			t.Fatalf("%s: configure: %v", label, err)
		}
		if ext.Enabled() {
			t.Errorf("%s: the artifact tools are on with no store named", label)
		}
		v := reflect.ValueOf(ext.settings)
		checked := 0
		for _, s := range config.DistributionSupplied {
			key, ok := strings.CutPrefix(s.Key, "extensions."+Name+".")
			if s.Env || !ok {
				continue
			}
			found := false
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).Tag.Get("toml") != key {
					continue
				}
				found = true
				if !v.Field(i).IsZero() {
					t.Errorf("%s: %s = %v; the core must leave it for a distribution to supply", label, s.Key, v.Field(i))
				}
			}
			if !found {
				t.Errorf("%s is not an artifacts setting", s.Key)
			}
			checked++
		}
		if checked == 0 {
			t.Fatal("no artifacts setting is listed, so nothing was checked")
		}
	}
}
