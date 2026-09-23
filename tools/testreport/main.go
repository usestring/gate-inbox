// Command testreport turns `go test -json` into a report that says what CI
// did not run.
//
// A large part of this module's suite drives real tmux servers, and every one
// of those tests skips itself when tmux is missing rather than failing. A
// workflow that only checks the exit code is therefore green both when the
// suite ran and when almost none of it did. This command closes that gap: it
// prints every skipped test with the reason the test itself gave, and fails
// the run when a test skips that the committed allowlist does not name.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// modulePath is trimmed off package names so a report and an allowlist read
// as repository paths rather than as import paths.
const modulePath = "github.com/usestring/gate-inbox"

// event is the subset of test2json's record this command reads.
type event struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// result is one test's outcome, keyed by the name the allowlist uses.
type result struct {
	name   string
	action string
	reason string
	// output is kept so a failure can be read here rather than by rerunning
	// the suite. Only the tail is kept: a single tmux test can print a whole
	// captured pane.
	output []string
}

// outputTail is how many lines of a failing test's output the report keeps.
const outputTail = 60

func (r *result) record(line string) {
	r.output = append(r.output, line)
	if len(r.output) > outputTail {
		r.output = r.output[len(r.output)-outputTail:]
	}
}

// reasonLine matches the line testing prints for a Skip or a Fatal, which is
// where the test's own explanation of itself lives.
var reasonLine = regexp.MustCompile(`^\s*[\w.\-]+\.go:\d+:\s*(.*)$`)

func main() {
	allow := flag.String("allow", "", "file naming the tests allowed to skip")
	summary := flag.String("summary", "", "file to append a Markdown summary to")
	flag.Parse()

	results, packageFailures, err := parse(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testreport:", err)
		os.Exit(2)
	}

	report, ok := render(results, packageFailures, *allow)
	fmt.Print(report)
	if *summary != "" {
		if err := appendSummary(*summary, report); err != nil {
			fmt.Fprintln(os.Stderr, "testreport:", err)
		}
	}
	if !ok {
		os.Exit(1)
	}
}

// parse folds the event stream into one result per test. A test that skips
// prints its reason before the skip record, so the newest reason-shaped
// output line seen for a test is the one that belongs to its outcome.
func parse(r io.Reader) ([]result, []string, error) {
	byName := map[string]*result{}
	var order []string
	var packageFailures []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev event
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		pkg := strings.TrimPrefix(strings.TrimPrefix(ev.Package, modulePath), "/")
		if pkg == "" {
			pkg = "."
		}
		if ev.Test == "" {
			// A package that fails to build reports no test at all, so the
			// failure would otherwise leave no trace in this report.
			if ev.Action == "fail" {
				packageFailures = append(packageFailures, pkg)
			}
			continue
		}
		name := pkg + " " + ev.Test
		res, seen := byName[name]
		if !seen {
			res = &result{name: name}
			byName[name] = res
			order = append(order, name)
		}
		switch ev.Action {
		case "output":
			text := strings.TrimRight(ev.Output, "\n")
			res.record(text)
			if match := reasonLine.FindStringSubmatch(text); match != nil {
				res.reason = match[1]
			}
		case "pass", "fail", "skip":
			res.action = ev.Action
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	results := make([]result, 0, len(order))
	for _, name := range order {
		results = append(results, *byName[name])
	}
	sort.Slice(results, func(i, j int) bool { return results[i].name < results[j].name })
	sort.Strings(packageFailures)
	return results, packageFailures, nil
}

// render writes the report and reports whether the run should be accepted.
func render(results []result, packageFailures []string, allowPath string) (string, bool) {
	var passed, failed, skipped []result
	for _, res := range results {
		switch res.action {
		case "pass":
			passed = append(passed, res)
		case "fail":
			failed = append(failed, res)
		case "skip":
			skipped = append(skipped, res)
		}
	}

	allowed, allowErr := readAllowlist(allowPath)
	var unexpected, stale, unknown []string
	if allowErr == nil && allowPath != "" {
		ran := map[string]bool{}
		for _, res := range results {
			ran[res.name] = true
		}
		didSkip := map[string]bool{}
		for _, res := range skipped {
			didSkip[res.name] = true
			if _, ok := allowed[res.name]; !ok {
				unexpected = append(unexpected, res.name)
			}
		}
		for name, always := range allowed {
			switch {
			case !ran[name]:
				unknown = append(unknown, name)
			case always && !didSkip[name]:
				stale = append(stale, name)
			}
		}
		sort.Strings(stale)
		sort.Strings(unknown)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "## Test report\n\n%d passed, %d failed, %d skipped.\n\n",
		len(passed), len(failed), len(skipped))

	// A run that reported nothing is a run that did not happen: go test died
	// before writing, or the stream was truncated. Counting outcomes would
	// call that clean, which is the same false green this command exists to
	// stop, arrived at from the other end. A package that failed to build
	// also reports no test, but it does report itself, so it is not this.
	if len(results) == 0 && len(packageFailures) == 0 {
		out.WriteString("### No test results\n\nThe stream carried no test at all, " +
			"so nothing here ran.\n\n")
		return out.String(), false
	}

	if len(failed) > 0 {
		fmt.Fprintf(&out, "### Failed (%d)\n\n", len(failed))
		for _, res := range failed {
			fmt.Fprintf(&out, "- `%s`\n", res.name)
		}
		out.WriteString("\n")
		for _, res := range failed {
			fmt.Fprintf(&out, "<details><summary>%s</summary>\n\n```\n%s\n```\n\n</details>\n\n",
				res.name, strings.Join(res.output, "\n"))
		}
	}
	if len(packageFailures) > 0 {
		fmt.Fprintf(&out, "### Packages that failed without reporting a test (%d)\n\n", len(packageFailures))
		for _, pkg := range packageFailures {
			fmt.Fprintf(&out, "- `%s`\n", pkg)
		}
		out.WriteString("\n")
	}

	fmt.Fprintf(&out, "### Not covered by this run: %d skipped\n\n", len(skipped))
	if len(skipped) == 0 {
		out.WriteString("Nothing skipped.\n\n")
	}
	for _, res := range skipped {
		reason := res.reason
		if reason == "" {
			reason = "no reason given"
		}
		fmt.Fprintf(&out, "- `%s` — %s\n", res.name, reason)
	}
	out.WriteString("\n")

	ok := len(failed) == 0 && len(packageFailures) == 0
	if allowErr != nil {
		fmt.Fprintf(&out, "### Allowlist unreadable\n\n%v\n\n", allowErr)
		return out.String(), false
	}
	if len(unexpected) > 0 {
		ok = false
		fmt.Fprintf(&out, "### Skipped without being allowed (%d)\n\n", len(unexpected))
		out.WriteString("A test skipping in CI that the allowlist does not name means CI is " +
			"quietly covering less than it claims. Fix the environment or add the test to " +
			allowPath + " with the reason.\n\n")
		for _, name := range unexpected {
			fmt.Fprintf(&out, "- `%s`\n", name)
		}
		out.WriteString("\n")
	}
	if len(stale) > 0 {
		ok = false
		fmt.Fprintf(&out, "### Allowed to skip but ran (%d)\n\n", len(stale))
		out.WriteString("These entries no longer describe this run. Remove them from " +
			allowPath + " so the allowlist keeps saying what is really uncovered.\n\n")
		for _, name := range stale {
			fmt.Fprintf(&out, "- `%s`\n", name)
		}
		out.WriteString("\n")
	}
	if len(unknown) > 0 {
		ok = false
		fmt.Fprintf(&out, "### Allowed to skip but never seen (%d)\n\n", len(unknown))
		out.WriteString("These name no test in this run at all, so they are a rename, a " +
			"deletion or a typo. An entry that matches nothing silently allows nothing.\n\n")
		for _, name := range unknown {
			fmt.Fprintf(&out, "- `%s`\n", name)
		}
		out.WriteString("\n")
	}
	return out.String(), ok
}

// readAllowlist reads the committed set of tests permitted to skip, mapping
// each to whether it is expected to skip on every run. Anything after a '#'
// is the human reason and is not matched on. A name ending in '?' skips only
// on some hosts -- a control client that did not come up, a pane with no
// scrollback yet -- so it is allowed to skip without being required to,
// which is the difference between an unconditional gate and a conditional
// one and is why only the former can go stale.
func readAllowlist(path string) (map[string]bool, error) {
	allowed := map[string]bool{}
	if path == "" {
		return allowed, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if cut := strings.IndexByte(line, '#'); cut >= 0 {
			line = line[:cut]
		}
		name := strings.Join(strings.Fields(line), " ")
		if name == "" {
			continue
		}
		always := !strings.HasSuffix(name, "?")
		allowed[strings.TrimSuffix(name, "?")] = always
	}
	return allowed, nil
}

func appendSummary(path, report string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(report)
	return err
}
