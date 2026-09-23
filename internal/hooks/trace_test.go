package hooks

import (
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tracetest"
)

// Three reads per session per pass, on a board with dozens of sessions, is the
// only reason these are traced at all -- so the span has to say which of the
// three it was and whether the file was there. Without the mailbox name the
// spans are an undifferentiated pile and the question "which of them costs
// anything" cannot be asked.
func TestEachMailboxReadNamesItself(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := manager.Write("abcd1234", status.Working); err != nil {
		t.Fatalf("write status: %v", err)
	}

	spans := tracetest.Capture(t)
	manager.Read("abcd1234")
	manager.ReadName("abcd1234")
	manager.ReadPriority("abcd1234")
	recorded := spans()

	reads := tracetest.Named(recorded, "hooks.read")
	if len(reads) != 3 {
		t.Fatalf("recorded %d reads, want one per mailbox", len(reads))
	}
	found := map[string]bool{}
	for _, read := range reads {
		mailbox, ok := read.Attr("mailbox").(string)
		if !ok {
			t.Fatalf("a read span carries no mailbox: %v", read.Attrs)
		}
		found[mailbox] = read.Attr("found") == true
		if read.Attr("session") != "abcd1234" {
			t.Errorf("%s read reports session %v, want abcd1234", mailbox, read.Attr("session"))
		}
	}
	if !found["status"] {
		t.Error("the status file was written, so its read must report found")
	}
	if found["name"] || found["priority"] {
		t.Error("no name or priority was written, so those reads must report not found")
	}
}

// A mailbox that is not there is the ordinary case, not a failure: most of
// these reads miss by design, and marking them as errors would paint a board
// doing exactly what it should entirely red.
func TestAMissingMailboxIsNotAFailedSpan(t *testing.T) {
	manager := NewManager(t.TempDir())

	spans := tracetest.Capture(t)
	manager.ReadName("abcd1234")
	recorded := spans()

	span := tracetest.One(t, recorded, "hooks.read")
	if span.Failed {
		t.Error("a mailbox with nothing in it must not record a failure")
	}
	if span.Attr("found") != false {
		t.Errorf("found = %v, want false", span.Attr("found"))
	}
}

// A name file is written by an agent, a status file by a hook. Neither belongs
// in a span: what the span reports is that a file was read, not what was in it.
func TestAReadNeverCarriesWhatWasInTheFile(t *testing.T) {
	manager := NewManager(t.TempDir())
	name := "a-name-an-agent-chose"
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatalf("create hooks dir: %v", err)
	}
	if err := os.WriteFile(manager.NameFile("abcd1234"), []byte(name), 0o644); err != nil {
		t.Fatalf("write name: %v", err)
	}

	spans := tracetest.Capture(t)
	if got, _ := manager.ReadName("abcd1234"); got != name {
		t.Fatalf("ReadName = %q, want %q", got, name)
	}
	recorded := spans()

	// The read has to have been recorded at all before the absence of the
	// name in it means anything: a test that only looked for the contents
	// would pass just as well against a read nothing measures.
	span := tracetest.One(t, recorded, "hooks.read")
	if span.Attr("found") != true {
		t.Errorf("found = %v, want true: the file was there", span.Attr("found"))
	}
	for key, value := range span.Attrs {
		if text, ok := value.(string); ok && strings.Contains(text, name) {
			t.Fatalf("attribute %q carries the file's contents: %q", key, text)
		}
	}
}

// The writes cost more than the reads and happen when somebody is waiting: a
// settings file is written before a session can launch at all.
func TestTheWritesAreRecordedToo(t *testing.T) {
	manager := NewManager(t.TempDir())

	spans := tracetest.Capture(t)
	if _, err := manager.EnsureSettings(); err != nil {
		t.Fatalf("EnsureSettings: %v", err)
	}
	if err := manager.Write("abcd1234", status.Working); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := manager.Write("abcd1234", "not-a-status"); err == nil {
		t.Fatal("an unknown status must be refused")
	}
	recorded := spans()

	tracetest.One(t, recorded, "hooks.settings")
	writes := tracetest.Named(recorded, "hooks.write")
	if len(writes) != 2 {
		t.Fatalf("recorded %d status writes, want 2", len(writes))
	}
	if writes[0].Failed {
		t.Error("the first write succeeded and must not carry a failure")
	}
	if !writes[1].Failed {
		t.Error("the refused write must carry its failure, or a rejected status looks like a written one")
	}
}
