package artifacts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A file on disk is published as its bytes, with the media type read off
// its extension: a PNG cannot travel as a string, and a page the agent has
// already written should not travel twice.
func TestPublishReadsContentFromAFile(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3}
	var seenType string
	var seenBody []byte
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenType = r.Header.Get("Content-Type")
		seenBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{}`))
	}))

	path := writeFile(t, "chart.PNG", png)
	if _, err := publish(context.Background(), client, publisher{session: "s"}, publishArgs{Title: "Chart", ContentPath: path}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if seenType != "image/png" || !bytes.Equal(seenBody, png) {
		t.Fatalf("sent %q / %v, want image/png with the file's bytes", seenType, seenBody)
	}

	// An explicit content_type wins over the extension.
	page := writeFile(t, "report.txt", []byte("<p>hi</p>"))
	if _, err := publish(context.Background(), client, publisher{session: "s"}, publishArgs{Title: "Report", ContentPath: page, ContentType: "text/html"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if seenType != "text/html" {
		t.Fatalf("sent %q, want the explicit type", seenType)
	}
}

func TestPublishRefusesAnUnusableContentPath(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("an invalid publish reached the network")
	}))
	cases := map[string]struct {
		args publishArgs
		want string
	}{
		"both":      {publishArgs{Title: "T", Content: "x", ContentPath: writeFile(t, "a.html", []byte("x"))}, "not both"},
		"relative":  {publishArgs{Title: "T", ContentPath: "a.html"}, "absolute"},
		"missing":   {publishArgs{Title: "T", ContentPath: filepath.Join(t.TempDir(), "gone.html")}, "content_path"},
		"empty":     {publishArgs{Title: "T", ContentPath: writeFile(t, "empty.html", nil)}, "empty"},
		"unknown":   {publishArgs{Title: "T", ContentPath: writeFile(t, "run.sh", []byte("x"))}, "extension"},
		"oversized": {publishArgs{Title: "T", ContentPath: writeFile(t, "big.html", bytes.Repeat([]byte("x"), 2048))}, "over the"},
		"no title":  {publishArgs{ContentPath: writeFile(t, "a.html", []byte("x"))}, "title"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := publish(context.Background(), client, publisher{session: "s"}, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestSaveWritesTheBytesWhereAsked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	saved, err := save("~/out/chart.png", []byte("bytes"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved != filepath.Join(home, "out/chart.png") {
		t.Fatalf("saved to %q", saved)
	}
	if data, err := os.ReadFile(saved); err != nil || string(data) != "bytes" {
		t.Fatalf("read back %q, %v", data, err)
	}
	if _, err := save("chart.png", nil); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative save: err = %v", err)
	}
	out := formatSaved(artifactMeta{Title: "Chart", ContentType: "image/png", Bytes: 5}, saved)
	if !strings.Contains(out, "saved to "+saved) || strings.Contains(out, "bytes\n\nbytes") {
		t.Fatalf("saved report = %q", out)
	}
}
