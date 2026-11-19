package parentseal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCallerTokenProvesOnlyItsOwnSession(t *testing.T) {
	dir := t.TempDir()
	alice, err := CallerToken(dir, "alice01")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := CallerToken(dir, "bob0001")
	if err != nil {
		t.Fatal(err)
	}
	if alice == bob {
		t.Fatal("two sessions share a token")
	}
	if again, _ := CallerToken(dir, "alice01"); again != alice {
		t.Fatal("a session's token changed between launches")
	}
	if !VerifyCaller(dir, "alice01", alice) {
		t.Fatal("a session's own token did not verify")
	}
	if VerifyCaller(dir, "alice01", bob) || VerifyCaller(dir, "alice01", "") {
		t.Fatal("another session's token, or none, verified")
	}
}

func TestCallerKeyIsMintedPrivatelyAndOnlyOnRequest(t *testing.T) {
	dir := t.TempDir()
	if token, err := ExistingCallerToken(dir, "alice01"); err != nil || token != "" {
		t.Fatalf("got %q, %v before any key was minted", token, err)
	}
	if VerifyCaller(dir, "alice01", "") {
		t.Fatal("an empty token verified with no key")
	}
	if _, err := os.Stat(KeyDir(dir)); !os.IsNotExist(err) {
		t.Fatal("reading a token minted a key")
	}
	if _, err := CallerToken(dir, "alice01"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(KeyDir(dir), callerKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key is %v, want 0600", info.Mode().Perm())
	}
}

func TestCallerTokenRefusesWhatIsNotASessionID(t *testing.T) {
	if _, err := CallerToken(t.TempDir(), "../alice"); err == nil {
		t.Fatal("a path was taken for a session id")
	}
}

// Launches mint at once; every one of them reads the same whole key.
func TestCallerKeyMintedConcurrentlyIsOneWholeKey(t *testing.T) {
	dir := t.TempDir()
	tokens := make(chan string, 32)
	errs := make(chan error, 32)
	for range 32 {
		go func() {
			token, err := CallerToken(dir, "alice01")
			tokens <- token
			errs <- err
		}()
	}
	first := ""
	for range 32 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		token := <-tokens
		if first == "" {
			first = token
		}
		if token != first {
			t.Fatal("concurrent mints disagree on the key")
		}
	}
	entries, err := os.ReadDir(KeyDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("key dir holds %d entries, want the key alone", len(entries))
	}
}

// A binding resolves to its session only with that session's token in it:
// one written by hand, or carrying another session's token, binds nothing.
func TestAgentSessionBindingVerifies(t *testing.T) {
	dir := t.TempDir()
	if err := BindAgentSession(dir, "ses_a1", "alice01"); err != nil {
		t.Fatal(err)
	}
	if got := AgentSessionCaller(dir, "ses_a1"); got != "alice01" {
		t.Fatalf("bound to %q, want alice01", got)
	}
	info, err := os.Stat(filepath.Join(KeyDir(dir), agentSessionDir, "ses_a1"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("binding %v, %v; want a 0600 file", info, err)
	}
	bob, _ := CallerToken(dir, "bob0001")
	forged := filepath.Join(KeyDir(dir), agentSessionDir, "ses_forged")
	if err := os.WriteFile(forged, []byte("alice01 "+bob+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := AgentSessionCaller(dir, "ses_forged"); got != "" {
		t.Fatalf("a binding with another session's token resolved to %q", got)
	}
	if got := AgentSessionCaller(dir, "ses_none"); got != "" {
		t.Fatalf("an unbound session resolved to %q", got)
	}
	if err := BindAgentSession(dir, "../ses", "alice01"); err == nil {
		t.Fatal("a path was taken for an agent session id")
	}
}

// A server's proof depends on the key and the nonce, and is never a token.
func TestServerProofIsKeyedAndFresh(t *testing.T) {
	dir := t.TempDir()
	one, err := ServerProof(dir, "nonce1")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := ServerProof(dir, "nonce1"); again != one {
		t.Fatal("the same nonce gave two proofs")
	}
	if other, _ := ServerProof(dir, "nonce2"); other == one {
		t.Fatal("two nonces gave one proof")
	}
	if elsewhere, _ := ServerProof(t.TempDir(), "nonce1"); elsewhere == one {
		t.Fatal("another key gave the same proof")
	}
}
