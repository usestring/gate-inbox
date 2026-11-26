package store

import "time"

const LaunchOverlapSetting = "experimental_launch_overlap"

func (s Session) LaunchOverlapEligible(since time.Time) bool {
	return !since.IsZero() && !s.Archived && s.TmuxPaneID == "" && s.CreatedAt.After(since) &&
		s.LaunchTime().Equal(s.CreatedAt) && s.MigrationID == ""
}

// LaunchOverlapSince limits the experiment to sessions created after opt-in.
// Otherwise the next prompt in a long-running session would count as a launch.
func (s *Store) LaunchOverlapSince() time.Time {
	value, err := s.Setting(LaunchOverlapSetting)
	if err != nil {
		return time.Time{}
	}
	since, _ := time.Parse(time.RFC3339Nano, value)
	return since
}

func (s *Store) SetLaunchOverlap(enabled bool, now time.Time) error {
	if !enabled {
		return s.SetSetting(LaunchOverlapSetting, "off")
	}
	// Saving unrelated settings must not move the start of an enabled experiment.
	_, err := s.db.Exec(`
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value
WHERE settings.value = 'off'`, LaunchOverlapSetting, now.UTC().Format(time.RFC3339Nano))
	return err
}
