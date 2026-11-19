package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
)

// resolved identifies the path or pattern; glob quotes the directory supplied
// by the session so SQLite interprets wildcards only in the user's pattern.
type reservationPath struct {
	resolved string
	glob     string
}

var literalReservationDir = strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]")

func resolveReservationPath(dir, pattern string) reservationPath {
	if filepath.IsAbs(dir) {
		dir = canonicalPath(dir)
	}
	if filepath.IsAbs(pattern) {
		path := canonicalPath(filepath.Clean(pattern))
		rel, err := filepath.Rel(dir, path)
		if err != nil || !filepath.IsLocal(rel) {
			return reservationPath{resolved: path, glob: path}
		}
		// Both spellings use the same literal directory prefix.
		pattern = rel
	}
	if !filepath.IsAbs(dir) {
		return reservationPath{}
	}
	return reservationPath{
		resolved: filepath.Join(dir, pattern),
		glob:     filepath.Join(literalReservationDir.Replace(filepath.Clean(dir)), pattern),
	}
}

// canonicalPath is path with links resolved through its nearest existing
// ancestor, so that one directory spelled two ways (macOS's symlinked temp
// directory, or a symlinked checkout) gives one lease path: sessions record
// their directory however they were started, and two in the same checkout
// must see each other's leases. The components that do not exist, a glob's
// wildcards among them, are kept as written.
func canonicalPath(path string) string {
	dir, rest := path, ""
	for {
		if target, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(target, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		dir, rest = parent, filepath.Join(filepath.Base(dir), rest)
	}
}

func reservationDirectory(tx *sql.Tx, sessionID string) (string, error) {
	var dir string
	err := tx.QueryRow(`SELECT cwd FROM sessions WHERE id = ?`, sessionID).Scan(&dir)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return dir, err
}

// Older leases have only the original pattern. Resolve them from the holder's
// recorded directory where possible, then keep that path even if the row moves.
func resolveLegacyReservationPaths(tx *sql.Tx) error {
	rows, err := tx.Query(`
SELECT f.id, f.pattern, COALESCE(s.cwd, '')
  FROM file_reservations f LEFT JOIN sessions s ON s.id = f.session_id
 WHERE f.resolved_path = ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type legacyPath struct {
		id   string
		path reservationPath
	}
	var paths []legacyPath
	for rows.Next() {
		var id, pattern, dir string
		if err := rows.Scan(&id, &pattern, &dir); err != nil {
			return err
		}
		if path := resolveReservationPath(dir, pattern); path.resolved != "" {
			paths = append(paths, legacyPath{id, path})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, legacy := range paths {
		if _, err := tx.Exec(`UPDATE file_reservations SET resolved_path = ?, match_glob = ? WHERE id = ?`,
			legacy.path.resolved, legacy.path.glob, legacy.id); err != nil {
			return err
		}
	}
	return nil
}
