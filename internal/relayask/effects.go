package relayask

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// What a shell command visibly does, in a line a person can decide on.
//
// A relayed permission prompt used to put the whole command in the question,
// and a 600-character loop is not something anyone reads before answering.
// So the question says what the command names: the files it deletes and
// writes, any network, cloud or production command, and the limits it runs
// under. This is a reading of the command's words, not of what the programs
// it runs do; that is what the full command in the preview is for, and the
// summary says "seen in the command" so it is not taken for more.

// Effects is what a command names.
type Effects struct {
	Deletes, Writes, Network, Cloud, Limits []string
	Root                                    bool
}

// summaryItems bounds each list in the summary.
const summaryItems = 3

// Summary is the effects as one or two sentences.
func (e Effects) Summary() string {
	var parts []string
	switch {
	case len(e.Deletes) > 0 && len(e.Writes) > 0:
		parts = append(parts, "deletes "+list(e.Deletes)+"; writes "+list(e.Writes))
	case len(e.Deletes) > 0:
		parts = append(parts, "deletes "+list(e.Deletes)+"; names nothing else it writes")
	case len(e.Writes) > 0:
		parts = append(parts, "writes "+list(e.Writes)+"; names nothing it deletes")
	default:
		parts = append(parts, "names no file it writes or deletes")
	}
	switch {
	case len(e.Network) == 0 && len(e.Cloud) == 0:
		parts = append(parts, "no network, cloud or production command")
	case len(e.Cloud) == 0:
		parts = append(parts, "network: "+list(e.Network)+"; no cloud or production command")
	case len(e.Network) == 0:
		parts = append(parts, "cloud or production: "+list(e.Cloud))
	default:
		parts = append(parts, "network: "+list(e.Network), "cloud or production: "+list(e.Cloud))
	}
	if e.Root {
		parts = append(parts, "runs as root")
	}
	if len(e.Limits) > 0 {
		parts = append(parts, "limited to "+list(e.Limits))
	}
	return "Seen in the command: " + strings.Join(parts, "; ") + "."
}

func list(items []string) string {
	if len(items) <= summaryItems {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:summaryItems], ", ") + fmt.Sprintf(" and %d more", len(items)-summaryItems)
}

// Read reads what command names.
func Read(command string) Effects {
	r := reader{vars: map[string]string{}}
	r.script(command)
	return r.out
}

// script reads each simple command of command, and of every $( ) and
// sh -c script inside it, in order.
func (r *reader) script(command string) {
	for _, simple := range splitCommands(command) {
		for _, inner := range simple.substitutions {
			r.script(inner)
		}
		r.simple(simple)
	}
}

// shells run their -c argument as a script.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true}

type reader struct {
	out  Effects
	vars map[string]string
	cwd  string
	seen map[string]bool
}

func (r *reader) add(to *[]string, item string) {
	if item == "" {
		return
	}
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	key := fmt.Sprintf("%p\x00%s", to, item)
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	*to = append(*to, item)
}

var (
	assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	variable   = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)
	production = regexp.MustCompile(`(?i)(?:^|[^a-z])(prod(?:uction)?)(?:$|[^a-z])`)
)

// keywords open a simple command without being its program.
var keywords = map[string]bool{"do": true, "then": true, "else": true, "elif": true, "if": true,
	"while": true, "until": true, "!": true, "{": true, "(": true, "time": true}

// skipped are simple commands that run nothing of their own.
var skipped = map[string]bool{"for": true, "case": true, "done": true, "fi": true, "esac": true, "}": true,
	")": true, "in": true, "select": true, "function": true}

var networkTools = map[string]bool{"curl": true, "wget": true, "ssh": true, "scp": true, "sftp": true,
	"rsync": true, "nc": true, "ncat": true, "telnet": true, "ftp": true, "http": true, "https": true,
	"gh": true, "ping": true}

var cloudTools = map[string]bool{"gcloud": true, "gsutil": true, "bq": true, "kubectl": true,
	"terraform": true, "aws": true, "az": true, "helm": true, "psql": true, "mysql": true,
	"redis-cli": true, "doppler": true, "firebase": true, "vercel": true, "flyctl": true}

var gitNetwork = map[string]bool{"push": true, "fetch": true, "pull": true, "clone": true, "ls-remote": true}

var installers = map[string]bool{"npm": true, "bun": true, "pnpm": true, "yarn": true, "pip": true,
	"pip3": true, "uv": true, "cargo": true, "go": true, "apt": true, "apt-get": true, "brew": true}

func (r *reader) simple(cmd simpleCommand) {
	// Its redirections go after what it names itself, so the order is the
	// command's own.
	defer func() {
		for _, redirect := range cmd.writes {
			if target := r.resolve(redirect); target != "" && !strings.HasPrefix(target, "/dev/") {
				r.add(&r.out.Writes, r.show(target))
			}
		}
	}()
	words := cmd.words
	for len(words) > 0 && keywords[words[0]] {
		words = words[1:]
	}
	if len(words) == 0 || skipped[words[0]] {
		return
	}
	for _, w := range words {
		if strings.Contains(w, "://") {
			r.add(&r.out.Network, hostOf(w))
		}
		if production.MatchString(w) {
			r.add(&r.out.Cloud, "names "+strings.ToLower(production.FindStringSubmatch(w)[1]))
		}
	}
	for len(words) > 0 && assignment.MatchString(words[0]) {
		if len(words) == 1 {
			name, value, _ := strings.Cut(words[0], "=")
			r.vars[name] = r.resolve(value)
		}
		words = words[1:]
	}
	words = r.unwrap(words)
	if len(words) == 0 {
		return
	}
	verb := path.Base(words[0])
	args := words[1:]
	r.outputFlags(args)
	switch {
	case shells[verb]:
		for i, a := range args {
			if a == "-c" && i+1 < len(args) {
				r.script(args[i+1])
				break
			}
		}
	case verb == "cd":
		if len(args) > 0 {
			r.cwd = r.resolve(args[0])
		}
	case verb == "rm" || verb == "rmdir" || verb == "unlink" || verb == "shred":
		for _, a := range operands(args) {
			r.add(&r.out.Deletes, r.show(r.resolve(a)))
		}
	case verb == "find":
		deletes := false
		for i, a := range args {
			if a == "-delete" || a == "-exec" && i+1 < len(args) && path.Base(args[i+1]) == "rm" {
				deletes = true
			}
		}
		if deletes {
			roots := []string{"."}
			if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
				roots = args[:1]
			}
			r.add(&r.out.Deletes, "files found under "+r.show(r.resolve(roots[0])))
		}
	case verb == "mv" || verb == "cp" || verb == "install" || verb == "ln":
		if ops := operands(args); len(ops) >= 2 {
			r.add(&r.out.Writes, r.show(r.resolve(ops[len(ops)-1])))
			if verb == "mv" {
				for _, a := range ops[:len(ops)-1] {
					r.add(&r.out.Deletes, r.show(r.resolve(a))+" (moved)")
				}
			}
		}
	case verb == "mkdir" || verb == "touch" || verb == "tee" || verb == "truncate":
		for _, a := range operands(args) {
			r.add(&r.out.Writes, r.show(r.resolve(a)))
		}
	case verb == "sed" && hasFlag(args, "-i"):
		if ops := operands(args); len(ops) >= 2 {
			r.add(&r.out.Writes, r.show(r.resolve(ops[len(ops)-1])))
		}
	case verb == "git":
		sub := firstOperand(args)
		switch {
		case gitNetwork[sub]:
			r.add(&r.out.Network, "git "+sub)
		case sub == "clean":
			r.add(&r.out.Deletes, "untracked files (git clean)")
		}
	case networkTools[verb]:
		r.add(&r.out.Network, verb)
	case cloudTools[verb]:
		r.add(&r.out.Cloud, strings.TrimSpace(verb+" "+firstOperand(args)))
	case installers[verb] && isInstall(firstOperand(args)):
		r.add(&r.out.Network, verb+" "+firstOperand(args))
	case verb == "docker" || verb == "podman":
		if sub := firstOperand(args); sub == "push" || sub == "pull" || sub == "login" {
			r.add(&r.out.Network, verb+" "+sub)
		}
	}
}

func isInstall(sub string) bool {
	switch sub {
	case "install", "i", "add", "get", "download", "update", "upgrade", "ci":
		return true
	}
	return false
}

// unwrap drops the programs that only run another one -- systemd-run,
// prlimit, timeout, nice, nohup, env, sudo, xargs -- and their options,
// keeping the limits they set.
func (r *reader) unwrap(words []string) []string {
	for len(words) > 0 {
		switch path.Base(words[0]) {
		case "sudo", "doas":
			r.out.Root = true
			words = skipOptions(words[1:], map[string]bool{"-u": true, "-g": true})
		case "systemd-run":
			rest := words[1:]
			for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
				opt := rest[0]
				rest = rest[1:]
				value := ""
				switch {
				case opt == "-p" || opt == "--property" || opt == "-u" || opt == "--unit" || opt == "-E" || opt == "--setenv":
					if len(rest) > 0 {
						value, rest = rest[0], rest[1:]
					}
				case strings.HasPrefix(opt, "--property="):
					value = strings.TrimPrefix(opt, "--property=")
				}
				if (opt == "-p" || strings.HasPrefix(opt, "--property")) && value != "" {
					r.add(&r.out.Limits, value)
				}
			}
			words = rest
		case "prlimit":
			rest := words[1:]
			for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
				if name, value, ok := strings.Cut(strings.TrimLeft(rest[0], "-"), "="); ok {
					r.add(&r.out.Limits, name+"="+value)
				}
				rest = rest[1:]
			}
			words = rest
		case "timeout":
			rest := skipOptions(words[1:], map[string]bool{"-s": true, "--signal": true, "-k": true, "--kill-after": true})
			if len(rest) > 0 {
				r.add(&r.out.Limits, "timeout "+rest[0])
				rest = rest[1:]
			}
			words = rest
		case "nice", "ionice":
			words = skipOptions(words[1:], map[string]bool{"-n": true, "-c": true})
		case "nohup", "command", "exec", "stdbuf", "caffeinate":
			words = skipOptions(words[1:], nil)
		case "env":
			rest := skipOptions(words[1:], map[string]bool{"-u": true, "-C": true})
			for len(rest) > 0 && assignment.MatchString(rest[0]) {
				rest = rest[1:]
			}
			words = rest
		case "xargs":
			words = skipOptions(words[1:], map[string]bool{"-I": true, "-n": true, "-P": true, "-d": true, "-L": true})
		default:
			return words
		}
	}
	return words
}

func skipOptions(words []string, takesValue map[string]bool) []string {
	for len(words) > 0 && strings.HasPrefix(words[0], "-") {
		if takesValue[words[0]] && len(words) > 1 {
			words = words[1:]
		}
		words = words[1:]
	}
	return words
}

// outputFlags records the file a -o or --output option names.
func (r *reader) outputFlags(args []string) {
	for i, a := range args {
		switch {
		case (a == "-o" || a == "--output" || a == "--out") && i+1 < len(args):
			r.add(&r.out.Writes, r.show(r.resolve(args[i+1])))
		case strings.HasPrefix(a, "--output="):
			r.add(&r.out.Writes, r.show(r.resolve(strings.TrimPrefix(a, "--output="))))
		}
	}
}

func operands(args []string) []string {
	var out []string
	options := true
	for _, a := range args {
		if options && a == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func firstOperand(args []string) string {
	if ops := operands(args); len(ops) > 0 {
		return ops[0]
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag) && !strings.HasPrefix(a, "--") {
			return true
		}
	}
	return false
}

func hostOf(word string) string {
	_, rest, _ := strings.Cut(word, "://")
	host, _, _ := strings.Cut(rest, "/")
	if host == "" {
		return word
	}
	return host
}

// resolve is word with the variables the command set itself put in, and a
// relative path read against the directory it changed into.
func (r *reader) resolve(word string) string {
	word = variable.ReplaceAllStringFunc(word, func(ref string) string {
		name := variable.FindStringSubmatch(ref)[1]
		if value, ok := r.vars[name]; ok {
			return value
		}
		return ref
	})
	if r.cwd != "" && word != "" && !strings.HasPrefix(word, "/") && !strings.HasPrefix(word, "~") &&
		!strings.HasPrefix(word, "$") {
		word = strings.TrimSuffix(r.cwd, "/") + "/" + word
	}
	return word
}

// show is a path as a person reads it: a session's scratchpad named as one.
func (r *reader) show(p string) string {
	if at := strings.Index(p, "/scratchpad"); at >= 0 {
		rest := strings.TrimPrefix(p[at+len("/scratchpad"):], "/")
		if rest == "" {
			return "scratchpad"
		}
		return "scratchpad/" + rest
	}
	return p
}

// simpleCommand is one command of a list or pipeline: its words with quotes
// removed, and the files its output redirections name.
type simpleCommand struct {
	words         []string
	writes        []string
	substitutions []string
}

// splitCommands reads command into simple commands: split on ;, &, |, &&,
// || and newlines outside quotes, $( ) and backquotes, with each output
// redirection's target taken out of the words.
func splitCommands(command string) []simpleCommand {
	var (
		out     []simpleCommand
		current simpleCommand
		word    strings.Builder
		inWord  bool
		depth   int
		quote   rune
		pending bool // the next word is an output redirection's target
		inner   strings.Builder
	)
	flushWord := func() {
		if !inWord {
			return
		}
		w := word.String()
		word.Reset()
		inWord = false
		if pending {
			current.writes = append(current.writes, w)
			pending = false
			return
		}
		current.words = append(current.words, w)
	}
	flushCommand := func() {
		flushWord()
		pending = false
		if len(current.words) > 0 || len(current.writes) > 0 {
			out = append(out, current)
		}
		current = simpleCommand{}
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case depth > 0:
			word.WriteRune(c)
			switch c {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				current.substitutions = append(current.substitutions, inner.String())
				inner.Reset()
			} else {
				inner.WriteRune(c)
			}
		case quote == '"' && c == '$' && i+1 < len(runes) && runes[i+1] == '(':
			word.WriteString("$(")
			i++
			depth = 1
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(runes) {
				i++
				word.WriteRune(runes[i])
			} else {
				word.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == '\\' && i+1 < len(runes):
			i++
			if runes[i] != '\n' {
				word.WriteRune(runes[i])
				inWord = true
			}
		case c == '$' && i+1 < len(runes) && runes[i+1] == '(':
			word.WriteString("$(")
			i++
			depth, inWord = 1, true
		case c == '>':
			// 2>, &>, >> and >| all write; >& and 2>&1 point at a descriptor.
			if inWord && isDigits(word.String()) {
				word.Reset()
				inWord = false
			}
			flushWord()
			for i+1 < len(runes) && (runes[i+1] == '>' || runes[i+1] == '|') {
				i++
			}
			if i+1 < len(runes) && runes[i+1] == '&' {
				i++
				for i+1 < len(runes) && (runes[i+1] >= '0' && runes[i+1] <= '9' || runes[i+1] == '-') {
					i++
				}
				continue
			}
			pending = true
		case c == '&' && i+1 < len(runes) && runes[i+1] == '>':
			flushWord()
			i++
			for i+1 < len(runes) && runes[i+1] == '>' {
				i++
			}
			pending = true
		case c == '<':
			flushWord()
			for i+1 < len(runes) && runes[i+1] == '<' {
				i++
			}
		case c == ';' || c == '&' || c == '|' || c == '\n' || c == '(' || c == ')':
			flushCommand()
		case c == ' ' || c == '\t':
			flushWord()
		case c == '#' && !inWord:
			for i+1 < len(runes) && runes[i+1] != '\n' {
				i++
			}
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	flushCommand()
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
