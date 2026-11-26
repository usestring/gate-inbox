package parentseal

import "testing"

func TestQuietPointerRoundTrips(t *testing.T) {
	for _, name := range []string{"lead", `tricky ", message 9: "x`, "multi\nline  name", ""} {
		line := QuietPointer("a1b2c3d4", name, 42)
		if id, ok := ParseQuietPointer(line); !ok || id != 42 {
			t.Errorf("name %q: parsed (%d, %v) from %q", name, id, ok, line)
		}
		pasted := "<pasted_content id=\"ab12\">\n" + line + "\n</pasted_content id=\"ab12\">"
		if id, ok := ParseQuietPointer(pasted); !ok || id != 42 {
			t.Errorf("name %q: a pasted pointer parsed (%d, %v)", name, id, ok)
		}
	}
}

// A person typing around a pointer wrote that prompt, and nothing in it is
// read as a delivery.
func TestQuietPointerMustBeTheWholePrompt(t *testing.T) {
	line := QuietPointer("a1b2c3d4", "lead", 42)
	for _, prompt := range []string{"see this: " + line, line + "\nand also this", "[gate-inbox] From agent session x, message 42"} {
		if _, ok := ParseQuietPointer(prompt); ok {
			t.Errorf("parsed a delivery out of %q", prompt)
		}
	}
}
