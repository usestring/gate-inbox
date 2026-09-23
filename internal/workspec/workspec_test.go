package workspec

import "testing"

func TestLinearBranchNamesItsTicket(t *testing.T) {
	for _, branch := range []string{
		"alice/abc-133756-expose-the-observed-failure-value",
		"carol/ABC-134809-quota-failure",
		"feature/abc-120313-grafana",
	} {
		ref, ok := TicketFromBranch(branch)
		if !ok {
			t.Fatalf("%q: no ticket found", branch)
		}
		if ref.Provenance != FromBranch {
			t.Errorf("%q: provenance = %q, want branch", branch, ref.Provenance)
		}
		if ref.Identifier == "" {
			t.Errorf("%q: empty identifier", branch)
		}
	}
}

func TestABranchThatIsNotLinearShapedNamesNothing(t *testing.T) {
	for _, branch := range []string{"main", "feat/gate-inbox", "fix-the-thing"} {
		if ref, ok := TicketFromBranch(branch); ok {
			t.Errorf("%q: found %q, want nothing", branch, ref.Identifier)
		}
	}
}

func TestTicketIdentifiersAreUppercasedFromTheBranch(t *testing.T) {
	// Git branches are conventionally lowercase and Linear's own format writes the key that
	// way; the ticket it names is not lowercase, and the API will not match it if we ask in
	// the branch's casing.
	ref, ok := TicketFromBranch("alice/abc-133756-slug")
	if !ok {
		t.Fatal("no ticket found")
	}
	if ref.Identifier != "ABC-133756" {
		t.Errorf("identifier = %q, want ABC-133756", ref.Identifier)
	}
}

func TestRemoteUrlsReduceToOwnerAndName(t *testing.T) {
	want := "example-org/sample-repo"
	for _, remote := range []string{
		"git@github.com:example-org/sample-repo.git",
		"git@github.com:example-org/sample-repo",
		"ssh://git@github.com/example-org/sample-repo.git",
		"https://github.com/example-org/sample-repo.git",
		"https://github.com/example-org/sample-repo",
	} {
		if got := RepoFromRemote(remote); got != want {
			t.Errorf("%q -> %q, want %q", remote, got, want)
		}
	}
}

func TestAPullRequestUrlCarriesItsOwnRepository(t *testing.T) {
	refs := ScanText("opened https://github.com/example-org/sample-repo/pull/820 just now", "", FromText)
	if len(refs) != 1 {
		t.Fatalf("found %d refs, want 1", len(refs))
	}
	if refs[0].Repo != "example-org/sample-repo" || refs[0].Number != 820 {
		t.Errorf("got %s#%d, want example-org/sample-repo#820", refs[0].Repo, refs[0].Number)
	}
}

// A bare "PR #7394" is the weakest signal the scanner has, and attaching it to the wrong
// repository puts a confident, wrong state on the board. Without a repository to resolve it
// against, it is dropped — an empty column is the better answer.
func TestABarePullReferenceIsDroppedWithoutARepository(t *testing.T) {
	if refs := ScanText("see PR #7394 for the fix", "", FromText); len(refs) != 0 {
		t.Errorf("found %d refs with no repo, want 0: %+v", len(refs), refs)
	}

	refs := ScanText("see PR #7394 for the fix", "example-org/sample-repo", FromText)
	if len(refs) != 1 || refs[0].Repo != "example-org/sample-repo" || refs[0].Number != 7394 {
		t.Errorf("with a repo, got %+v, want example-org/sample-repo#7394", refs)
	}
}

func TestABareNumberIsNotAPullRequest(t *testing.T) {
	// "#7394" on its own appears in prose constantly. The word has to be there.
	if refs := ScanText("bumped the timeout to #7394 ms", "example-org/sample-repo", FromText); len(refs) != 0 {
		t.Errorf("found %d refs, want 0: %+v", len(refs), refs)
	}
}

func TestTicketsAreFoundInProse(t *testing.T) {
	refs := ScanText("this is the ABC-133756 follow-up, not ABC-134809", "", FromText)
	if len(refs) != 2 {
		t.Fatalf("found %d refs, want 2: %+v", len(refs), refs)
	}
	if refs[0].Identifier != "ABC-133756" || refs[1].Identifier != "ABC-134809" {
		t.Errorf("got %q and %q", refs[0].Identifier, refs[1].Identifier)
	}
}

func TestTheSameReferenceFoundTwiceIsOneRow(t *testing.T) {
	refs := Dedupe([]Ref{
		{Kind: KindPR, Repo: "example-org/sample-repo", Number: 7394, Provenance: FromText},
		{Kind: KindPR, Repo: "example-org/sample-repo", Number: 7394, Provenance: FromText},
	})
	if len(refs) != 1 {
		t.Fatalf("deduped to %d, want 1", len(refs))
	}
}

// Deduplication must not lose the better evidence. A PR mentioned in passing and then actually
// opened by the session is one PR, known by its creation.
func TestDeduplicationKeepsTheStrongestEvidence(t *testing.T) {
	refs := Dedupe([]Ref{
		{Kind: KindPR, Repo: "example-org/sample-repo", Number: 7394, Provenance: FromText},
		{Kind: KindPR, Repo: "example-org/sample-repo", Number: 7394, Provenance: FromCreation},
	})
	if len(refs) != 1 {
		t.Fatalf("deduped to %d, want 1", len(refs))
	}
	if refs[0].Provenance != FromCreation {
		t.Errorf("provenance = %q, want pr-create", refs[0].Provenance)
	}
}

func TestIncompleteReferencesAreDroppedRatherThanDrawn(t *testing.T) {
	refs := Dedupe([]Ref{
		{Kind: KindPR, Repo: "", Number: 7394, Provenance: FromText},
		{Kind: KindPR, Repo: "example-org/sample-repo", Number: 0, Provenance: FromText},
		{Kind: KindTicket, Identifier: "", Provenance: FromText},
	})
	if len(refs) != 0 {
		t.Errorf("kept %d incomplete refs: %+v", len(refs), refs)
	}
}

// The board lists every reference, so what provenance decides is the order
// they are read in: the branch first, then the pull request the session
// watched itself open, then whatever it merely said.
func TestTheBranchLeadsAndAMentionTrails(t *testing.T) {
	in := []Ref{
		{Kind: KindTicket, Identifier: "ABC-999999", Provenance: FromText},
		{Kind: KindTicket, Identifier: "ABC-133756", Provenance: FromBranch},
		{Kind: KindPR, Repo: "example-org/sample-repo", Number: 1, Provenance: FromText},
		{Kind: KindPR, Repo: "example-org/sample-repo", Number: 7394, Provenance: FromCreation},
	}
	got := ByEvidence(in)

	want := []string{"ticket:ABC-133756", "pr:example-org/sample-repo#7394", "ticket:ABC-999999", "pr:example-org/sample-repo#1"}
	for i, key := range want {
		if got[i].Key() != key {
			t.Errorf("position %d = %q, want %q", i, got[i].Key(), key)
		}
	}
	// Ordering must not disturb what the caller handed over: the tracker keeps
	// its own discovery order and hands the same slice to every reader.
	if in[0].Identifier != "ABC-999999" {
		t.Errorf("the input was reordered: %+v", in)
	}
}

// Equal evidence keeps discovery order, so two mentions do not swap places
// between one frame and the next.
func TestEqualEvidenceKeepsDiscoveryOrder(t *testing.T) {
	got := ByEvidence([]Ref{
		{Kind: KindTicket, Identifier: "ABC-2", Provenance: FromText},
		{Kind: KindTicket, Identifier: "ABC-1", Provenance: FromText},
	})
	if got[0].Identifier != "ABC-2" || got[1].Identifier != "ABC-1" {
		t.Errorf("got %+v", got)
	}
}

func TestASessionWithNoReferencesOrdersToNothing(t *testing.T) {
	if got := ByEvidence(nil); len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

/* -------------------------------------------------------------- guessed repositories */

// A bare mention records that its repository was inferred, because that is the part nobody said.
func TestABareMentionRecordsThatItsRepositoryWasGuessed(t *testing.T) {
	bare := ScanText("see PR #39", "example-org/sample-repo", FromText)
	if len(bare) != 1 || !bare[0].Inferred {
		t.Errorf("bare mention = %+v, want one ref marked Inferred", bare)
	}
	stated := ScanText("https://github.com/example-org/sample-repo/pull/39", "example-org/sample-repo", FromText)
	if len(stated) != 1 || stated[0].Inferred {
		t.Errorf("url mention = %+v, want one ref not marked Inferred", stated)
	}
}

func TestPruneInferred(t *testing.T) {
	bare := func(n int) Ref {
		return Ref{Kind: KindPR, Repo: "example-org/sample-repo", Number: n, Provenance: FromText, Inferred: true}
	}
	stated := func(repo string, n int, how Provenance) Ref {
		return Ref{Kind: KindPR, Repo: repo, Number: n, Provenance: how}
	}
	ticket := Ref{Kind: KindTicket, Identifier: "ABC-135518", Provenance: FromText}

	for _, c := range []struct {
		name string
		in   []Ref
		want []string
	}{{
		name: "a bare mention with nothing to contradict it stands",
		in:   []Ref{bare(39)},
		want: []string{"pr:example-org/sample-repo#39"},
	}, {
		// The screenshot case: the session said "PR #39" and printed the URL of the pull
		// request it opened. One pull request, said twice.
		name: "a stated reference with the same number drops the guess",
		in:   []Ref{bare(39), stated("example-org/component-a", 39, FromCreation)},
		want: []string{"pr:example-org/component-a#39"},
	}, {
		name: "a stated reference with a different number leaves the guess alone",
		in:   []Ref{bare(39), stated("example-org/component-a", 41, FromCreation)},
		want: []string{"pr:example-org/sample-repo#39", "pr:example-org/component-a#41"},
	}, {
		name: "a stated reference alone passes through",
		in:   []Ref{stated("example-org/component-a", 39, FromText)},
		want: []string{"pr:example-org/component-a#39"},
	}, {
		name: "tickets are never pruned",
		in:   []Ref{ticket, bare(39), stated("example-org/component-a", 39, FromCreation)},
		want: []string{"ticket:ABC-135518", "pr:example-org/component-a#39"},
	}, {
		name: "a guess the stated reference agrees with collapses to one row",
		in:   []Ref{bare(39), stated("example-org/sample-repo", 39, FromCreation)},
		want: []string{"pr:example-org/sample-repo#39"},
	}} {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, ref := range Dedupe(PruneInferred(c.in)) {
				got = append(got, ref.Key())
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

// Dedupe collapsing a guess into a stated reference must clear the mark, or a later prune would
// treat a reference somebody actually named as a guess.
func TestDedupeKeepsTheStatedReference(t *testing.T) {
	refs := Dedupe([]Ref{
		{Kind: KindPR, Repo: "example-org/sample-repo", Number: 39, Provenance: FromText, Inferred: true},
		{Kind: KindPR, Repo: "example-org/sample-repo", Number: 39, Provenance: FromCreation},
	})
	if len(refs) != 1 || refs[0].Inferred {
		t.Errorf("got %+v, want one ref not marked Inferred", refs)
	}
}

func TestANamedPullRequestNeedsAKnownName(t *testing.T) {
	names := map[string]string{"go": "example-org/component-b", "sample-repo": "example-org/sample-repo"}
	refs := ScanNamed("go#1392 is updated; see also sample-repo#7585 and other#3 and #4", names, FromText)
	if len(refs) != 2 {
		t.Fatalf("found %d refs, want 2: %+v", len(refs), refs)
	}
	if refs[0].Repo != "example-org/component-b" || refs[0].Number != 1392 || refs[0].Inferred {
		t.Errorf("first = %+v, want stated example-org/component-b#1392", refs[0])
	}
	if refs[1].Repo != "example-org/sample-repo" || refs[1].Number != 7585 {
		t.Errorf("second = %+v, want example-org/sample-repo#7585", refs[1])
	}
	if got := ScanNamed("go#1392", nil, FromText); got != nil {
		t.Errorf("with no names, got %+v, want nothing", got)
	}
}
