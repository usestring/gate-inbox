package store

import "fmt"

const AccountRoutingSetting = "account_routing"

// PeekAccount is the account the next NextAccount call would return for
// the same names, without taking the turn.
func (s *Store) PeekAccount(tool string, names []string) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("no eligible accounts for %s", tool)
	}
	var turn int64
	err := s.db.QueryRow(`SELECT COALESCE((SELECT CAST(value AS INTEGER) + 1 FROM settings WHERE key = ?), 0)`,
		"account_rotation:"+tool).Scan(&turn)
	if err != nil {
		return "", err
	}
	return names[turn%int64(len(names))], nil
}

func (s *Store) NextAccount(tool string, names []string) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("no eligible accounts for %s", tool)
	}
	var turn int64
	err := s.db.QueryRow(`INSERT INTO settings (key, value) VALUES (?, '0')
		ON CONFLICT(key) DO UPDATE SET value = CAST(value AS INTEGER) + 1
		RETURNING CAST(value AS INTEGER)`, "account_rotation:"+tool).Scan(&turn)
	if err != nil {
		return "", err
	}
	return names[turn%int64(len(names))], nil
}
