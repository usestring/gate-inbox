package communications

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

func TestDisabledConfigDoesNotCreateState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	e := New()
	if err := e.Configure(extension.NewConfig(nil).WithDataDir(dir)); err != nil {
		t.Fatal(err)
	}
	if e.enabled {
		t.Fatal("communications is on without opting in")
	}
	if err := e.run(t.Context(), []string{"list"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("disabled command succeeded")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("disabled extension created state")
	}
}

func TestCLIImportReviewAndDraft(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	e := New()
	if err := e.Configure(extension.NewConfig(map[string]any{"enabled": true}).WithDataDir(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("Configure performed I/O")
	}
	run := func(args []string, input string) string {
		t.Helper()
		var out bytes.Buffer
		if err := e.run(t.Context(), args, strings.NewReader(input), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	data, err := json.Marshal([]Message{sample("sms")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run([]string{"import"}, string(data)), `"imported": 1`) {
		t.Fatal("import did not report success")
	}
	var listed []Summary
	if err := json.Unmarshal([]byte(run([]string{"list"}, "")), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	var c Conversation
	if err := json.Unmarshal([]byte(run([]string{"read", listed[0].ID}, "")), &c); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]string{"conversation_id": c.ID, "revision": c.Revision, "body": "Yes."})
	if err != nil {
		t.Fatal(err)
	}
	var draft Draft
	if err := json.Unmarshal([]byte(run([]string{"draft"}, string(request))), &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Body != "Yes." || draft.Source != "sms" || draft.InReplyTo != "message-one" {
		t.Fatalf("draft = %+v", draft)
	}
	run([]string{"mark-read", c.ID, c.Revision}, "")
	var read Conversation
	if err := json.Unmarshal([]byte(run([]string{"read", c.ID}, "")), &read); err != nil {
		t.Fatal(err)
	}
	if read.Unread || len(read.Drafts) != 1 {
		t.Fatalf("read = %+v", read)
	}
	if err := e.run(t.Context(), []string{"send"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("communications exposed a send command")
	}
}

func TestDecodeRejectsTrailingAndOversizedInput(t *testing.T) {
	for _, input := range []string{"[] []", strings.Repeat(" ", (8<<20)+1)} {
		var value []Message
		if err := decode(strings.NewReader(input), &value); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestHelpWorksWithoutEnablingExtension(t *testing.T) {
	e := New()
	var out bytes.Buffer
	err := e.run(t.Context(), []string{"-h"}, strings.NewReader(""), &out)
	if !errors.Is(err, flag.ErrHelp) || !strings.Contains(out.String(), "mark-read <id> <revision>") {
		t.Fatalf("help = %q, %v", out.String(), err)
	}
}
