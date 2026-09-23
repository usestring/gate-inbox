// Command thirdpartylicences regenerates the Go dependency section of
// LICENSES/THIRD-PARTY.md and the licence files under LICENSES/third_party.
//
// It classifies every licence file at a module's root with
// github.com/google/licensecheck, so the result depends only on the module
// cache and never on a network lookup. Run it through
// scripts/third-party-licences.sh from the repository root.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/licensecheck"
)

const (
	beginMarker = "<!-- BEGIN GENERATED: scripts/third-party-licences.sh -->"
	endMarker   = "<!-- END GENERATED -->"
)

var (
	goosList     = []string{"linux", "darwin", "windows"}
	licenceFile  = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents|unlicense|authors|contributors|[a-z]+-licen[cs]e)([.-].*)?$`)
	copyleftIDs  = regexp.MustCompile(`^(A?GPL|LGPL|MPL|EPL|CDDL|EUPL|OSL|CPAL|SSPL|CC-BY-SA|CC-BY-NC)`)
	minCoverage  = 20.0
	thirdPartyMD = filepath.Join("LICENSES", "THIRD-PARTY.md")
	copiesDir    = filepath.Join("LICENSES", "third_party")
)

type module struct {
	Path    string
	Version string
	Dir     string
	Main    bool
	Replace *module
}

type row struct {
	mod      module
	scope    string
	licences []string
	files    []string
	flag     string
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := run(*root); err != nil {
		fmt.Fprintln(os.Stderr, "thirdpartylicences:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	mods, err := listModules(root)
	if err != nil {
		return err
	}
	linked := map[string][]string{}
	for _, goos := range goosList {
		paths, err := goList(root, goos, false)
		if err != nil {
			return err
		}
		for _, p := range paths {
			linked[p] = append(linked[p], goos)
		}
	}
	testOnly := map[string]bool{}
	paths, err := goList(root, "", true)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if _, ok := linked[p]; !ok {
			testOnly[p] = true
		}
	}

	var rows []row
	for _, m := range mods {
		if m.Main {
			continue
		}
		r := row{mod: m, scope: "module graph only"}
		switch {
		case linked[m.Path] != nil:
			r.scope = "linked (" + strings.Join(linked[m.Path], ", ") + ")"
		case testOnly[m.Path]:
			r.scope = "tests only"
		}
		if err := classify(&r); err != nil {
			return err
		}
		rows = append(rows, r)
	}

	if err := os.RemoveAll(filepath.Join(root, copiesDir)); err != nil {
		return err
	}
	for _, r := range rows {
		if !strings.HasPrefix(r.scope, "linked") {
			continue
		}
		for _, f := range r.files {
			if err := copyFile(filepath.Join(r.mod.Dir, f), filepath.Join(root, copiesDir, r.mod.Path, f)); err != nil {
				return err
			}
		}
	}
	return writeSection(root, render(rows))
}

func listModules(root string) ([]module, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m all: %w", err)
	}
	var mods []module
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var m module
		if err := dec.Decode(&m); err != nil {
			return nil, err
		}
		if m.Replace != nil {
			m.Version, m.Dir = m.Replace.Version, m.Replace.Dir
		}
		mods = append(mods, m)
	}
	return mods, nil
}

func goList(root, goos string, tests bool) ([]string, error) {
	args := []string{"list", "-deps", "-f", "{{if not .Standard}}{{with .Module}}{{.Path}}{{end}}{{end}}"}
	if tests {
		args = append(args, "-test")
	}
	cmd := exec.Command("go", append(args, "./...")...)
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if goos != "" {
		cmd.Env = append(cmd.Env, "GOOS="+goos, "CGO_ENABLED=0")
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps (GOOS=%s): %w", goos, err)
	}
	seen := map[string]bool{}
	var paths []string
	for _, p := range strings.Fields(string(out)) {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func classify(r *row) error {
	if r.mod.Dir == "" {
		r.flag = "NOT DOWNLOADED: run scripts/third-party-licences.sh"
		return nil
	}
	entries, err := os.ReadDir(r.mod.Dir)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !licenceFile.MatchString(e.Name()) {
			continue
		}
		r.files = append(r.files, e.Name())
		text, err := os.ReadFile(filepath.Join(r.mod.Dir, e.Name()))
		if err != nil {
			return err
		}
		cov := licensecheck.Scan(text)
		if cov.Percent < minCoverage {
			continue
		}
		for _, m := range cov.Match {
			ids[m.ID] = true
		}
	}
	for id := range ids {
		r.licences = append(r.licences, id)
		if copyleftIDs.MatchString(id) {
			r.flag = "COPYLEFT"
		}
	}
	sort.Strings(r.licences)
	if len(r.licences) == 0 {
		r.flag = "UNKNOWN LICENCE"
	}
	return nil
}

func render(rows []row) string {
	var b strings.Builder
	counts := map[string]int{}
	linked := 0
	for _, r := range rows {
		if strings.HasPrefix(r.scope, "linked") {
			linked++
			counts[strings.Join(r.licences, " + ")]++
		}
	}
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	fmt.Fprintf(&b, "%d modules in `go list -m all` besides this one; %d are linked into a binary on at least one of %s.\n\n",
		len(rows), linked, strings.Join(goosList, ", "))
	b.WriteString("Linked modules by detected licence:\n\n")
	for _, k := range keys {
		name := k
		if name == "" {
			name = "UNKNOWN"
		}
		fmt.Fprintf(&b, "- %s: %d\n", name, counts[k])
	}
	var flagged []string
	for _, r := range rows {
		if r.flag != "" {
			flagged = append(flagged, fmt.Sprintf("%s (%s, %s)", r.mod.Path, r.flag, r.scope))
		}
	}
	if len(flagged) == 0 {
		b.WriteString("\nNo copyleft or unknown licence detected in any module.\n")
	} else {
		b.WriteString("\nFlagged:\n\n")
		for _, f := range flagged {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	b.WriteString("\n| Module | Version | Licence | Source | Notes |\n|---|---|---|---|---|\n")
	for _, r := range rows {
		lic := strings.Join(r.licences, " + ")
		if lic == "" {
			lic = "UNKNOWN"
		}
		notes := r.scope
		if len(r.files) > 0 {
			notes += "; files: " + strings.Join(r.files, ", ")
		}
		if strings.HasPrefix(r.scope, "linked") && len(r.files) > 0 {
			notes += "; copied to `" + filepath.ToSlash(filepath.Join(copiesDir, r.mod.Path)) + "/`"
		}
		if r.flag != "" {
			notes += "; **" + r.flag + "**"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | https://pkg.go.dev/%s@%s?tab=licenses | %s |\n",
			r.mod.Path, r.mod.Version, lic, r.mod.Path, r.mod.Version, notes)
	}
	return b.String()
}

func writeSection(root, section string) error {
	path := filepath.Join(root, thirdPartyMD)
	doc, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	begin := bytes.Index(doc, []byte(beginMarker))
	end := bytes.Index(doc, []byte(endMarker))
	if begin < 0 || end < begin {
		return fmt.Errorf("%s: generated-section markers missing", thirdPartyMD)
	}
	var out bytes.Buffer
	out.Write(doc[:begin+len(beginMarker)])
	out.WriteString("\n\n")
	out.WriteString(section)
	out.WriteString("\n")
	out.Write(doc[end:])
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
