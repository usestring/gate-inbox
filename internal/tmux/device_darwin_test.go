package tmux

import (
	"encoding/binary"
	"slices"
	"testing"
)

func TestEnvironmentFromProcArgs(t *testing.T) {
	cases := []struct {
		name string
		argc uint32
		body string
		want []string
	}{
		{"named device", 2, "/bin/tmux\x00\x00tmux\x00attach\x00GATE_INBOX_DEVICE=tablet\x00TERM=xterm\x00\x00", []string{"GATE_INBOX_DEVICE=tablet", "TERM=xterm"}},
		{"empty argument", 3, "/bin/tmux\x00tmux\x00\x00GATE_INBOX_DEVICE=argument\x00GATE_INBOX_DEVICE=actual\x00", []string{"GATE_INBOX_DEVICE=actual"}},
		{"spaces and equals", 1, "/path with spaces/tmux\x00\x00\x00tmux\x00GATE_INBOX_DEVICE=my tablet\x00OTHER=a=b\x00\x00", []string{"GATE_INBOX_DEVICE=my tablet", "OTHER=a=b"}},
		{"empty environment", 1, "/bin/tmux\x00tmux\x00\x00", nil},
		{"trailing data", 1, "/bin/tmux\x00tmux\x00TERM=xterm\x00\x00GATE_INBOX_DEVICE=trailing\x00", []string{"TERM=xterm"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := binary.NativeEndian.AppendUint32(nil, tc.argc)
			got, err := environmentFromProcArgs(append(data, tc.body...))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("environment = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnvironmentFromProcArgsRejectsMalformedData(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		{1, 0, 0},
		binary.NativeEndian.AppendUint32(nil, 0),
		binary.NativeEndian.AppendUint32(nil, ^uint32(0)),
		append(binary.NativeEndian.AppendUint32(nil, 1), "/bin/tmux"...),
		append(binary.NativeEndian.AppendUint32(nil, 2), "/bin/tmux\x00tmux\x00"...),
		append(binary.NativeEndian.AppendUint32(nil, 1), "/bin/tmux\x00tmux\x00TERM=unterminated"...),
	} {
		if _, err := environmentFromProcArgs(data); err == nil {
			t.Fatalf("accepted malformed process arguments: %q", data)
		}
	}
}
