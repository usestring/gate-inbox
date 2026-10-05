package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/usestring/gate-inbox/internal/parentseal"
)

// Global key-dir denials.
//
// A launch denies its session the sealing keys (KeyDirDenials) through its
// --settings file. A claude started anywhere else loaded no such file, so
// once adopted it could read the keys that let it forge a parent's seal. The
// board therefore also writes the same denials into the user's settings,
// beside the global hooks: permissions.deny for the Read and Edit tools and
// sandbox.filesystem.denyRead for the sandboxed shell. Every claude on the
// machine loads them, which is the point, and they cost the sessions that
// never meet the board nothing: they only take away access to Gate Inbox's
// own key directory, and Claude Code merges them with the operator's lists.
//
// The entries carry no tag, since a rule is a bare string, so they are
// recognised by being exactly the strings this board's key directory gives.
// Only those strings are added or removed; every other byte of the file stays
// where it was. A list or object that had to be made to hold them is noted in
// the board's hooks directory (createdName) and taken out again when the
// entries go, so an unregister gives back the file the operator had.
//
// Settings are read when a session starts, so a session that was already
// running when they were written does not have them. For an adopted session
// the PreToolUse dispatch refuses a tool call that names the directory
// (KeyDirDecision), which covers it from its next call.

// createdName is the file in the hooks directory that lists, per settings
// file, the containers the denials were the reason to create.
const createdName = "global-settings-created.json"

// denyPath and denyReadPath are where the two lists live in a settings file.
var (
	denyPath     = []string{"permissions", "deny"}
	denyReadPath = []string{"sandbox", "filesystem", "denyRead"}
)

// globalDenials are the list entries this board's key directory gives, by
// list.
func globalDenials(configDir string) map[string][]string {
	keyDir := parentseal.KeyDir(configDir)
	rules, sandbox := KeyDirDenials(keyDir)
	return map[string][]string{
		strings.Join(denyPath, "."):     rules.Deny,
		strings.Join(denyReadPath, "."): sandbox.Filesystem.DenyRead,
	}
}

// addDenials adds whichever of configDir's denials raw lacks, and returns the
// dotted paths of the containers it had to create.
func addDenials(raw []byte, configDir string) ([]byte, []string, error) {
	var created []string
	wanted := globalDenials(configDir)
	for _, path := range [][]string{denyPath, denyReadPath} {
		var made []string
		var err error
		raw, made, err = addToList(raw, path, wanted[strings.Join(path, ".")])
		if err != nil {
			return nil, nil, err
		}
		created = append(created, made...)
	}
	return raw, created, nil
}

// removeDenials takes configDir's denials out of raw, and with them any
// container in created they leave empty.
func removeDenials(raw []byte, configDir string, created []string) ([]byte, error) {
	wanted := globalDenials(configDir)
	for _, path := range [][]string{denyReadPath, denyPath} {
		var err error
		raw, err = removeFromList(raw, path, wanted[strings.Join(path, ".")], created)
		if err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// addToList makes sure the string list at path holds every value, adding the
// missing ones at its end.
func addToList(raw []byte, path []string, values []string) ([]byte, []string, error) {
	where := strings.Join(path, ".")
	at, depth, err := locate(raw, path)
	if err != nil {
		return nil, nil, err
	}
	if depth == len(path) {
		if raw[at] != '[' {
			return nil, nil, fmt.Errorf("hooks: the settings file's %s is not a list", where)
		}
		have, err := listStrings(raw, at)
		if err != nil {
			return nil, nil, err
		}
		var missing [][]byte
		for _, v := range values {
			if !slices.Contains(have, v) {
				encoded, err := marshalPlain(v)
				if err != nil {
					return nil, nil, err
				}
				missing = append(missing, encoded)
			}
		}
		if len(missing) == 0 {
			return raw, nil, nil
		}
		return appendElements(raw, at, missing), nil, nil
	}
	if raw[at] != '{' {
		return nil, nil, fmt.Errorf("hooks: the settings file's %s is not an object", strings.Join(path[:depth], "."))
	}
	var value any = values
	for i := len(path) - 1; i > depth; i-- {
		value = map[string]any{path[i]: value}
	}
	encoded, err := marshalPlain(value)
	if err != nil {
		return nil, nil, err
	}
	var created []string
	for i := depth + 1; i <= len(path); i++ {
		created = append(created, strings.Join(path[:i], "."))
	}
	return insertMember(raw, at, path[depth], encoded), created, nil
}

// removeFromList drops every value from the list at path, then removes the
// list, and each object above it, that is left empty and is in created.
func removeFromList(raw []byte, path []string, values []string, created []string) ([]byte, error) {
	at, depth, err := locate(raw, path)
	if err != nil || depth < len(path) || raw[at] != '[' {
		return raw, nil
	}
	for {
		elements, _, err := arraySpans(raw, at)
		if err != nil {
			return raw, nil
		}
		gone := -1
		for i, el := range elements {
			var s string
			if json.Unmarshal(raw[el.start:el.end], &s) == nil && slices.Contains(values, s) {
				gone = i
				break
			}
		}
		if gone < 0 {
			break
		}
		raw = removeElement(raw, at, elements, gone)
	}
	for level := len(path); level > 0; level-- {
		if !slices.Contains(created, strings.Join(path[:level], ".")) {
			break
		}
		at, depth, err := locate(raw, path[:level])
		if err != nil || depth < level {
			break
		}
		if empty, err := emptyContainer(raw, at); err != nil || !empty {
			break
		}
		parent, _, err := locate(raw, path[:level-1])
		if err != nil {
			break
		}
		raw = deleteMember(raw, parent, path[level-1])
	}
	return raw, nil
}

// locate walks path from the top-level object and returns the offset of the
// deepest value it reaches, and how many keys deep that is. Every value on
// the way but the last must be an object.
func locate(raw []byte, path []string) (at, depth int, err error) {
	at = bytes.IndexByte(raw, '{')
	if at < 0 || len(bytes.TrimSpace(raw[:at])) > 0 {
		return 0, 0, errors.New("hooks: the settings file is not a JSON object")
	}
	for depth < len(path) {
		if raw[at] != '{' {
			return at, depth, nil
		}
		members, _, err := objectSpans(raw, at)
		if err != nil {
			return 0, 0, err
		}
		i := slices.IndexFunc(members, func(m memberSpan) bool { return m.key == path[depth] })
		if i < 0 {
			return at, depth, nil
		}
		at = members[i].valueStart
		depth++
	}
	return at, depth, nil
}

// memberSpan is where one member of an object sits in the file.
type memberSpan struct {
	key                            string
	keyStart, valueStart, valueEnd int
}

// span is where one element of an array sits.
type span struct{ start, end int }

// objectSpans reads the object opening at raw[open] and returns its members
// and the offset of its closing brace.
func objectSpans(raw []byte, open int) ([]memberSpan, int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw[open:]))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, 0, errors.New("hooks: not a JSON object")
	}
	var members []memberSpan
	for dec.More() {
		from := open + int(dec.InputOffset())
		tok, err := dec.Token()
		if err != nil {
			return nil, 0, err
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, 0, err
		}
		end := open + int(dec.InputOffset())
		members = append(members, memberSpan{key: key, keyStart: skipSeparators(raw, from), valueStart: end - len(v), valueEnd: end})
	}
	if _, err := dec.Token(); err != nil {
		return nil, 0, err
	}
	return members, open + int(dec.InputOffset()) - 1, nil
}

// arraySpans reads the array opening at raw[open] and returns its elements
// and the offset of its closing bracket.
func arraySpans(raw []byte, open int) ([]span, int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw[open:]))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, 0, errors.New("hooks: not a JSON array")
	}
	var elements []span
	for dec.More() {
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, 0, err
		}
		end := open + int(dec.InputOffset())
		elements = append(elements, span{start: end - len(v), end: end})
	}
	if _, err := dec.Token(); err != nil {
		return nil, 0, err
	}
	return elements, open + int(dec.InputOffset()) - 1, nil
}

func skipSeparators(raw []byte, i int) int {
	for i < len(raw) && (isSpace(raw[i]) || raw[i] == ',') {
		i++
	}
	return i
}

// listStrings is the string elements of the array at raw[open].
func listStrings(raw []byte, open int) ([]string, error) {
	elements, _, err := arraySpans(raw, open)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, el := range elements {
		var s string
		if json.Unmarshal(raw[el.start:el.end], &s) == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func emptyContainer(raw []byte, open int) (bool, error) {
	switch raw[open] {
	case '{':
		members, _, err := objectSpans(raw, open)
		return len(members) == 0, err
	case '[':
		elements, _, err := arraySpans(raw, open)
		return len(elements) == 0, err
	}
	return false, nil
}

// lineIndent is the whitespace that starts the line holding raw[i].
func lineIndent(raw []byte, i int) string {
	start := bytes.LastIndexByte(raw[:i], '\n') + 1
	end := start
	for end < len(raw) && (raw[end] == ' ' || raw[end] == '\t') {
		end++
	}
	return string(raw[start:end])
}

// multiline reports whether the container opening at raw[open] puts its
// first item on a line of its own.
func multiline(raw []byte, open, first int) bool {
	return bytes.IndexByte(raw[open:first], '\n') >= 0
}

// layout is how a new member of the object at raw[open] is written: the
// indent of its line and one step of indentation, or inline when the object
// keeps its members on one line.
func layout(raw []byte, open int, members []memberSpan) (indent, step string, inline bool) {
	base := lineIndent(raw, open)
	if len(members) == 0 {
		step = "  "
		if strings.Contains(base, "\t") {
			step = "\t"
		}
		return base + step, step, false
	}
	if !multiline(raw, open, members[0].keyStart) {
		return "", "", true
	}
	indent = lineIndent(raw, members[0].keyStart)
	step = strings.TrimPrefix(indent, base)
	if step == indent && base != "" || step == "" {
		step = "  "
	}
	return indent, step, false
}

// render lays out a compact JSON value as a member at indent.
func render(value []byte, indent, step string, inline bool) []byte {
	var out bytes.Buffer
	if inline {
		if json.Compact(&out, value) != nil {
			return value
		}
		return out.Bytes()
	}
	if json.Indent(&out, value, indent, step) != nil {
		return value
	}
	return out.Bytes()
}

// insertMember adds key: value at the end of the object at raw[open], in the
// object's own layout. deleteMember of the same key gives back raw.
func insertMember(raw []byte, open int, key string, value []byte) []byte {
	members, closing, err := objectSpans(raw, open)
	if err != nil {
		return raw
	}
	name, _ := marshalPlain(key)
	indent, step, inline := layout(raw, open, members)
	if len(members) == 0 {
		entry := "\n" + indent + string(name) + ": " + string(render(value, indent, step, false)) + "\n" + lineIndent(raw, open)
		return splice(raw, open+1, closing, entry)
	}
	colon := ": "
	first := members[0]
	if seg := raw[first.keyStart:first.valueStart]; bytes.LastIndexByte(seg, ':') > 0 {
		c := bytes.LastIndexByte(seg, ':')
		if q := bytes.LastIndexByte(seg[:c], '"'); q >= 0 {
			colon = string(seg[q+1:])
		}
	}
	sep := ",\n" + indent
	if inline {
		sep = ", "
		if len(members) > 1 {
			sep = string(raw[members[0].valueEnd:members[1].keyStart])
		}
	}
	last := members[len(members)-1]
	entry := sep + string(name) + colon + string(render(value, indent, step, inline))
	return splice(raw, last.valueEnd, last.valueEnd, entry)
}

// deleteMember takes key out of the object at raw[open] with the separator
// before it, which is what insertMember put there.
func deleteMember(raw []byte, open int, key string) []byte {
	members, closing, err := objectSpans(raw, open)
	if err != nil {
		return raw
	}
	i := slices.IndexFunc(members, func(m memberSpan) bool { return m.key == key })
	switch {
	case i < 0:
		return raw
	case len(members) == 1:
		return splice(raw, open+1, closing, "")
	case i > 0:
		return splice(raw, members[i-1].valueEnd, members[i].valueEnd, "")
	}
	return splice(raw, members[0].keyStart, members[1].keyStart, "")
}

// appendElements adds values at the end of the array at raw[open], one per
// line when the array is laid out that way.
func appendElements(raw []byte, open int, values [][]byte) []byte {
	elements, closing, err := arraySpans(raw, open)
	if err != nil {
		return raw
	}
	if len(elements) == 0 {
		parent := lineIndent(raw, open)
		var b strings.Builder
		if inlineContext(raw, open) {
			for i, v := range values {
				if i > 0 {
					b.WriteString(", ")
				}
				b.Write(v)
			}
			return splice(raw, open+1, closing, b.String())
		}
		step := "  "
		if strings.Contains(parent, "\t") {
			step = "\t"
		}
		for i, v := range values {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n" + parent + step)
			b.Write(v)
		}
		b.WriteString("\n" + parent)
		return splice(raw, open+1, closing, b.String())
	}
	sep := ", "
	if multiline(raw, open, elements[0].start) {
		sep = ",\n" + lineIndent(raw, elements[0].start)
	} else if len(elements) > 1 {
		sep = string(raw[elements[0].end:elements[1].start])
	}
	var b strings.Builder
	for _, v := range values {
		b.WriteString(sep)
		b.Write(v)
	}
	last := elements[len(elements)-1].end
	return splice(raw, last, last, b.String())
}

// inlineContext reports whether the array at raw[open] sits in a container
// written on one line, where a new element should not open a line of its
// own: true when the text before it on its line opens no other container.
func inlineContext(raw []byte, open int) bool {
	start := bytes.LastIndexByte(raw[:open], '\n') + 1
	before := raw[start:open]
	return bytes.ContainsAny(before, "{[") || bytes.IndexByte(raw[open:], '\n') < 0
}

// removeElement takes elements[i] out of the array at raw[open] with the
// separator before it, which is what appendElements put there.
func removeElement(raw []byte, open int, elements []span, i int) []byte {
	switch {
	case len(elements) == 1:
		_, closing, err := arraySpans(raw, open)
		if err != nil {
			return raw
		}
		return splice(raw, open+1, closing, "")
	case i > 0:
		return splice(raw, elements[i-1].end, elements[i].end, "")
	}
	return splice(raw, elements[0].start, elements[1].start, "")
}

func splice(raw []byte, from, to int, with string) []byte {
	out := make([]byte, 0, len(raw)-(to-from)+len(with))
	out = append(out, raw[:from]...)
	out = append(out, with...)
	return append(out, raw[to:]...)
}

// readCreated is the containers the denials of configDir's board created in
// the settings file at path.
func readCreated(configDir, path string) []string {
	raw, err := os.ReadFile(filepath.Join(configDir, "hooks", createdName))
	if err != nil {
		return nil
	}
	var all map[string][]string
	if json.Unmarshal(raw, &all) != nil {
		return nil
	}
	return all[path]
}

// writeCreated records containers for the settings file at path, adding to
// what is there; with none and forget set it drops path's record.
func writeCreated(configDir, path string, containers []string, forget bool) error {
	file := filepath.Join(configDir, "hooks", createdName)
	all := map[string][]string{}
	if raw, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(raw, &all)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	switch {
	case forget:
		if _, ok := all[path]; !ok {
			return nil
		}
		delete(all, path)
	case len(containers) == 0:
		return nil
	default:
		for _, c := range containers {
			if !slices.Contains(all[path], c) {
				all[path] = append(all[path], c)
			}
		}
	}
	if len(all) == 0 {
		return removeIfExists(file)
	}
	body, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return WriteWhole(file, string(body)+"\n")
}

// stillThere is the containers among created that raw still has.
func stillThere(raw []byte, created []string) []string {
	var out []string
	for _, c := range created {
		path := strings.Split(c, ".")
		if _, depth, err := locate(raw, path); err == nil && depth == len(path) && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// registeredBoards is the config directory of every board whose global hooks
// raw carries, read back off their tags.
func registeredBoards(raw []byte) []string {
	var settings struct {
		Hooks map[string][]hookMatcher `json:"hooks"`
	}
	if json.Unmarshal(raw, &settings) != nil {
		return nil
	}
	var dirs []string
	for _, groups := range settings.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				rest, ok := strings.CutPrefix(h.Command, globalTag+" ")
				if !ok {
					continue
				}
				if dir, ok := shellUnquote(rest); ok && !slices.Contains(dirs, dir) {
					dirs = append(dirs, dir)
				}
			}
		}
	}
	slices.Sort(dirs)
	return dirs
}

// shellUnquote reads the one word shellQuote wrote at the start of s.
func shellUnquote(s string) (string, bool) {
	var b strings.Builder
	for {
		if !strings.HasPrefix(s, "'") {
			return "", false
		}
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", false
		}
		b.WriteString(s[1 : 1+end])
		s = s[end+2:]
		if !strings.HasPrefix(s, `\'`) {
			return b.String(), true
		}
		b.WriteByte('\'')
		s = s[2:]
	}
}
