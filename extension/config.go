package extension

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is one extension's section of the operator's config: the table
// under [extensions.<id>], and nothing outside it.
type Config struct {
	section map[string]any
	dataDir string
}

// NewConfig wraps a decoded section. The host builds these from the config
// file; an extension's own tests build them from a literal. A nil section is
// an absent one.
func NewConfig(section map[string]any) Config {
	return Config{section: section}
}

// WithDataDir is c with dir as the extension's data directory. The host
// sets it to <config dir>/extensions/<id>; an extension's own tests point it
// at a temporary directory.
func (c Config) WithDataDir(dir string) Config {
	c.dataDir = dir
	return c
}

// DataDir is the directory this extension keeps its own state in, created
// on first use. It is the extension's alone: the host never reads it, and
// no core table holds extension data, so an extension can change what it
// stores without the board's schema changing with it.
//
// It is shared by every process of one build: the board and each session's
// MCP server run at once, so an extension that writes here from more than
// one of them has concurrent writers. SQLite in WAL mode, or files replaced
// by rename, are the safe shapes.
func (c Config) DataDir() (string, error) {
	if c.dataDir == "" {
		return "", errors.New("the host gave this extension no data directory")
	}
	if err := os.MkdirAll(c.dataDir, 0o700); err != nil {
		return "", err
	}
	return c.dataDir, nil
}

// Present reports whether the operator wrote this extension's section at
// all, even an empty one.
func (c Config) Present() bool {
	return c.section != nil
}

// Decode fills v, a pointer to the extension's settings struct, from the
// section using the same `toml` field tags the config file is written in.
// A key v has no field for is an error rather than something silently
// dropped: a misspelt setting that is ignored reads, to the operator, like
// a setting that does nothing. Decoding an absent section leaves v as it
// was.
func (c Config) Decode(v any) error {
	if len(c.section) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c.section); err != nil {
		return err
	}
	meta, err := toml.Decode(buf.String(), v)
	if err != nil {
		return err
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		slices.Sort(keys)
		return fmt.Errorf("unknown key(s): %s", strings.Join(keys, ", "))
	}
	return nil
}

// Duration is a time.Duration a section writes as a Go duration string,
// such as "90s" or "720h".
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}
