// Package inbox is the remote inbox: a small HTTP endpoint that turns a text
// message, such as a phone's dictation, into a new top-level router session
// on the board.
//
// It binds loopback by default and is meant to be reached only over a
// private network that proxies to it (tailscale serve, an SSH tunnel). A
// client proves itself with a bearer token kept outside config.toml, and each
// token may claim only the sources its client is allowed. Every spawn starts
// an agent, so spawns are rate limited: a router that hands messages to
// another agent which posts back here cannot run away.
package inbox

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension"
)

const (
	DefaultListen      = "127.0.0.1:8740"
	DefaultTool        = "claude"
	DefaultSpawnLimit  = 6
	DefaultSpawnWindow = 5 * time.Minute
	// TokensFile is the tokens file's name in the config directory.
	TokensFile = "inbox-tokens"
	// TokenEnvPrefix starts the environment variable that carries a
	// client's token: GATE_INBOX_INBOX_TOKEN_<CLIENT>, the client's name
	// upper-cased with dashes as underscores.
	TokenEnvPrefix = "GATE_INBOX_INBOX_TOKEN_"
)

// Config is the [inbox] section of config.toml. It never holds a token.
type Config struct {
	// Listen is the host:port to bind. Empty is DefaultListen, loopback.
	Listen string `toml:"listen"`
	// Group is the existing group router sessions are filed in; empty is
	// the root.
	Group string `toml:"group"`
	// Directory is where router sessions start; empty is the home directory.
	Directory string `toml:"directory"`
	// Tool is the configured agent CLI router sessions run; empty is claude.
	Tool string `toml:"tool"`
	// BriefTemplate is a Go text/template file the router's first prompt is
	// rendered from; empty is the built-in DefaultBrief.
	BriefTemplate string `toml:"brief_template"`
	// TokensFile overrides <config dir>/inbox-tokens.
	TokensFile  string             `toml:"tokens_file"`
	SpawnLimit  int                `toml:"spawn_limit"`
	SpawnWindow extension.Duration `toml:"spawn_window"`
	// Clients names each client and the sources its token may claim.
	Clients map[string]Client `toml:"clients"`
	// Origins is a note per source on where its messages come from, laid
	// over DefaultOrigins. A source with no entry of its own takes the one
	// for the part before its first dash (bot-chat takes bot).
	Origins map[string]string `toml:"origins"`
}

// Client is one [inbox.clients.<name>] block.
type Client struct {
	// Sources are what this client may post as. "x*" admits any source
	// starting with "x". Empty is the client's own name. The first one,
	// without its "*", is the source a post that names none is filed under.
	Sources []string `toml:"sources"`
}

// DefaultOrigins are the origin notes a config need not repeat.
var DefaultOrigins = map[string]string{
	"iphone": "The user dictated it on their phone, so expect speech-to-text slips and little punctuation.",
	"ipad":   "The user dictated it on their tablet, so expect speech-to-text slips and little punctuation.",
}

var (
	sourcePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	clientPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

func (c Config) listen() string {
	if c.Listen == "" {
		return DefaultListen
	}
	return c.Listen
}

// SpawnTool is the CLI router sessions run.
func (c Config) SpawnTool() string {
	if c.Tool == "" {
		return DefaultTool
	}
	return c.Tool
}

func (c Config) spawnLimit() int {
	if c.SpawnLimit <= 0 {
		return DefaultSpawnLimit
	}
	return c.SpawnLimit
}

func (c Config) spawnWindow() time.Duration {
	if c.SpawnWindow.Duration <= 0 {
		return DefaultSpawnWindow
	}
	return c.SpawnWindow.Duration
}

// StartDirectory is where router sessions start, with a leading ~ expanded.
func (c Config) StartDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if c.Directory == "" {
		return home, nil
	}
	return expandHome(c.Directory, home), nil
}

func (c Config) origin(source string) string {
	base, _, _ := strings.Cut(source, "-")
	for _, key := range []string{source, base} {
		if note, ok := c.Origins[key]; ok {
			return note
		}
		if note, ok := DefaultOrigins[key]; ok {
			return note
		}
	}
	return fmt.Sprintf("Source %q is a client of the remote inbox.", source)
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

// identity is a client a token was issued to.
type identity struct {
	name    string
	sources []string
}

// defaultSource is the source a post that names none is filed under.
func (id identity) defaultSource() string {
	return strings.TrimSuffix(id.sources[0], "*")
}

func (id identity) allows(source string) bool {
	return slices.ContainsFunc(id.sources, func(pattern string) bool {
		if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
			return strings.HasPrefix(source, prefix)
		}
		return source == pattern
	})
}

// LoadTokens reads each client's token, from its environment variable when
// set and otherwise from the tokens file, and unsets those variables so no
// session the board launches from this process inherits one. The file must
// be readable by its owner alone; a missing file is no tokens.
func LoadTokens(cfg Config, configDir string) (map[string]identity, error) {
	path := cfg.TokensFile
	if path == "" {
		path = filepath.Join(configDir, TokensFile)
	} else if home, err := os.UserHomeDir(); err == nil {
		path = expandHome(path, home)
	}
	byClient, err := readTokensFile(path)
	if err != nil {
		return nil, err
	}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		rest, ok := strings.CutPrefix(key, TokenEnvPrefix)
		if !ok {
			continue
		}
		os.Unsetenv(key)
		if value = strings.TrimSpace(value); value != "" {
			byClient[strings.ReplaceAll(strings.ToLower(rest), "_", "-")] = value
		}
	}
	tokens := map[string]identity{}
	for name, token := range byClient {
		if !clientPattern.MatchString(name) {
			return nil, fmt.Errorf("inbox client %q: names are lowercase letters, digits and dashes", name)
		}
		if other, taken := tokens[token]; taken {
			return nil, fmt.Errorf("inbox clients %q and %q share a token; give each its own", other.name, name)
		}
		sources := cfg.Clients[name].Sources
		if len(sources) == 0 {
			sources = []string{name}
		}
		for _, pattern := range sources {
			if !sourcePattern.MatchString(strings.TrimSuffix(pattern, "*")) {
				return nil, fmt.Errorf("inbox.clients.%s: source %q is not lowercase letters, digits and dashes", name, pattern)
			}
		}
		tokens[token] = identity{name: name, sources: sources}
	}
	return tokens, nil
}

func readTokensFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is readable by others (mode %04o); chmod 600 it", path, info.Mode().Perm())
	}
	tokens := map[string]string{}
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		name, token, ok := strings.Cut(text, "=")
		name, token = strings.TrimSpace(name), strings.TrimSpace(token)
		if !ok || name == "" || token == "" {
			return nil, fmt.Errorf("%s:%d: want <client>=<token>", path, line)
		}
		tokens[name] = token
	}
	return tokens, scanner.Err()
}
