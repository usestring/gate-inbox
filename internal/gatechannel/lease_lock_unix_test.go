//go:build darwin || linux

package gatechannel

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLeaseContender(t *testing.T) {
	if len(os.Args) < 4 || os.Args[len(os.Args)-3] != "--lease-contender" {
		return
	}
	dir, consumer := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	fmt.Println("ready")
	if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if consumer == "release" {
		if err := Release(dir, "expired"); err != nil && !strings.Contains(err.Error(), "token does not match") {
			t.Fatal(err)
		}
		return
	}
	_, err := Renew(dir, consumer, "", true, time.Second, MaxLeaseTTL, time.Unix(100, 0))
	if err == nil {
		fmt.Println("acquired")
	} else if !strings.Contains(err.Error(), "holds the gate channel") {
		t.Fatal(err)
	}
}

func TestLeaseOwnershipAcrossProcesses(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			dir := t.TempDir()
			if expired {
				if err := writeJSON(leasePath(dir), Lease{Consumer: "old", Token: "expired", ExpiresAt: time.Unix(99, 0)}); err != nil {
					t.Fatal(err)
				}
			}
			type contender struct {
				cmd    *exec.Cmd
				input  io.WriteCloser
				output *bufio.Reader
			}
			var contenders []contender
			for i := range 8 {
				consumer := fmt.Sprintf("consumer-%d", i)
				if i == 7 && expired {
					consumer = "release"
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestLeaseContender$", "--", "--lease-contender", dir, consumer)
				input, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				out, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				cmd.Stderr = os.Stderr
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill() })
				output := bufio.NewReader(out)
				if ready, err := output.ReadString('\n'); err != nil || ready != "ready\n" {
					t.Fatalf("contender readiness: %q, %v", ready, err)
				}
				contenders = append(contenders, contender{cmd, input, output})
			}
			for _, c := range contenders {
				if _, err := c.input.Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
				_ = c.input.Close()
			}
			acquired := 0
			for _, c := range contenders {
				output, err := io.ReadAll(c.output)
				if err != nil {
					t.Fatal(err)
				}
				if err := c.cmd.Wait(); err != nil {
					t.Fatalf("contender: %v\n%s", err, output)
				}
				if strings.Contains(string(output), "acquired\n") {
					acquired++
				}
			}
			if acquired != 1 {
				t.Fatalf("%d consumers acquired the lease, want 1", acquired)
			}
			if lease, ok := ReadLease(dir); !ok || lease.Token == "expired" {
				t.Fatal("the new lease was lost")
			}
		})
	}
}
