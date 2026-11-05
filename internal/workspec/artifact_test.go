package workspec

import (
	"slices"
	"testing"
)

// Both Claude artifact shapes and an artifact service's /a/art_ link are published pages, and the
// id is the part of the address that names the page.
func TestArtifactRefReadsEveryPublishedShape(t *testing.T) {
	for _, tc := range []struct{ url, id string }{
		{"https://claude.ai/artifact/H9XE1gDSESPDiKoD6MmZGc", "H9XE1gDSESPDiKoD6MmZGc"},
		{"https://claude.ai/code/artifact/0b6c6d1e-5f2a-4c4e-9d0a-7f1f3c2b9e11", "0b6c6d1e-5f2a-4c4e-9d0a-7f1f3c2b9e11"},
		{"https://artifacts.example.test/a/art_rEhwwItYH9wu-EYblFTmnQ", "art_rEhwwItYH9wu-EYblFTmnQ"},
	} {
		ref, ok := ArtifactRef(tc.url, "Report")
		if !ok {
			t.Fatalf("%s: not read as a published page", tc.url)
		}
		if ref.Kind != KindArtifact || ref.Identifier != tc.id || ref.URL != tc.url || ref.Title != "Report" {
			t.Errorf("%s: got %+v", tc.url, ref)
		}
		if ref.Provenance != FromCreation {
			t.Errorf("%s: provenance = %q, want the session's own publish", tc.url, ref.Provenance)
		}
		if ref.Key() != "artifact:"+tc.url {
			t.Errorf("%s: key = %q", tc.url, ref.Key())
		}
	}
	for _, url := range []string{
		"", "https://claude.ai/chat/abc", "https://github.com/o/r/pull/1",
		"see https://claude.ai/artifact/abc", "https://example.test/a/not-an-artifact",
	} {
		if ref, ok := ArtifactRef(url, ""); ok {
			t.Errorf("%q read as a published page: %+v", url, ref)
		}
	}
}

// A publish result prints the address among other text, sometimes twice; each address comes out
// once, in order.
func TestArtifactURLsFindsEachAddressOnce(t *testing.T) {
	text := "Published /tmp/x.html at https://claude.ai/artifact/H9XE1gDSESPDiKoD6MmZGc (Version 1)\n" +
		`{"url":"https://artifacts.example.test/a/art_abc_D-1","public_url":null}` + "\n" +
		"again https://claude.ai/artifact/H9XE1gDSESPDiKoD6MmZGc."
	got := ArtifactURLs(text)
	want := []string{"https://claude.ai/artifact/H9XE1gDSESPDiKoD6MmZGc", "https://artifacts.example.test/a/art_abc_D-1"}
	if !slices.Equal(got, want) {
		t.Errorf("urls = %q, want %q", got, want)
	}
}

// Prose never yields a published page: ScanText reads what a session said, and a session links
// other people's pages as often as its own.
func TestScanTextLeavesArtifactLinksAlone(t *testing.T) {
	for _, ref := range ScanText("look at https://claude.ai/artifact/H9XE1gDSESPDiKoD6MmZGc", "o/r", FromText) {
		if ref.Kind == KindArtifact {
			t.Errorf("a mentioned page became work: %+v", ref)
		}
	}
}

// The same page published twice is one row, carrying the title it was last published under.
func TestDedupeKeepsOnePageAndItsNewestTitle(t *testing.T) {
	first, _ := ArtifactRef("https://claude.ai/artifact/abc", "Draft")
	second, _ := ArtifactRef("https://claude.ai/artifact/abc", "Final")
	untitled, _ := ArtifactRef("https://claude.ai/artifact/abc", "")
	got := Dedupe([]Ref{first, second, untitled, {Kind: KindArtifact}})
	if len(got) != 1 || got[0].Title != "Final" {
		t.Errorf("dedupe = %+v, want one page titled Final", got)
	}
}
