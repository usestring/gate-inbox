package forge

import (
	"reflect"
	"testing"
)

// A review thread points at a line by its number on one side, so each line must carry the numbers
// GitHub's gutter shows, restarting at every hunk header.
func TestParsePatchNumbersBothSides(t *testing.T) {
	patch := "@@ -3,4 +3,5 @@ func main() {\n" +
		" keep\n" +
		"-gone\n" +
		"+new\n" +
		"+added\n" +
		"\n" +
		"@@ -20 +21 @@\n" +
		"-last\n" +
		"\\ No newline at end of file\n" +
		"+last\n"

	want := []DiffLine{
		{Kind: DiffHunk, Text: "@@ -3,4 +3,5 @@ func main() {"},
		{Kind: DiffContext, Old: 3, New: 3, Text: "keep"},
		{Kind: DiffDelete, Old: 4, Text: "gone"},
		{Kind: DiffAdd, New: 4, Text: "new"},
		{Kind: DiffAdd, New: 5, Text: "added"},
		{Kind: DiffContext, Old: 5, New: 6, Text: ""},
		{Kind: DiffHunk, Text: "@@ -20 +21 @@"},
		{Kind: DiffDelete, Old: 20, Text: "last"},
		{Kind: DiffNote, Text: "\\ No newline at end of file"},
		{Kind: DiffAdd, New: 21, Text: "last"},
	}
	if got := ParsePatch(patch); !reflect.DeepEqual(got, want) {
		t.Errorf("ParsePatch:\n got %+v\nwant %+v", got, want)
	}
}
