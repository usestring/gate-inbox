package store

import (
	"testing"
	"time"
)

func TestArchiveStoppedLaunch(t *testing.T) {
	for _, scenario := range []string{"stopped", "revived", "new launch stopped", "already archived", "legacy timestamp"} {
		t.Run(scenario, func(t *testing.T) {
			s := openRetentionStore(t)
			if err := s.UpdateStatus("one", "dead"); err != nil {
				t.Fatal(err)
			}
			if scenario == "legacy timestamp" {
				if _, err := s.db.Exec(`UPDATE sessions SET created_at = ? WHERE id = 'one'`, time.Now().Unix()); err != nil {
					t.Fatal(err)
				}
			}
			original, err := s.Get("one")
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "revived":
				if err := s.UpdateStatus("one", "working"); err != nil {
					t.Fatal(err)
				}
			case "new launch stopped":
				if err := s.SetAgentLaunchedAt("one", original.LaunchTime().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			case "already archived":
				if err := s.SetArchived("one", true); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.Get("one")
			if err != nil {
				t.Fatal(err)
			}
			archived, err := s.ArchiveStoppedLaunch(original)
			want := scenario == "stopped" || scenario == "legacy timestamp"
			if err != nil || archived != want {
				t.Fatalf("archived = %v, want %v: %v", archived, want, err)
			}
			after, err := s.Get("one")
			if err != nil {
				t.Fatal(err)
			}
			if want {
				if !after.Archived || after.ArchivedAt.IsZero() {
					t.Fatalf("archive not recorded: %+v", after)
				}
			} else if after.Archived != before.Archived || !after.ArchivedAt.Equal(before.ArchivedAt) {
				t.Fatalf("refused archive changed row: %+v", after)
			}
		})
	}
}
