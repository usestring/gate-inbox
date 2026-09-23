package store

import (
	"path/filepath"
	"strconv"
	"strings"
)

// nameSourceBackfill marks the pass as done, so a row a person renames to
// something that happens to look derived is never reclassified later.
const nameSourceBackfill = "name_source_backfill"

// BackfillNameSource classifies the names a database written before the
// name_source column carries.
//
// Those rows all default to "user", which is the safe direction and also the
// useless one: an adopted board is eighty-odd rows named after the one
// directory they share, and none of them would ever be improved. So the pass
// reclaims exactly the names it can prove the manager itself wrote -- the
// working directory's basename, with the collision counter adoption appends --
// and leaves everything else alone. A row called "sample-repo-4" in /repos/sample-repo is
// not a name anybody typed.
//
// It runs once. A person is free to name a row "sample-repo-4" afterwards and keep it.
func (s *Store) BackfillNameSource() error {
	done, err := s.Setting(nameSourceBackfill)
	if err != nil {
		return err
	}
	if done != "" {
		return nil
	}
	rows, err := s.db.Query(`SELECT id, name, cwd FROM sessions WHERE name_source = ?`, SourceUser)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id, name, cwd string
		if err := rows.Scan(&id, &name, &cwd); err != nil {
			rows.Close()
			return err
		}
		if looksDerived(name, cwd) {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.db.Exec(`UPDATE sessions SET name_source = ? WHERE id = ?`, SourceDerived, id); err != nil {
			return err
		}
	}
	return s.SetSetting(nameSourceBackfill, "1")
}

// looksDerived reports whether a name is one adoption would have produced for
// this working directory: its basename, optionally followed by the "-2", "-3"
// counter that disambiguates the second and later rows sharing it.
func looksDerived(name, cwd string) bool {
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return false
	}
	if name == base {
		return true
	}
	suffix, ok := strings.CutPrefix(name, base+"-")
	if !ok {
		return false
	}
	n, err := strconv.Atoi(suffix)
	return err == nil && n >= 2
}
