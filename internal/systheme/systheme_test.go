// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package systheme

import "testing"

func TestParseOSC11(t *testing.T) {
	tests := []struct {
		name     string
		response string
		r, g, b  int
		ok       bool
	}{
		{"xterm 16-bit, ST", "\x1b]11;rgb:0f0f/1111/1515\x1b\\", 15, 17, 21, true},
		{"8-bit, BEL", "\x1b]11;rgb:fd/f6/e3\a", 253, 246, 227, true},
		{"4-bit channels", "\x1b]11;rgb:f/f/f\a", 255, 255, 255, true},
		{"no color spec", "\x1b]11;?\a", 0, 0, 0, false},
		{"wrong channel count", "\x1b]11;rgb:aa/bb\a", 0, 0, 0, false},
		{"garbage", "hello", 0, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, g, b, ok := parseOSC11(tt.response)
			if r != tt.r || g != tt.g || b != tt.b || ok != tt.ok {
				t.Errorf("parseOSC11(%q) = %d,%d,%d,%v want %d,%d,%d,%v",
					tt.response, r, g, b, ok, tt.r, tt.g, tt.b, tt.ok)
			}
		})
	}
}

func TestBackgroundFormatsTheReply(t *testing.T) {
	for _, tt := range []struct {
		name    string
		r, g, b int
		ok      bool
		want    string
		wantOK  bool
	}{
		{name: "dark", r: 0x0f, g: 0x11, b: 0x15, ok: true, want: "#0f1115", wantOK: true},
		{name: "channels below sixteen pad", r: 1, g: 2, b: 3, ok: true, want: "#010203", wantOK: true},
		{name: "white", r: 255, g: 255, b: 255, ok: true, want: "#ffffff", wantOK: true},
		{name: "no answer", ok: false, want: "", wantOK: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := background(func() (int, int, int, bool) {
				return tt.r, tt.g, tt.b, tt.ok
			})
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("background() = %q, %v; want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
