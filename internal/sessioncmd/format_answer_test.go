package sessioncmd

import (
	"strings"
	"testing"
)

func TestFormatAnswerNamesTheOptionAnAnswerLandedOn(t *testing.T) {
	line := FormatAnswer(AnsweredQuestion{Name: "child", SessionID: "abcd1234", Submitted: true, Verified: true,
		Answers: []FilledAnswer{
			{Index: 1, Header: "Language", Answer: "rust", Selected: "Rust"},
			{Index: 2, Header: "Database", Answer: "Postgres with read replicas", Selected: "Postgres"},
			{Index: 3, Header: "Deploy", Answer: "Nomad on our own racks"},
		}})
	for _, want := range []string{`1 Language: picked "Rust";`, `2 Database: picked "Postgres" for "Postgres with read replicas";`,
		`3 Deploy: typed "Nomad on our own racks";`} {
		if !strings.Contains(line, want) {
			t.Errorf("line lacks %s:\n%s", want, line)
		}
	}
}
