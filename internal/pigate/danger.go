package pigate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"mvdan.cc/sh/v3/syntax"
)

// The deny-by-exception gate's deterministic list: the obvious cases of each
// danger class, caught without a classifier call, so a classifier that is
// down, slow or wrong never lets them through. A call on the list asks. What
// the list cannot read for certain it leaves to the classifier; it does not
// try to be a shell sandbox, since pi's bash already runs in one.

// dangerous reports whether call is on the list, and what it is.
func dangerous(call grant.Call) (string, bool) {
	switch {
	case call.Tool == "Bash":
		command, _ := call.Input["command"].(string)
		return dangerousCommand(call, command, 0)
	case call.Tool == "Write" || call.Tool == "Edit" || call.Tool == "NotebookEdit":
		path := toolPath(call)
		if path == "" {
			return "", false
		}
		if secretPath(call, path) || startupPath(call, path) {
			return "a write to a credential, shell start-up or settings file (" + path + ")", true
		}
		if !scratch(call, path) {
			return "a write outside the working directory (" + path + ")", true
		}
	case slices.Contains(readOnlyTools, call.Tool):
		if path := toolPath(call); path != "" && secretPath(call, path) {
			return "a read of a credential file (" + path + ")", true
		}
	case strings.HasPrefix(call.Tool, "mcp__"):
		return dangerousMCP(call.Tool)
	}
	return "", false
}

func toolPath(call grant.Call) string {
	for _, key := range []string{"path", "file_path", "notebook_path"} {
		if p, _ := call.Input[key].(string); p != "" {
			return p
		}
	}
	return ""
}

// dangerousMCP lists an MCP tool whose name says it sends, publishes,
// deletes or changes something outside the session. A tool its server marks
// read-only was settled by the fast path before this. The board's own
// server's tools act on the board, and only its grants are listed.
func dangerousMCP(tool string) (string, bool) {
	rest := strings.TrimPrefix(tool, "mcp__")
	server, name, ok := strings.Cut(rest, "__")
	if !ok {
		name = rest
	}
	if server == "gate-inbox" || server == "gate_inbox" {
		if name == "grant_permission" {
			return "a permission grant (" + tool + ")", true
		}
		return "", false
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return r == '_' || r == '-' || r == '.' }) {
		if mcpDangerWords[word] {
			return "an MCP call that acts outside the session (" + tool + ", " + word + ")", true
		}
	}
	return "", false
}

var mcpDangerWords = map[string]bool{
	"send": true, "post": true, "publish": true, "reply": true, "email": true, "tweet": true,
	"delete": true, "remove": true, "drop": true, "destroy": true, "purge": true,
	"deploy": true, "merge": true, "transfer": true, "pay": true, "charge": true, "refund": true,
	"invite": true, "share": true, "grant": true, "revoke": true,
}

// maxDepth bounds how deep bash -c, eval and find -exec are read into.
const maxDepth = 4

// dangerousCommand reads every simple command in a shell command, in order,
// keeping track of the directory a cd leaves it in.
func dangerousCommand(call grant.Call, command string, depth int) (string, bool) {
	if depth > maxDepth || strings.ContainsRune(command, 0) {
		return "a command nested too deep to read", true
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return "", false // not shell the list can read: the classifier judges it
	}
	r := commandReader{call: call, dir: call.Cwd, depth: depth}
	syntax.Walk(file, func(node syntax.Node) bool {
		if r.why != "" {
			return false
		}
		switch n := node.(type) {
		case *syntax.Stmt:
			for _, rd := range n.Redirs {
				if writesTo(rd.Op) && rd.Word != nil {
					if target, ok := r.literal(rd.Word); ok && (secretPath(r.at(), target) || startupPath(r.at(), target)) {
						r.why = "a write to a credential, shell start-up or settings file (" + target + ")"
					}
				}
			}
		case *syntax.CallExpr:
			r.simple(n)
		}
		return true
	})
	return r.why, r.why != ""
}

func writesTo(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.ClbOut, syntax.RdrInOut:
		return true
	}
	return false
}

type commandReader struct {
	call grant.Call
	// dir is where the commands read so far leave the shell, "" once a cd
	// to somewhere unreadable has made it unknown.
	dir   string
	depth int
	why   string
	// raw is the simple command being read, word by word, as unquoted
	// gives it.
	raw []string
}

// at is the call as the command being read sees it, from the directory the
// commands before it left.
func (r *commandReader) at() grant.Call {
	c := r.call
	c.Cwd = r.dir
	return c
}

// literal is a word's value when it has no expansion other than a leading
// tilde, which is kept for the path checks to read as the home directory.
func (r *commandReader) literal(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

// words is a simple command's words, with "" for a word that is not literal.
func (r *commandReader) words(call *syntax.CallExpr) []string {
	out := make([]string, len(call.Args))
	for i, w := range call.Args {
		out[i], _ = r.literal(w)
	}
	return out
}

func (r *commandReader) simple(call *syntax.CallExpr) {
	words := r.words(call)
	if len(words) == 0 {
		return
	}
	r.raw = make([]string, len(call.Args))
	for i, w := range call.Args {
		r.raw[i] = unquoted(w)
	}
	if why, ok := r.judge(words); ok {
		r.why = why
	}
}

// unquoted is a word's source with one layer of quotes taken off, which is
// the script bash -c or eval would read, expansions left in place for the
// list to find unreadable.
func unquoted(w *syntax.Word) string {
	var b strings.Builder
	if syntax.NewPrinter().Print(&b, w) != nil {
		return ""
	}
	text := b.String()
	if len(w.Parts) == 1 {
		switch w.Parts[0].(type) {
		case *syntax.SglQuoted, *syntax.DblQuoted:
			if len(text) >= 2 {
				return text[1 : len(text)-1]
			}
		}
	}
	return text
}

// wrappers run the rest of their words as a command, after their own options.
var wrappers = map[string]bool{
	"env": true, "nohup": true, "time": true, "nice": true, "ionice": true, "command": true,
	"exec": true, "builtin": true, "stdbuf": true, "caffeinate": true,
}

func (r *commandReader) judge(words []string) (string, bool) {
	for len(words) > 0 && (wrappers[words[0]] || words[0] == "timeout") {
		timeout := words[0] == "timeout"
		words = words[1:]
		for len(words) > 0 && (strings.HasPrefix(words[0], "-") || strings.Contains(words[0], "=")) {
			words = words[1:]
		}
		if timeout && len(words) > 0 {
			words = words[1:] // the duration
		}
	}
	if len(words) == 0 {
		return "", false
	}
	// raw is words' source, when words are the tail of the command read.
	var raw []string
	if off := len(r.raw) - len(words); r.raw != nil && off >= 0 {
		raw = r.raw[off:]
	}
	script := func(i int) string {
		if raw != nil {
			return raw[i]
		}
		return words[i]
	}
	if words[0] == "" && r.depth > 0 {
		return "a command the gate cannot read inside a shell script", true
	}
	name := filepath.Base(words[0])
	args := words[1:]
	for _, a := range args {
		if a != "" && !strings.HasPrefix(a, "-") && secretPath(r.at(), a) {
			return "a command on a credential file (" + a + ")", true
		}
	}
	if slices.Contains([]string{"cp", "mv", "tee", "ln", "install", "rsync", "sed", "perl", "truncate"}, name) {
		for _, a := range args {
			if a != "" && !strings.HasPrefix(a, "-") && startupPath(r.at(), a) {
				return "a write to a shell start-up or agent settings file (" + a + ")", true
			}
		}
	}
	switch name {
	case "sudo", "doas", "su", "pkexec":
		return "a command run as another user (" + name + ")", true
	case "cd", "pushd":
		r.cd(args)
	case "bash", "sh", "zsh", "dash", "ksh":
		for i, a := range args {
			if a == "-c" && i+1 < len(args) {
				if script(i+2) == "" {
					return "a shell script the gate cannot read (" + name + " -c)", true
				}
				return dangerousCommand(r.at(), script(i+2), r.depth+1)
			}
		}
	case "eval":
		parts := make([]string, 0, len(args))
		for i := range args {
			parts = append(parts, script(i+1))
		}
		return dangerousCommand(r.at(), strings.Join(parts, " "), r.depth+1)
	case "xargs":
		return r.xargs(args)
	case "rm", "rmdir", "shred", "unlink", "srm":
		return r.deletes(name, args)
	case "find":
		return r.find(args)
	case "mkfs", "fdisk", "parted", "wipefs":
		return "a disk format (" + name + ")", true
	case "dd":
		for _, a := range args {
			if strings.HasPrefix(a, "of=") && !scratch(r.at(), strings.TrimPrefix(a, "of=")) {
				return "a dd write outside the working directory", true
			}
		}
	case "chmod", "chown", "chgrp", "setfacl", "chattr":
		for _, a := range args {
			if !strings.HasPrefix(a, "-") && strings.ContainsAny(a, "/~.") && !scratch(r.at(), a) {
				return "a permission change outside the working directory (" + name + " " + a + ")", true
			}
		}
	case "git":
		return r.git(args)
	case "gh":
		return ghWrites(args)
	case "kubectl", "oc":
		return subcommandIn(name, args, "a cluster change", "apply", "create", "delete", "edit", "patch", "replace",
			"scale", "rollout", "set", "label", "annotate", "drain", "cordon", "uncordon", "taint", "exec", "cp", "run", "expose", "autoscale")
	case "helm":
		return subcommandIn(name, args, "a cluster change", "install", "upgrade", "uninstall", "delete", "rollback")
	case "terraform", "tofu", "terragrunt":
		if why, ok := subcommandIn(name, args, "an infrastructure change", "apply", "destroy", "import", "taint", "untaint", "force-unlock"); ok {
			return why, ok
		}
		if i := firstWord(args); i >= 0 && args[i] == "state" {
			return subcommandIn(name, args[i+1:], "an infrastructure state change", "rm", "mv", "push", "replace-provider")
		}
	case "gcloud", "aws", "az", "gsutil", "bq", "doctl", "flyctl", "fly", "vercel", "netlify", "wrangler", "firebase", "heroku", "railway":
		return cloudWrites(name, args)
	case "curl", "http", "https", "xh":
		return httpWrites(name, args)
	case "wget":
		for _, a := range args {
			if strings.HasPrefix(a, "--post-") || strings.HasPrefix(a, "--method") || strings.HasPrefix(a, "--body-") {
				return "an outbound HTTP write (wget " + a + ")", true
			}
		}
	case "ssh", "scp", "sftp", "mosh":
		return "a remote command (" + name + ")", true
	case "rsync":
		for _, a := range args {
			if !strings.HasPrefix(a, "-") && strings.Contains(a, ":") {
				return "a copy to or from another machine (rsync)", true
			}
		}
	case "sendmail", "mail", "mailx", "mutt", "msmtp", "swaks":
		return "an outbound email (" + name + ")", true
	case "npm", "pnpm", "yarn", "bun", "cargo", "gem", "twine", "poetry", "dotnet", "mvn", "gradle":
		return subcommandIn(name, args, "a package publish", "publish", "push", "upload", "deploy", "unpublish", "yank")
	case "docker", "podman":
		return subcommandIn(name, args, "an image publish", "push", "login")
	case "crontab":
		if !slices.Contains(args, "-l") {
			return "a crontab change", true
		}
	case "systemctl", "launchctl", "service":
		return subcommandIn(name, args, "a system service change", "start", "stop", "restart", "enable", "disable",
			"mask", "kill", "load", "unload", "bootstrap", "bootout", "reload")
	case "shutdown", "reboot", "halt", "poweroff":
		return "a machine shutdown (" + name + ")", true
	case "psql", "mysql", "mongosh", "clickhouse-client", "redis-cli":
		for _, a := range args {
			if sqlWrites(a) {
				return "a database write (" + name + ")", true
			}
		}
	}
	if strings.Contains(strings.ToLower(words[0]), "slack") || strings.Contains(strings.ToLower(words[0]), "email") ||
		strings.Contains(strings.ToLower(words[0]), "gmail") || strings.Contains(strings.ToLower(words[0]), "whatsapp") {
		lower := strings.ToLower(words[0])
		for _, verb := range []string{"send", "post", "reply", "publish"} {
			if strings.Contains(lower, verb) {
				return "an outbound message (" + words[0] + ")", true
			}
		}
	}
	return "", false
}

func (r *commandReader) cd(args []string) {
	target := "~"
	for _, a := range args {
		if a == "" {
			r.dir = ""
			return
		}
		if !strings.HasPrefix(a, "-") {
			target = a
			break
		}
	}
	if r.dir == "" {
		return
	}
	resolved := expandPath(r.at(), target)
	if resolved == "" {
		r.dir = ""
		return
	}
	r.dir = resolved
}

// deletes lists a deletion that reaches outside the working directory and
// the temporary directory, or takes the working directory itself.
func (r *commandReader) deletes(name string, args []string) (string, bool) {
	return r.deletesFrom(name, args, false)
}

// deletesFrom is deletes, where roots says the paths are where the deletion
// starts (find's), so the working directory itself may be one.
func (r *commandReader) deletesFrom(name string, args []string, roots bool) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		if a == "" || r.dir == "" {
			return "a deletion of a path the gate cannot read (" + name + ")", true
		}
		if !scratch(r.at(), a) || !roots && filepath.Clean(expandPath(r.at(), a)) == filepath.Clean(r.call.Cwd) {
			return "a deletion outside the working directory (" + name + " " + a + ")", true
		}
	}
	return "", false
}

func (r *commandReader) xargs(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if slices.Contains([]string{"-I", "-n", "-P", "-L", "-d", "-E", "-s"}, a) {
				i++
			}
			continue
		}
		rest := args[i:]
		switch filepath.Base(rest[0]) {
		case "rm", "rmdir", "shred", "unlink":
			return "a deletion of paths another command lists (xargs " + rest[0] + ")", true
		}
		return r.judge(rest)
	}
	return "", false
}

func (r *commandReader) find(args []string) (string, bool) {
	// An -exec command's words are not a tail of find's own, so the
	// commands it runs are read without their source.
	saved := r.raw
	r.raw = nil
	defer func() { r.raw = saved }()
	var roots []string
	i := 0
	for i < len(args) && slices.Contains([]string{"-H", "-L", "-P"}, args[i]) {
		i++
	}
	for ; i < len(args) && !strings.HasPrefix(args[i], "-") && args[i] != "(" && args[i] != "!"; i++ {
		roots = append(roots, args[i])
	}
	if len(roots) == 0 {
		roots = []string{"."}
	}
	deletes := false
	for j := i; j < len(args); j++ {
		switch args[j] {
		case "-delete":
			deletes = true
		case "-exec", "-execdir", "-ok", "-okdir":
			end := j + 1
			for end < len(args) && args[end] != ";" && args[end] != "+" {
				end++
			}
			inner := slices.DeleteFunc(slices.Clone(args[j+1:end]), func(s string) bool { return s == "{}" })
			if len(inner) > 0 {
				switch filepath.Base(inner[0]) {
				case "rm", "rmdir", "shred", "unlink":
					deletes = true
				default:
					if why, ok := r.judge(inner); ok {
						return why, ok
					}
				}
			}
			j = end
		}
	}
	if deletes {
		if why, ok := r.deletesFrom("find", roots, true); ok {
			return why, ok
		}
	}
	return "", false
}

func (r *commandReader) git(args []string) (string, bool) {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if args[i] == "-C" || args[i] == "-c" {
			i++
		}
		i++
	}
	if i >= len(args) {
		return "", false
	}
	sub, rest := args[i], args[i+1:]
	has := func(flags ...string) bool {
		return slices.ContainsFunc(rest, func(a string) bool {
			return slices.ContainsFunc(flags, func(f string) bool { return a == f || strings.HasPrefix(a, f+"=") })
		})
	}
	switch sub {
	case "push":
		return r.push(rest)
	case "reset":
		if has("--hard", "--merge", "--keep") {
			return "a git reset that discards work (git reset " + strings.Join(rest, " ") + ")", true
		}
	case "clean":
		if slices.ContainsFunc(rest, func(a string) bool {
			return a == "--force" || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "f")
		}) {
			return "a git clean that deletes untracked files", true
		}
	case "stash":
		if len(rest) > 0 && (rest[0] == "drop" || rest[0] == "clear") {
			return "a stash deletion (git stash " + rest[0] + ")", true
		}
	case "branch":
		if has("-D", "--delete", "-d", "--force", "-f", "-M") {
			return "a branch deletion or overwrite (git branch " + strings.Join(rest, " ") + ")", true
		}
	case "config":
		if has("--global", "--system") && !has("--get", "--get-all", "--list", "-l", "--get-regexp") {
			return "a change to the global git configuration", true
		}
	case "update-ref", "filter-branch", "filter-repo", "replace":
		return "a history rewrite (git " + sub + ")", true
	case "remote":
		if len(rest) > 0 && slices.Contains([]string{"add", "set-url", "remove", "rm", "rename"}, rest[0]) {
			return "a remote change (git remote " + rest[0] + ")", true
		}
	}
	return "", false
}

// sharedBranch is a branch others build or deploy from.
func sharedBranch(ref string) bool {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	return ref == "main" || ref == "master" || ref == "trunk" || ref == "production" || ref == "prod" ||
		strings.HasPrefix(ref, "deploy-") || strings.HasPrefix(ref, "release/")
}

func (r *commandReader) push(args []string) (string, bool) {
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "":
			return "a push the gate cannot read", true
		case a == "--force" || strings.HasPrefix(a, "--force-with-lease") || a == "--force-if-includes" ||
			a == "--mirror" || a == "--delete" || a == "--prune" || a == "--all" ||
			strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "fd"):
			return "a force, delete or mirror push (git push " + strings.Join(args, " ") + ")", true
		case a == "-o" || a == "--push-option" || a == "--repo" || a == "--receive-pack" || a == "--exec":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) <= 1 {
		// The current branch goes to its upstream.
		if r.dir == "" {
			return "a push from a directory the gate cannot read", true
		}
		if branch := currentBranch(r.dir); branch == "" || sharedBranch(branch) {
			return "a push of the current branch (" + orUnknown(branch) + ")", true
		}
		return "", false
	}
	for _, spec := range positional[1:] {
		if strings.HasPrefix(spec, "+") || strings.HasPrefix(spec, ":") {
			return "a force or delete push (" + spec + ")", true
		}
		src, dst, found := strings.Cut(spec, ":")
		if !found {
			dst = src
		}
		if dst == "HEAD" || src == "HEAD" && !found {
			branch := currentBranch(r.dir)
			if branch == "" || sharedBranch(branch) {
				return "a push of HEAD (" + orUnknown(branch) + ")", true
			}
			continue
		}
		if sharedBranch(dst) {
			return "a push to a shared branch (" + dst + ")", true
		}
	}
	return "", false
}

func orUnknown(s string) string {
	if s == "" {
		return "branch unknown"
	}
	return s
}

// currentBranch is the branch checked out in dir, or "" when there is none
// or git cannot say in time.
var currentBranch = func(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-replace-objects", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func firstWord(args []string) int {
	return slices.IndexFunc(args, func(a string) bool { return a != "" && !strings.HasPrefix(a, "-") })
}

// subcommandIn lists a command whose first word after its options is one of
// subs.
func subcommandIn(name string, args []string, what string, subs ...string) (string, bool) {
	i := firstWord(args)
	if i >= 0 && slices.Contains(subs, args[i]) {
		return what + " (" + name + " " + args[i] + ")", true
	}
	return "", false
}

func ghWrites(args []string) (string, bool) {
	i := firstWord(args)
	if i < 0 {
		return "", false
	}
	group, rest := args[i], args[i+1:]
	if group == "api" {
		for j, a := range rest {
			method := ""
			switch {
			case a == "-X" || a == "--method":
				if j+1 < len(rest) {
					method = rest[j+1]
				}
			case strings.HasPrefix(a, "--method="):
				method = strings.TrimPrefix(a, "--method=")
			case strings.HasPrefix(a, "-X") && len(a) > 2:
				method = a[2:]
			case a == "-f" || a == "-F" || a == "--field" || a == "--raw-field" || a == "--input":
				method = "POST"
			}
			if method != "" && !strings.EqualFold(method, "GET") && !strings.EqualFold(method, "HEAD") {
				return "a GitHub API write (gh api " + strings.ToUpper(method) + ")", true
			}
		}
		return "", false
	}
	j := firstWord(rest)
	if j < 0 {
		return "", false
	}
	action := rest[j]
	writes := map[string][]string{
		"pr":       {"comment", "review", "close", "reopen", "edit", "ready", "lock", "unlock"},
		"issue":    {"create", "comment", "close", "reopen", "edit", "delete", "transfer", "lock", "pin"},
		"release":  {"create", "delete", "edit", "upload"},
		"repo":     {"create", "delete", "archive", "rename", "edit", "fork", "sync"},
		"secret":   {"set", "delete"},
		"variable": {"set", "delete"},
		"workflow": {"run", "enable", "disable"},
		"run":      {"cancel", "rerun", "delete"},
		"gist":     {"create", "edit", "delete"},
		"ssh-key":  {"add", "delete"},
		"gpg-key":  {"add", "delete"},
		"auth":     {"login", "logout", "refresh", "token", "setup-git"},
	}
	if slices.Contains(writes[group], action) {
		return "a GitHub write (gh " + group + " " + action + ")", true
	}
	return "", false
}

var cloudWriteVerbs = map[string]bool{
	"create": true, "delete": true, "update": true, "deploy": true, "set": true, "patch": true, "apply": true,
	"add-iam-policy-binding": true, "remove-iam-policy-binding": true, "set-iam-policy": true,
	"reset": true, "start": true, "stop": true, "resize": true, "import": true, "ssh": true, "scp": true,
	"enable": true, "disable": true, "rollback": true, "cancel": true, "remove": true, "rm": true, "mv": true,
	"cp": true, "rsync": true, "sync": true, "mk": true, "mb": true, "rb": true, "load": true, "insert": true,
	"put": true, "terminate": true, "reboot": true, "invoke": true, "publish": true, "submit": true,
	"activate-service-account": true, "login": true, "print-access-token": true, "print-identity-token": true,
	"setmeta": true, "acl": true, "iam": true, "destroy": true, "scale": true, "promote": true, "--prod": true,
	"secrets": true, "upgrade": true, "restart": true,
}

func cloudWrites(name string, args []string) (string, bool) {
	var positional []string
	for _, a := range args {
		if a != "" && !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}
	for _, a := range args {
		verb := strings.ToLower(a)
		if verb == "cp" || verb == "rsync" || verb == "sync" {
			// A copy down from the cloud only reads it; one up writes it.
			if last := positional[len(positional)-1]; strings.Contains(last, "://") {
				return "a cloud upload (" + name + " " + a + " to " + last + ")", true
			}
			continue
		}
		if cloudWriteVerbs[verb] || name == "aws" && slices.ContainsFunc([]string{"put-", "delete-", "create-", "update-",
			"terminate-", "run-", "start-", "stop-", "modify-", "attach-", "detach-", "invoke"}, func(p string) bool {
			return strings.HasPrefix(verb, p)
		}) {
			return "a cloud change (" + name + " " + a + ")", true
		}
		if name == "bq" && verb == "query" && slices.ContainsFunc(args, sqlWrites) {
			return "a BigQuery write (bq query)", true
		}
	}
	return "", false
}

func httpWrites(name string, args []string) (string, bool) {
	if name != "curl" {
		return httpieWrites(name, args)
	}
	writes := false
	var hosts []string
	for i, a := range args {
		switch {
		case a == "-X" || a == "--request":
			if i+1 < len(args) && !strings.EqualFold(args[i+1], "GET") && !strings.EqualFold(args[i+1], "HEAD") {
				writes = true
			}
		case strings.HasPrefix(a, "-X") && len(a) > 2:
			if m := a[2:]; !strings.EqualFold(m, "GET") && !strings.EqualFold(m, "HEAD") {
				writes = true
			}
		case strings.HasPrefix(a, "--request="):
			if m := strings.TrimPrefix(a, "--request="); !strings.EqualFold(m, "GET") && !strings.EqualFold(m, "HEAD") {
				writes = true
			}
		case a == "-d" || strings.HasPrefix(a, "--data") || a == "-F" || strings.HasPrefix(a, "--form") ||
			a == "-T" || a == "--upload-file" || a == "--json" || strings.HasPrefix(a, "-d") && len(a) > 2:
			writes = true
		case strings.Contains(a, "://"):
			hosts = append(hosts, a)
		}
	}
	if !writes {
		return "", false
	}
	for _, h := range hosts {
		if !localURL(h) {
			return "an outbound HTTP write (" + name + " to " + h + ")", true
		}
	}
	if len(hosts) == 0 {
		return "an outbound HTTP write (" + name + ")", true
	}
	return "", false
}

// httpieWrites reads httpie's and xh's [METHOD] URL [ITEM...] form, where a
// data item (a=b, a:=b, a@file) makes the request a write.
func httpieWrites(name string, args []string) (string, bool) {
	var positional []string
	for _, a := range args {
		if a != "" && !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}
	if len(positional) == 0 {
		return "", false
	}
	method := ""
	if m := strings.ToUpper(positional[0]); slices.Contains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, m) {
		method, positional = m, positional[1:]
	}
	if len(positional) == 0 {
		return "", false
	}
	url, items := positional[0], positional[1:]
	writes := method != "" && method != "GET" && method != "HEAD" && method != "OPTIONS" ||
		method == "" && slices.ContainsFunc(items, func(i string) bool { return strings.ContainsAny(i, "=@") && !strings.Contains(i, "==") })
	if !strings.Contains(url, "://") {
		url = "http://" + url
	}
	if writes && !localURL(url) {
		return "an outbound HTTP write (" + name + " to " + positional[0] + ")", true
	}
	return "", false
}

func localURL(raw string) bool {
	_, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return false
	}
	host := rest
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if strings.HasPrefix(host, "[") {
		host, _, _ = strings.Cut(strings.TrimPrefix(host, "["), "]")
	} else if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost")
}

func sqlWrites(arg string) bool {
	upper := " " + strings.ToUpper(arg) + " "
	for _, kw := range []string{" INSERT ", " UPDATE ", " DELETE ", " DROP ", " ALTER ", " TRUNCATE ", " CREATE ",
		" GRANT ", " REVOKE ", " MERGE ", " REPLACE "} {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

// expandPath is path, read from call.Cwd, absolute and with a leading tilde
// as the home directory, or "" when it cannot be placed.
func expandPath(call grant.Call, path string) string {
	home := call.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		if home == "" {
			return ""
		}
		return filepath.Join(home, path[2:])
	case strings.HasPrefix(path, "~"):
		return "" // another user's home
	case filepath.IsAbs(path):
		return filepath.Clean(path)
	case call.Cwd == "" || !filepath.IsAbs(call.Cwd):
		return ""
	}
	return filepath.Join(call.Cwd, path)
}

// tempRoots are the temporary directories a deletion or write may reach.
func tempRoots() []string {
	roots := []string{os.TempDir(), "/tmp", "/var/tmp"}
	if t := os.Getenv("TMPDIR"); t != "" {
		roots = append(roots, t)
	}
	return roots
}

// scratch reports whether path is inside the working directory or a
// temporary directory.
func scratch(call grant.Call, path string) bool {
	abs := expandPath(call, path)
	if abs == "" {
		return false
	}
	if call.Cwd != "" && filepath.IsAbs(call.Cwd) && within(call.Cwd, abs) {
		return true
	}
	for _, root := range tempRoots() {
		if root != "" && filepath.IsAbs(root) && within(root, abs) && filepath.Clean(abs) != filepath.Clean(root) {
			return true
		}
	}
	return false
}

// secretPath reports a credential store, private key or shell start-up
// file, wherever it is.
func secretPath(call grant.Call, path string) bool {
	abs := expandPath(call, path)
	if abs == "" {
		if strings.HasPrefix(path, "~") {
			return false
		}
		abs = path
	}
	home := call.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	base := filepath.Base(abs)
	if strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") || strings.HasPrefix(base, "id_ecdsa") ||
		strings.HasSuffix(base, ".pem") && !strings.Contains(abs, "/node_modules/") ||
		base == ".netrc" || base == ".credentials.json" || base == ".git-credentials" || base == ".pgpass" || base == ".pypirc" || base == "authorized_keys" {
		return true
	}
	if home == "" {
		return false
	}
	return underHome(home, abs, ".ssh", ".aws", ".gnupg", ".kube", ".docker/config.json", ".npmrc",
		".config/gcloud", ".azure", ".config/gh/hosts.yml", ".claude.json")
}

// startupPath reports a file whose change runs code or loosens permissions
// in later sessions: shell start-up files and Claude Code's settings.
func startupPath(call grant.Call, path string) bool {
	abs := expandPath(call, path)
	if abs == "" {
		return false
	}
	home := call.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return home != "" && underHome(home, abs, ".bashrc", ".bash_profile", ".bash_login", ".profile", ".zshrc",
		".zprofile", ".zshenv", ".zlogin", ".config/fish", ".claude/settings.json", ".claude/settings.local.json",
		".config/gate-inbox/config.toml")
}

func underHome(home, abs string, rels ...string) bool {
	for _, rel := range rels {
		root := filepath.Join(home, rel)
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
