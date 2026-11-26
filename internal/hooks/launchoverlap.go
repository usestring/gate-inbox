package hooks

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func (m *Manager) LaunchScope(id string) string {
	if checkID(id) != nil {
		return ""
	}
	body, _ := os.ReadFile(m.StatusFile(id) + ".launch-scope")
	return string(body)
}

// RecordLaunchScope publishes the opening prompt only once. Linking the
// completed file lets simultaneous hooks see either the full scope or nothing.
func (m *Manager) RecordLaunchScope(id, prompt string) (bool, error) {
	if err := checkID(id); err != nil {
		return false, err
	}
	path := m.StatusFile(id) + ".launch-scope"
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return false, err
	}
	file, err := os.CreateTemp(m.dir, filepath.Base(path)+".*")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(prompt)
	if err := errors.Join(err, file.Close()); err != nil {
		return false, err
	}
	if err := os.Link(file.Name(), path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
