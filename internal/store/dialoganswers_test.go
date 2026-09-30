package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestDialogAnswerLedger(t *testing.T) {
	st, err := Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	agent := DialogAnswer{TargetSession: "child", TargetToolUseID: "toolu_c", QuestionHash: "q1", Answer: "Yes",
		BySession: "parent", Mode: AnswerByAgent}
	id, err := st.RecordAnswer(agent)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.AnswersFor("child", "toolu_c")
	if err != nil || len(rows) != 1 || rows[0].State != AnswerPending || rows[0].Answer != "Yes" || rows[0].BySession != "parent" {
		t.Fatalf("rows = %+v, %v; want one pending row", rows, err)
	}
	if err := st.MarkAnswer(id, AnswerFailed); err != nil {
		t.Fatal(err)
	}
	if typed, err := st.AgentAnswered("child", "toolu_c"); err != nil || !typed {
		t.Fatalf("AgentAnswered = %v, %v; a failed agent row still counts", typed, err)
	}
	if typed, _ := st.AgentAnswered("child", "toolu_other"); typed {
		t.Fatal("AgentAnswered matched another call")
	}

	relayed := DialogAnswer{TargetSession: "child", TargetToolUseID: "toolu_c2", QuestionHash: "q1", Answer: "Yes",
		BySession: "parent", Mode: AnswerRelayedUser, EvidenceToolUseID: "toolu_p"}
	if _, err := st.RecordAnswer(relayed); err != nil {
		t.Fatal(err)
	}
	if used, _ := st.EvidenceUsed("toolu_p", "q1"); !used {
		t.Fatal("EvidenceUsed missed the relayed row")
	}
	if used, _ := st.EvidenceUsed("toolu_p", "q2"); used {
		t.Fatal("EvidenceUsed matched another question of the same dialog")
	}
	if _, err := st.RecordAnswer(relayed); !errors.Is(err, ErrEvidenceUsed) {
		t.Fatalf("second row citing the same evidence: err = %v, want ErrEvidenceUsed", err)
	}
	relayed.QuestionHash = "q2"
	if _, err := st.RecordAnswer(relayed); err != nil {
		t.Fatalf("another question of the same parent dialog: %v", err)
	}
	if typed, _ := st.AgentAnswered("child", "toolu_c2"); typed {
		t.Fatal("a relayed row read as an agent's")
	}
}
