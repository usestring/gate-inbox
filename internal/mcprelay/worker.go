package mcprelay

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
)

// process is a worker that is a `gate-inbox mcp` child.
type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan []byte
	once  sync.Once
}

// StartProcess runs bin's MCP server as the row id, in configDir.
func StartProcess(bin, configDir, id string) (Worker, error) {
	cmd := exec.Command(bin, "mcp")
	cmd.Env = append(withoutBoardEnv(os.Environ()),
		hooks.EnvSessionID+"="+id, config.HomeEnv+"="+configDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &process{cmd: cmd, stdin: stdin, lines: make(chan []byte)}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
		for scanner.Scan() {
			p.lines <- bytes.Clone(scanner.Bytes())
		}
		_ = cmd.Wait()
		close(p.lines)
	}()
	return p, nil
}

// withoutBoardEnv drops what a launch would have set, which an adopted
// claude never has but a stale shell might.
func withoutBoardEnv(env []string) []string {
	out := env[:0:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, hooks.EnvSessionID+"=") || strings.HasPrefix(entry, config.HomeEnv+"=") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (p *process) Send(line []byte) error {
	_, err := p.stdin.Write(line)
	return err
}

func (p *process) Lines() <-chan []byte { return p.lines }

// Stop closes the worker's stdin, which ends an MCP server, and kills it if
// it is still there a moment later. The lines it wrote meanwhile are drained
// so the reader can reach Wait.
func (p *process) Stop() {
	p.once.Do(func() {
		_ = p.stdin.Close()
		go func() {
			timer := time.AfterFunc(2*time.Second, func() { _ = p.cmd.Process.Kill() })
			for range p.lines {
			}
			timer.Stop()
		}()
	})
}

// Serve runs the relay for the claude session on stdin and stdout. A session
// the board launched carries its own gate-inbox server, which shadows this
// one by name; should both run, this one lists no tools for good.
func Serve(in io.Reader, out io.Writer, configDir, bin, version string) error {
	launched := os.Getenv(hooks.EnvSessionID) != "" || os.Getenv(hooks.EnvStatusFile) != ""
	manager := hooks.NewManager(configDir)
	// The relay starts with its claude, so it is a second way a session
	// started outside the board tells a running board where it is, for a
	// claude whose hooks did not.
	if !launched {
		manager.Announce(os.Getenv("TMUX"), os.Getenv("TMUX_PANE"), os.Getppid())
	}
	stamp := binaryStamp(bin)
	relay := Relay{
		In:      in,
		Out:     out,
		Version: version,
		Caller:  claudeCaller(manager, launched),
		Start: func(id string) (Worker, error) {
			stamp = binaryStamp(bin)
			return StartProcess(bin, configDir, id)
		},
		Upgraded: func() bool {
			now := binaryStamp(bin)
			return now != "" && now != stamp
		},
	}
	return relay.Run()
}

// claudeCaller names the row this relay's claude is on the board as. Only a
// claude's own marker counts: a codex or opencode adopted in the pane is not
// the claude this relay serves, even a claude started from inside it.
func claudeCaller(manager *hooks.Manager, launched bool) func() (string, bool) {
	return func() (string, bool) {
		pane := os.Getenv("TMUX_PANE")
		if launched || pane == "" {
			return "", false
		}
		return manager.AdoptedClaudeCaller(os.Getenv("TMUX"), pane, func() []int {
			return adopt.Ancestors(int32(os.Getpid()))
		})
	}
}

// binaryStamp changes when the installed binary is replaced.
func binaryStamp(bin string) string {
	info, err := os.Stat(bin)
	if err != nil {
		return ""
	}
	return info.ModTime().String() + "/" + strconv.FormatInt(info.Size(), 10)
}
