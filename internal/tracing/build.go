package tracing

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"

	"github.com/usestring/gate-inbox/internal/managerbuild"
)

// Revision is the commit this binary was built from, when the build supplies
// one:
//
//	go build -ldflags "-X github.com/usestring/gate-inbox/internal/tracing.Revision=$(git rev-parse HEAD)"
//
// It is an override rather than the source, because Go already stamps the
// commit into a binary built from a working tree and asking every build to
// repeat that by hand is how the two disagree. What it is here for is the
// builds Go declines to stamp, and on this program that is most of them.
// Measured on this module, with Go 1.26.5:
//
//	go build, standalone clone   vcs.revision, vcs.time, vcs.modified
//	go run .                     nothing: no vcs settings at all
//	go build, git submodule      nothing, and no error either
//
// The middle row is the one that matters. `go run .` is how an operator's launch wrapper
// starts this board, so the boards most likely to be traced are exactly the
// ones Go tells nothing about. The third is a working copy nested inside
// another repository, which -buildvcs declines without complaining.
//
// Both rows are also why Fingerprint below exists. Nothing in this repository
// can set this variable on the dominant path: `am` is a shell function in the
// operator's own profile that runs `go run .`, with no build step this tree
// controls, and launch.Install copies the running binary rather than building
// one. tools/capture/capture.sh does build, and does pass this, but that is
// the recorder rather than the board an operator runs. So a commit is what
// this reports when a build could supply one, and never a stand-in when none
// could.
var Revision string

// Release is the tagged version this build reports, set by main when it has
// resolved one. It stays empty for the development builds everybody runs,
// which is why it is separate from Revision: a commit says which code, a tag
// says which release, and only one of them exists on any given build.
var Release string

// resource is what this process reports itself as: the machine, the build and
// the run, worked out once and then reused.
//
// It rides on the OTLP resource rather than on the span. The exporter sends
// one resource and up to five hundred spans under it, so the whole of this
// costs one map of strings per flush and nothing per span, which is the only
// budget it could have come out of -- the recording path's is 2.4us of CPU
// per second and it is already spent.
var resource = sync.OnceValue(func() []Attr {
	info, ok := debug.ReadBuildInfo()
	// An error here is a machine that will not say its own name, which is
	// not worth failing a trace over; the key is simply left out.
	host, _ := os.Hostname()
	return identity(buildIdentity{
		Revision: Revision,
		Release:  Release,
		// managerbuild memoises this for the life of the process and the
		// board has already asked for it at startup, so by the first flush
		// this is a field read. A run that never asked -- a test, a
		// subcommand -- pays one hash of the binary here instead, once,
		// on the exporter's goroutine rather than the recording path.
		Fingerprint: managerbuild.Fingerprint(),
		Host:        host,
		Instance:    instanceID(),
	}, info, ok)
})

// buildIdentity is what identity cannot work out for itself. It is a struct
// rather than five string parameters because four of them are strings and
// transposing two would produce a payload that is wrong and still valid.
type buildIdentity struct {
	Revision    string
	Release     string
	Fingerprint string
	Host        string
	Instance    string
}

// identity renders the resource attributes, and renders only what its inputs
// actually say: a key whose source is empty is left out of the payload.
//
// The omission is the point. "unknown" or "dev" in service.version is a value
// like any other once it is in the dataset, and a query grouping by version
// would count it as a release. Left absent, one APL predicate separates a
// binary that predates this from one that could not work out its own commit,
// and neither is mistaken for a build somebody could go and check out.
func identity(build buildIdentity, info *debug.BuildInfo, ok bool) []Attr {
	settings := map[string]string{}
	if ok && info != nil {
		for _, setting := range info.Settings {
			settings[setting.Key] = setting.Value
		}
	}
	// The linker's value wins where both exist: it is set by a build that was
	// told its commit, and the stamp is missing in precisely the cases that
	// made the flag necessary.
	revision := build.Revision
	if revision == "" {
		revision = settings["vcs.revision"]
	}

	attrs := []Attr{{Key: "service.name", Value: ServiceName}}
	add := func(key, value string) {
		if value == "" {
			return
		}
		attrs = append(attrs, Attr{Key: key, Value: value})
	}
	add("service.version", revision)
	add("service.release", build.Release)
	// The sha256 of the running binary, which is the one identity available
	// on every launch path: `go run` builds a binary Go deletes on exit and
	// stamps nothing into, so on the board an operator actually starts this
	// is all there is. It names the build without claiming to name a commit
	// -- it cannot be checked out, only compared -- so it is its own key and
	// never a substitute for service.version. Two boards agreeing on it are
	// running the same bytes; a board that disagrees with a colleague's has
	// a different build, whatever either of them says about a commit.
	add("service.build.fingerprint", build.Fingerprint)
	add("service.instance.id", build.Instance)
	add("vcs.revision", settings["vcs.revision"])
	add("vcs.time", settings["vcs.time"])
	// Whether the tree was dirty is what decides how much the revision above
	// is worth: these binaries are mostly built out of worktrees, and a dirty
	// one names a commit whose contents it does not have.
	if modified, err := strconv.ParseBool(settings["vcs.modified"]); err == nil {
		attrs = append(attrs, Attr{Key: "vcs.modified", Value: modified})
	}
	add("host.name", build.Host)
	add("os.type", runtime.GOOS)
	add("host.arch", runtime.GOARCH)
	add("process.runtime.version", runtime.Version())
	return attrs
}

// instanceID tells two boards running on one machine apart.
//
// A pid on its own will not. Starting this program supersedes a manager
// already running on the same state directory rather than opening a second
// board, so the board that has just been asked to quit and the board that
// replaced it are both sending within the same few seconds, and an operating
// system that hands out numbers again can give them the same one. The pid is
// kept because it is what an operator has at a terminal; the random half is
// what makes the pair unambiguous regardless.
func instanceID() string {
	return strconv.Itoa(os.Getpid()) + "-" + newSpanID()
}
