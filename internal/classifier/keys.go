package classifier

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/usestring/gate-inbox/internal/parentseal"
)

func (p Provider) KeyPath(configDir string) string {
	file := "typesafe.key"
	if p == Clef {
		file = "workers-ai.key"
	}
	return filepath.Join(parentseal.KeyDir(configDir), file)
}

// ReadSavedKey is the key pasted into Settings, or empty when there is none.
func (p Provider) ReadSavedKey(configDir string) (string, error) {
	if configDir == "" || (p != JEV && p != Clef) {
		return "", nil
	}
	data, err := os.ReadFile(p.KeyPath(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(data)), err
}

// Environment credentials override only the same provider’s saved key.
func (p Provider) Key(configDir string) string {
	if key := strings.TrimSpace(os.Getenv(p.KeyEnv())); key != "" {
		return key
	}
	key, _ := p.ReadSavedKey(configDir)
	return key
}
