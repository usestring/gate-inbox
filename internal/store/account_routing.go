package store

import "fmt"

const AccountRoutingSetting = "account_routing"

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
