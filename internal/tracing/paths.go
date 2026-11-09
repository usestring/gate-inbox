package tracing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// A path names a person, a machine and a repository, and a dataset of spans
// is shared. Call sites are asked not to set one, but asking is not enough:
// an error wraps whatever its callee put in it, and an extension or a future
// call site can set any string. So every string a span carries -- its name,
// its attributes, its error text, the resource -- is checked here, on the
// way out, and every path found is sent as HashPath's form of it.

// HashPath is p as it may leave the machine: each segment a keyed hash of the
// path up to and including it, joined with "/", after "pa:" for an absolute
// path or "pr:" for a relative one, so a three-directory absolute path
// becomes pa:1a2b3c4d/5e6f7a8b/9c0d1e2f: paths that share a prefix share their leading hashes and show
// their depth, and nothing else about them can be read without this install's
// key. Each hash covers the whole prefix, so the same name at two places in
// two paths does not show as the same hash.
//
// The path is normalised first: "\" read as "/", "." and ".." resolved, a
// trailing separator dropped, a drive letter lowercased and kept as the first
// segment, "~" taken as the home directory and a file:// URL as its path.
func HashPath(p string) string { return hashPathWith(installKey(), p) }

// pathSegmentHex is how much of each segment's HMAC is sent.
const pathSegmentHex = 8

func hashPathWith(key []byte, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if len(p) >= 5 && strings.EqualFold(p[:5], "file:") {
		p = strings.TrimPrefix(p[5:], "//")
		// file:///x has an empty host and file://host/x is a share; either
		// way what follows the host is the path.
		if !strings.HasPrefix(p, "/") {
			p = "//" + p
		}
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			p = home + p[1:]
		}
	}
	p = strings.ReplaceAll(p, `\`, "/")
	var segments []string
	absolute := false
	if len(p) >= 2 && p[1] == ':' && isLetter(p[0]) {
		segments, absolute, p = []string{asciiLower(p[:2])}, true, p[2:]
	}
	if strings.HasPrefix(p, "/") {
		absolute = true
	}
	cleaned := path.Clean(p)
	if absolute {
		// Above the root is the root; a relative path keeps its leading
		// "..", which is where it climbs to.
		cleaned = path.Clean("/" + p)
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment != "" && segment != "." {
			segments = append(segments, segment)
		}
	}
	flag := "pr:"
	if absolute {
		flag = "pa:"
	}
	hashes := make([]string, len(segments))
	prefix := flag
	for i, segment := range segments {
		prefix += "/" + segment
		m := hmac.New(sha256.New, key)
		m.Write([]byte("path\x00"))
		m.Write([]byte(prefix))
		hashes[i] = hex.EncodeToString(m.Sum(nil))[:pathSegmentHex]
	}
	return flag + strings.Join(hashes, "/")
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// stop are the characters a path cannot run across: whitespace, quotes and
// the brackets and separators that surround a value in a message.
const stop = "\\s\"'`<>(){}\\[\\],;|"

var (
	// fileURL is a file:// URL, which is a path with a scheme.
	fileURL = regexp.MustCompile(`(?i)file:/[^` + stop + `]*`)
	// drivePath is a drive-letter path. Its directories may hold spaces
	// ("C:\Program Files\x"), so a run with spaces counts as long as a
	// separator ends it.
	drivePath = regexp.MustCompile(`(^|[` + stop + `=:])([A-Za-z]:[/\\](?:[^"'<>|\r\n/\\]*[/\\])*[^` + stop + `]*)`)
	// knownRoot is any token with one of these root directories inside it,
	// wherever the token starts and with either separator: each names a
	// place on a disk.
	knownRoot = regexp.MustCompile(`(?i)[^` + stop + `=]*[/\\](home|users|tmp|var|private|opt|root|mnt|volumes|etc|run|srv|nix|usr|documents and settings)[/\\][^` + stop + `]*`)
	// rooted is a token that starts as a path does: "/", "~/", "./", "../"
	// or a UNC share, at the start of the value or after a separator, so
	// "1/2" and "and/or" are left alone.
	rooted = regexp.MustCompile(`(^|[` + stop + `=:])((?:~[A-Za-z0-9._-]*|\.{1,2})[/\\][^` + stop + `]*|[/\\][^` + stop + `]+)`)
)

// hashPathsIn is s with every path in it replaced, in place, by HashPath's
// form of it. A trailing colon or full stop is the sentence's, not the
// path's, and is kept.
func hashPathsIn(s string) string {
	if s == "" {
		return s
	}
	for _, place := range localPlaces() {
		s = replaceFold(s, place)
	}
	s = fileURL.ReplaceAllStringFunc(s, hashToken)
	s = replaceLed(drivePath, s)
	s = knownRoot.ReplaceAllStringFunc(s, hashToken)
	s = replaceLed(rooted, s)
	return s
}

// replaceLed hashes each match of re's second group, leaving the separator
// that led into it.
func replaceLed(re *regexp.Regexp, s string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		lead := re.FindStringSubmatch(m)[1]
		body := m[len(lead):]
		if lead == ":" && strings.HasPrefix(body, "//") {
			// A URL's own "//": what follows is a host, which the error
			// scrubber blanks, and then the URL's path, checked as any
			// other text is.
			return "://" + hashPathsIn(body[2:])
		}
		return lead + hashToken(body)
	})
}

// hashToken is a matched path as HashPath sends it, keeping the punctuation
// that ends a sentence or a "path: error" prefix. A token already hashed is
// left as it is.
func hashToken(m string) string {
	if hashedPath.MatchString(m) {
		return m
	}
	body := strings.TrimRight(m, ":.")
	if body == "" {
		return m
	}
	return HashPath(body) + m[len(body):]
}

var hashedPath = regexp.MustCompile(`^p[ar]:[0-9a-f/]*$`)

// replaceFold replaces place, and the rest of the path it starts, in s.
// Case is folded because macOS and Windows disks fold it.
func replaceFold(s, place string) string {
	// ASCII folding only, so an index into lower is an index into s.
	lower, needle := asciiLower(s), asciiLower(place)
	var b strings.Builder
	for {
		i := strings.Index(lower, needle)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(needle)
		for end < len(s) && !strings.ContainsRune(" \t\r\n\"'`<>(){}[],;|", rune(s[end])) {
			end++
		}
		b.WriteString(s[:i])
		b.WriteString(hashToken(s[i:end]))
		s, lower = s[end:], lower[end:]
	}
}

// pathStart is a value that begins the way a path does.
var pathStart = regexp.MustCompile(`(?i)^(~|\.{1,2}[/\\]|/|\\\\|[a-z]:[/\\]|file:/)`)

// isPath is whether s is a path and nothing else. One that begins as a path
// does is taken whole, spaces in its directories included, unless its last
// segment runs on into words: that is a message.
func isPath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || hashedPath.MatchString(s) {
		return false
	}
	if pathStart.MatchString(s) {
		last := s[strings.LastIndexAny(s, `/\`)+1:]
		if !strings.ContainsAny(last, " \t\r\n") {
			return true
		}
	}
	return hashedPath.MatchString(strings.Trim(hashPathsIn(s), ":. "))
}

// HidePaths is s as it may leave the machine: HashPath of it when it is a
// path and nothing else, otherwise s with each path inside it hashed in
// place. Extensions call it, through extension.HidePaths, on anything they
// send elsewhere, so their events and the board's spans agree on a path.
func HidePaths(s string) string {
	if isPath(s) {
		return HashPath(s)
	}
	return hashPathsIn(s)
}

// localPlaces are this machine's own directories: the home directory, where
// the process runs, the repository around it, the board's config directory,
// the temp directory and the binary's. Each is caught as text wherever it
// appears, so a path that matches no pattern above still never leaves.
// Longest first, so a repository inside home is hashed whole.
var localPlaces = sync.OnceValue(findLocalPlaces)

func findLocalPlaces() []string {
	var places []string
	add := func(dir string) {
		dir = filepath.Clean(strings.TrimSpace(dir))
		// A root or a one-letter directory would catch half of every message.
		if len(dir) < 3 || dir == "." {
			return
		}
		for _, have := range places {
			if have == dir {
				return
			}
		}
		places = append(places, dir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home)
	}
	if cwd, err := os.Getwd(); err == nil {
		add(cwd)
		add(repoRoot(cwd))
	}
	add(keyDir())
	add(os.TempDir())
	if exe, err := os.Executable(); err == nil {
		add(filepath.Dir(exe))
	}
	sort.Slice(places, func(i, j int) bool { return len(places[i]) > len(places[j]) })
	return places
}

// repoRoot is the nearest directory at or above dir holding .git, or "".
func repoRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// privateValue is one attribute value as it may leave, with its paths
// hashed. Values that are not strings are rendered first, since the encoder
// would otherwise print them as they are.
func privateValue(v any) any {
	switch typed := v.(type) {
	case bool, int, int64, float64, time.Duration:
		return v
	case string:
		return HidePaths(typed)
	default:
		return HidePaths(encodeValue(v)["stringValue"].(string))
	}
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
