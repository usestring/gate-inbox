package tracing

import (
	"encoding/json"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/usestring/gate-inbox/internal/managerbuild"
	"strings"
	"testing"
	"time"
)

// resourceOf reads the resource the exporter actually wrote, the way a
// backend reading the payload would, rather than by calling identity again.
func resourceOf(t *testing.T, path string) map[string]any {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	line := strings.TrimSpace(string(blob))
	if line == "" {
		t.Fatal("nothing was written; the sampler was not pinned")
	}
	var payload struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []struct {
					Key   string         `json:"key"`
					Value map[string]any `json:"value"`
				} `json:"attributes"`
			} `json:"resource"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal([]byte(strings.Split(line, "\n")[0]), &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if len(payload.ResourceSpans) != 1 {
		t.Fatalf("got %d resourceSpans, want 1", len(payload.ResourceSpans))
	}
	out := map[string]any{}
	for _, attr := range payload.ResourceSpans[0].Resource.Attributes {
		// An OTLP value is a one-entry object naming its own type --
		// {"stringValue": "linux"} -- and which type it is is what the
		// encoder's own tests cover, so this keeps the value and drops
		// the wrapper.
		for _, value := range attr.Value {
			out[attr.Key] = value
		}
	}
	return out
}

// The whole point of the change: a span in a shared dataset has to say which
// binary on which machine produced it. Two boards from two trees were
// arriving indistinguishable, and the paths in their span attributes named
// machines nobody could identify.
func TestTheResourceNamesTheBuildAndTheMachine(t *testing.T) {
	path, tracer := tracing(t)
	now := time.Now()
	Record("tmux.capture-pane", now, now.Add(time.Millisecond), nil)
	tracer.Close()

	attrs := resourceOf(t, path)
	if attrs["service.name"] != ServiceName {
		t.Fatalf("service.name is %v, want %q", attrs["service.name"], ServiceName)
	}
	// These four come from the running process, so they are always knowable
	// and their absence is a defect rather than an unstamped build.
	// A machine that will not say its own name leaves host.name out, which
	// is the one value here that can legitimately be missing.
	host, _ := os.Hostname()
	for key, want := range map[string]any{
		"host.name":               host,
		"os.type":                 runtime.GOOS,
		"host.arch":               runtime.GOARCH,
		"process.runtime.version": runtime.Version(),
	} {
		if want == "" {
			continue
		}
		if attrs[key] != want {
			t.Fatalf("%s is %v, want %v", key, attrs[key], want)
		}
	}
	instance, _ := attrs["service.instance.id"].(string)
	if !strings.Contains(instance, "-") {
		t.Fatalf("service.instance.id is %q, want a pid and a random half", instance)
	}
}

// A resource costs what it costs once per payload, not once per span. Sending
// it on every span would multiply the dataset's width by the batch size to
// carry the same nine values.
func TestTheResourceIsSentOncePerPayloadNotPerSpan(t *testing.T) {
	path, tracer := tracing(t)
	start := time.Now()
	trace := NewTrace("poll.pass", start, start.Add(90*time.Millisecond))
	trace.Child("scan", start, start.Add(10*time.Millisecond))
	trace.Child("capture", start.Add(10*time.Millisecond), start.Add(60*time.Millisecond))
	trace.Emit()
	tracer.Close()

	spans := decode(t, path)
	if len(spans) != 3 {
		t.Fatalf("wrote %d spans, want 3", len(spans))
	}
	for _, span := range spans {
		attrs, _ := span["attributes"].([]any)
		for _, attr := range attrs {
			if key := attr.(map[string]any)["key"]; key == "service.version" || key == "host.name" {
				t.Fatalf("%v is on the span; build identity belongs on the resource", key)
			}
		}
	}
	// And it is still there once, on the payload the spans arrived under.
	if resourceOf(t, path)["service.name"] != ServiceName {
		t.Fatal("the resource went missing from a payload carrying several spans")
	}
}

// A build that cannot say which commit it came from must say nothing. A
// constant -- "dev", "unknown" -- is a value in the dataset like any other,
// and it makes a binary that predates this attribution indistinguishable from
// one that has it and could not resolve a commit.
func TestAnUnknownCommitIsOmittedRatherThanNamed(t *testing.T) {
	attrs := identity(buildIdentity{Host: "box", Instance: "17-abcd"}, nil, false)
	for _, attr := range attrs {
		switch attr.Key {
		case "service.version", "vcs.revision", "vcs.time", "vcs.modified":
			t.Fatalf("%s was emitted as %v by a build that knows no commit", attr.Key, attr.Value)
		}
		if attr.Value == "" {
			t.Fatalf("%s was emitted empty; an unknown value is left out", attr.Key)
		}
	}
	// An empty stamp is the same case as no build info at all, and reaches
	// identity by a different route: settings present, values blank.
	blank := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: ""},
		{Key: "vcs.modified", Value: ""},
	}}
	for _, attr := range identity(buildIdentity{Host: "box", Instance: "17-abcd"}, blank, true) {
		if strings.HasPrefix(attr.Key, "vcs.") || attr.Key == "service.version" {
			t.Fatalf("%s was emitted as %v from a blank stamp", attr.Key, attr.Value)
		}
	}
}

// Go stamps nothing under `go run`, which is how the `am` wrapper starts this
// board, so the linker's value has to be able to fill that in -- and has to
// win, since the stamp is missing exactly when it was needed.
func TestTheLinkerRevisionWinsOverTheStamp(t *testing.T) {
	stamped := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "stamped0000000000000000000000000000000f"},
		{Key: "vcs.time", Value: "2026-09-17T05:01:06Z"},
		{Key: "vcs.modified", Value: "true"},
	}}
	got := map[string]any{}
	for _, attr := range identity(buildIdentity{Revision: "linker00000000000000000000000000000000f", Host: "box", Instance: "17-abcd"}, stamped, true) {
		got[attr.Key] = attr.Value
	}
	if got["service.version"] != "linker00000000000000000000000000000000f" {
		t.Fatalf("service.version is %v, want the linker's value", got["service.version"])
	}
	// The stamp is still reported beside it: when the two disagree, which
	// tree the binary was actually compiled out of is the question being asked.
	if got["vcs.revision"] != "stamped0000000000000000000000000000000f" {
		t.Fatalf("vcs.revision is %v, want the stamp kept alongside", got["vcs.revision"])
	}
	// A dirty tree has to arrive as a boolean, not as the string Go keeps it
	// in, or a backend cannot filter on it.
	if got["vcs.modified"] != true {
		t.Fatalf("vcs.modified is %#v, want the bool true", got["vcs.modified"])
	}
	// With no linker value the stamp is what service.version reports.
	fallback := map[string]any{}
	for _, attr := range identity(buildIdentity{Host: "box", Instance: "17-abcd"}, stamped, true) {
		fallback[attr.Key] = attr.Value
	}
	if fallback["service.version"] != "stamped0000000000000000000000000000000f" {
		t.Fatalf("service.version is %v, want the stamp when no linker value is set", fallback["service.version"])
	}
}

// The fallback this exists for. `go run .` is how the board is really
// started, Go stamps no commit into it, and nothing in this repository can
// pass -ldflags on that path -- so without the fingerprint the boards most
// likely to be traced arrive with no build identity at all.
func TestTheFingerprintStandsInWhenNoCommitIsKnowable(t *testing.T) {
	got := map[string]any{}
	for _, attr := range identity(buildIdentity{
		Fingerprint: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Host:        "box",
		Instance:    "17-abcd",
	}, nil, false) {
		got[attr.Key] = attr.Value
	}
	if got["service.build.fingerprint"] != "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" {
		t.Fatalf("service.build.fingerprint is %v, want the digest", got["service.build.fingerprint"])
	}
	// It names a build; it does not name a commit. Filling service.version
	// with a content hash would make an unattributed binary read as an
	// attributed one, which is the whole failure this set of attributes
	// exists to prevent.
	if _, present := got["service.version"]; present {
		t.Fatalf("service.version is %v; a fingerprint is not a commit", got["service.version"])
	}
}

// A build that does know its commit still leads with the commit, so the
// fallback never quietly displaces the better answer.
func TestAKnownCommitIsStillPreferredToTheFingerprint(t *testing.T) {
	got := map[string]any{}
	for _, attr := range identity(buildIdentity{
		Revision:    "linker00000000000000000000000000000000f",
		Fingerprint: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Host:        "box",
		Instance:    "17-abcd",
	}, nil, false) {
		got[attr.Key] = attr.Value
	}
	if got["service.version"] != "linker00000000000000000000000000000000f" {
		t.Fatalf("service.version is %v, want the commit", got["service.version"])
	}
	// Both are reported: the commit says which source, the fingerprint says
	// which bytes, and a dirty or hand-patched build is where they diverge.
	if got["service.build.fingerprint"] == nil {
		t.Fatal("the fingerprint was dropped once a commit was known; they answer different questions")
	}
}

// The tag is only emitted when a release build resolved one. main keeps its
// "dev" default out of here, and an empty Release must not become a key.
func TestTheReleaseTagIsOmittedUntilThereIsOne(t *testing.T) {
	for _, attr := range identity(buildIdentity{Host: "box", Instance: "17-abcd"}, nil, false) {
		if attr.Key == "service.release" {
			t.Fatalf("service.release was emitted as %v by a build with no tag", attr.Value)
		}
	}
	got := map[string]any{}
	for _, attr := range identity(buildIdentity{Release: "0.31.0", Host: "box", Instance: "17-abcd"}, nil, false) {
		got[attr.Key] = attr.Value
	}
	if got["service.release"] != "0.31.0" {
		t.Fatalf("service.release is %v, want the tag", got["service.release"])
	}
}

// The fingerprint costs one hash of the binary for the life of the process.
// managerbuild memoises it and the board asks at startup, so the exporter
// must be reusing that answer rather than rehashing 23MB on every flush.
func TestTheFingerprintIsNotRecomputedPerFlush(t *testing.T) {
	first := managerbuild.Fingerprint()
	if first == "" {
		t.Skip("this binary cannot read itself; nothing to memoise")
	}
	start := time.Now()
	for range 1000 {
		if managerbuild.Fingerprint() != first {
			t.Fatal("the fingerprint changed under a running process")
		}
	}
	// A rehash of the test binary is milliseconds each; a memoised read is
	// nanoseconds. A thousand of them inside a millisecond can only be the
	// memoised path.
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("1000 reads took %s, so it is being recomputed rather than memoised", elapsed)
	}
}
