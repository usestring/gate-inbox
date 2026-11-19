package sessioncmd

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/migrate"
)

// pi --continue resumes the newest *.jsonl in the session's folder, so a pi
// copy written beside the session file must not end in .jsonl; every other
// layout, and a pi copy written elsewhere, keeps the name it was asked for.
func TestPiCopiesBesideTheSessionFileDropJSONL(t *testing.T) {
	src := "/s/--repo--/2026-11-10T11-22-12-760Z_e6f55c57.jsonl"
	pi := migrate.Transcript{Kind: "pi", Path: src}
	for _, c := range []struct {
		transcript migrate.Transcript
		dst, want  string
	}{
		{pi, src + ".handover.jsonl", src + ".handover"},
		{pi, src + ".rewind.jsonl", src + ".rewind"},
		{pi, "/elsewhere/copy.jsonl", "/elsewhere/copy.jsonl"},
		{migrate.Transcript{Kind: "claude", Path: "/c/x.jsonl"}, "/c/x.jsonl.handover.jsonl", "/c/x.jsonl.handover.jsonl"},
	} {
		if got := copyPath(c.transcript, c.dst); got != c.want {
			t.Errorf("copyPath(%s, %q) = %q, want %q", c.transcript.Kind, c.dst, got, c.want)
		}
	}
}
