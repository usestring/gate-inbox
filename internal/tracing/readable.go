package tracing

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// A path inside a repository the operator named readable is sent as
// "owner/name:path/in/repo" instead of hashed, so a team reading its own
// board's traces can tell which file of which of its repositories a span is
// about. Only the part inside the repository is sent: where the checkout
// lives -- the home directory, the user name, a worktree's directory --
// stays on the machine, and every path outside a readable repository is
// hashed as before. With no patterns, the default, every path is hashed.
//
// A repository is readable when the nearest directory at or above the path
// holding .git -- a checkout, a linked worktree or a submodule, the
// innermost winning -- has an origin remote on github.com whose owner/name
// matches a pattern. The remote is read from the repository's git config,
// never by running git, and remembered per repository root.

// readable is the compiled pattern set; nil when there is none.
var readable atomic.Pointer[readableRepos]

type readableRepos struct {
	patterns []string
	mu       sync.Mutex
	// roots caches a repository root's owner/name, "" when it is not
	// readable; paths caches a path's readable form, "" when it has none.
	roots map[string]string
	paths map[string]string
}

// readableCacheSize bounds each cache: a long-running board sees many paths,
// and a forgotten entry only costs re-reading a few small files.
const readableCacheSize = 4096

// SetReadableRepos names the repositories whose paths are sent readable:
// GitHub "owner/name" globs as path.Match reads them, matched without regard
// to case, so "acme/*" is every repository of acme. Nil or empty sends every
// path hashed. It refuses a pattern that is not one owner and one name.
// Every config load calls it, so the same patterns again keep what is
// already cached.
func SetReadableRepos(patterns []string) error {
	clean, err := cleanRepoPatterns(patterns)
	if err != nil {
		return err
	}
	if len(clean) == 0 {
		readable.Store(nil)
		return nil
	}
	if current := readable.Load(); current != nil && slices.Equal(current.patterns, clean) {
		return nil
	}
	readable.Store(&readableRepos{patterns: clean, roots: map[string]string{}, paths: map[string]string{}})
	return nil
}

// CheckReadableRepos reports whether SetReadableRepos would take patterns.
func CheckReadableRepos(patterns []string) error {
	_, err := cleanRepoPatterns(patterns)
	return err
}

func cleanRepoPatterns(patterns []string) ([]string, error) {
	var clean []string
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if err := checkRepoPattern(p); err != nil {
			return nil, err
		}
		clean = append(clean, p)
	}
	return clean, nil
}

func checkRepoPattern(p string) error {
	owner, name, ok := strings.Cut(p, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("readable_repos: %q: want a GitHub owner/name, globs allowed", p)
	}
	if _, err := path.Match(p, "x/x"); err != nil {
		return fmt.Errorf("readable_repos: %q: %w", p, err)
	}
	return nil
}

func (r *readableRepos) matches(repo string) bool {
	for _, p := range r.patterns {
		if ok, _ := path.Match(p, repo); ok {
			return true
		}
	}
	return false
}

// hidePath is p as it may leave: readable when it lies inside a readable
// repository, HashPath's form otherwise.
func hidePath(p string) string {
	if r := readable.Load(); r != nil {
		if form := r.form(p); form != "" {
			return form
		}
	}
	return HashPath(p)
}

// readableToken matches what form returns, so a pass over text that already
// went through one does not hash it again.
var readableToken = regexp.MustCompile(`^([a-z0-9._-]+/[a-z0-9._-]+):([^/` + stop + `][^` + stop + `]*)?$`)

// isReadableForm reports whether m is already a readable path: one of the
// readable repositories, and a relative path that does not climb out of it.
func isReadableForm(m string) bool {
	r := readable.Load()
	if r == nil {
		return false
	}
	sub := readableToken.FindStringSubmatch(m)
	if sub == nil || !r.matches(sub[1]) {
		return false
	}
	rel := sub[2]
	return rel == "" || (!strings.Contains(rel, `\`) && path.Clean(rel) == rel && rel != ".." && !strings.HasPrefix(rel, "../"))
}

// form is p's readable form, or "" when p is not inside a readable
// repository. Only an absolute path can be: a relative one is relative to a
// directory this process cannot know.
func (r *readableRepos) form(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		p = home + p[1:]
	}
	if len(p) >= 5 && strings.EqualFold(p[:5], "file:") {
		p = strings.TrimPrefix(p[5:], "//")
	}
	if !filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	r.mu.Lock()
	form, ok := r.paths[p]
	r.mu.Unlock()
	if ok {
		return form
	}
	form = r.compute(p)
	r.mu.Lock()
	if len(r.paths) >= readableCacheSize {
		clear(r.paths)
	}
	r.paths[p] = form
	r.mu.Unlock()
	return form
}

func (r *readableRepos) compute(p string) string {
	root := repoRoot(p)
	if root == "" {
		return ""
	}
	repo := r.repoAt(root)
	if repo == "" {
		return ""
	}
	// A symlink inside the repository can point anywhere: what it leads to
	// must be inside the repository too, or the name inside it says where.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	if !within(realRoot, resolveExisting(p)) {
		return ""
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	if rel == "." {
		rel = ""
	}
	return repo + ":" + filepath.ToSlash(rel)
}

// resolveExisting is p with symlinks resolved as far as it exists: a file
// that is gone or not yet written still sits under a directory that is.
func resolveExisting(p string) string {
	rest := ""
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// repoAt is the owner/name of root's origin when it is readable, else "".
func (r *readableRepos) repoAt(root string) string {
	r.mu.Lock()
	repo, ok := r.roots[root]
	r.mu.Unlock()
	if ok {
		return repo
	}
	repo = githubRepo(originURL(root))
	if repo != "" && !r.matches(repo) {
		repo = ""
	}
	r.mu.Lock()
	if len(r.roots) >= readableCacheSize {
		clear(r.roots)
	}
	r.roots[root] = repo
	r.mu.Unlock()
	return repo
}

// originURL is the url of root's origin remote, read from its git config:
// .git is the git directory itself, or, in a linked worktree or a
// submodule, a file naming it; a worktree's git directory names the common
// one, which holds the config, in its commondir file.
func originURL(root string) string {
	gitDir := filepath.Join(root, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		raw, err := os.ReadFile(gitDir)
		if err != nil {
			return ""
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
		if !ok {
			return ""
		}
		gitDir = strings.TrimSpace(target)
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		gitDir = common
	}
	return remoteURL(filepath.Join(gitDir, "config"), "origin")
}

// remoteURL reads remote's url from a git config file: the first url under
// [remote "<remote>"].
func remoteURL(configPath, remote string) string {
	f, err := os.Open(configPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	in := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			in = strings.EqualFold(strings.Join(strings.Fields(line), " "), `[remote "`+remote+`"]`)
			continue
		}
		if !in {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "url") {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}

// githubRepo is the lowercase owner/name a github.com remote url names, or
// "" for any other url: git@github.com:o/r.git, ssh://git@github.com/o/r,
// https://github.com/o/r.git and https://user@github.com/o/r alike.
func githubRepo(url string) string {
	var host, rest string
	if scheme, after, ok := strings.Cut(url, "://"); ok {
		switch strings.ToLower(scheme) {
		case "https", "http", "ssh", "git":
		default:
			return ""
		}
		host, rest, _ = strings.Cut(after, "/")
	} else {
		// scp-like: [user@]host:owner/name
		var ok bool
		host, rest, ok = strings.Cut(url, ":")
		if !ok || strings.Contains(host, "/") {
			return ""
		}
	}
	if _, h, ok := strings.Cut(host, "@"); ok {
		host = h
	}
	host, _, _ = strings.Cut(host, ":")
	if !strings.EqualFold(host, "github.com") {
		return ""
	}
	rest = strings.TrimSuffix(strings.Trim(rest, "/"), ".git")
	owner, name, ok := strings.Cut(strings.ToLower(rest), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") || !repoPart.MatchString(owner) || !repoPart.MatchString(name) || strings.Trim(name, ".") == "" {
		return ""
	}
	return owner + "/" + name
}

var repoPart = regexp.MustCompile(`^[a-z0-9._-]+$`)
