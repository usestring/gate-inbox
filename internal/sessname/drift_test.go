package sessname

import "testing"

const anchorTitle = "Build UK business rates overpayment detection system"

func TestTitleHoldsWhileThePromptsStillTouchIt(t *testing.T) {
	d := NewDrift()
	prompts := []string{
		"check the business rates table again",
		"the overpayment detector missed one",
		"rerun rates on the whole borough",
	}
	for i := 0; i < 3; i++ {
		if got := d.Title("s", anchorTitle, prompts); got != anchorTitle {
			t.Fatalf("pass %d = %q, want the title held", i, got)
		}
	}
}

func TestTitleHoldsUntilTheReplacementIsSeenTwice(t *testing.T) {
	d := NewDrift()
	prompts := []string{
		"the grafana dashboard is empty again",
		"grafana still shows nothing for the loki panel",
		"fix the loki grafana panel query",
	}
	if got := d.Title("s", anchorTitle, prompts); got != anchorTitle {
		t.Fatalf("first sighting already renamed: %q", got)
	}
	got := d.Title("s", anchorTitle, prompts)
	if got == anchorTitle {
		t.Fatalf("second sighting did not rename")
	}
	if len(Kebab{}.Compress(got)) == 0 {
		t.Fatalf("drift title %q compressed to nothing", got)
	}
}

func TestOneOffTopicPromptIsNotDrift(t *testing.T) {
	d := NewDrift()
	prompts := []string{
		"the grafana dashboard is empty again",
		"back to the business rates overpayment list",
		"which rates rows are duplicated",
	}
	for i := 0; i < 3; i++ {
		if got := d.Title("s", anchorTitle, prompts); got != anchorTitle {
			t.Fatalf("pass %d renamed on one aside: %q", i, got)
		}
	}
}

func TestTooFewPromptsIsNeverDrift(t *testing.T) {
	d := NewDrift()
	prompts := []string{"grafana loki panel", "grafana loki query"}
	for i := 0; i < 3; i++ {
		if got := d.Title("s", anchorTitle, prompts); got != anchorTitle {
			t.Fatalf("pass %d renamed on %d prompts: %q", i, len(prompts), got)
		}
	}
}

func TestPromptsThatAgreeOnNothingAreNotDrift(t *testing.T) {
	d := NewDrift()
	prompts := []string{
		"look at the kafka consumer",
		"whats wrong with terraform",
		"open the postgres console",
	}
	for i := 0; i < 3; i++ {
		if got := d.Title("s", anchorTitle, prompts); got != anchorTitle {
			t.Fatalf("pass %d renamed on unrelated prompts: %q", i, got)
		}
	}
}

func TestAnEmptyTitleIsNeverReplacedByDrift(t *testing.T) {
	d := NewDrift()
	prompts := []string{"grafana loki panel", "grafana loki query", "grafana loki alert"}
	for i := 0; i < 3; i++ {
		if got := d.Title("s", "", prompts); got != "" {
			t.Fatalf("pass %d invented a title from prompts: %q", i, got)
		}
	}
}

func TestOnceDriftedThePromptsAreMeasuredAgainstTheNewAnchor(t *testing.T) {
	d := NewDrift()
	prompts := []string{
		"the grafana dashboard is empty again",
		"grafana still shows nothing for the loki panel",
		"fix the loki grafana panel query",
	}
	d.Title("s", anchorTitle, prompts)
	drifted := d.Title("s", anchorTitle, prompts)
	if drifted == anchorTitle {
		t.Fatal("never drifted")
	}
	for i := 0; i < 4; i++ {
		if got := d.Title("s", anchorTitle, prompts); got != drifted {
			t.Fatalf("pass %d churned from %q to %q", i, drifted, got)
		}
	}
}

func TestKeepForgetsRowsThatHaveGone(t *testing.T) {
	d := NewDrift()
	prompts := []string{
		"the grafana dashboard is empty again",
		"grafana still shows nothing for the loki panel",
		"fix the loki grafana panel query",
	}
	d.Title("s", anchorTitle, prompts)
	d.Keep(nil)
	if got := d.Title("s", anchorTitle, prompts); got != anchorTitle {
		t.Errorf("state survived the row: %q", got)
	}
}

func TestANilDriftJustHoldsTheTitle(t *testing.T) {
	var d *Drift
	if got := d.Title("s", anchorTitle, nil); got != anchorTitle {
		t.Errorf("got %q", got)
	}
	d.Keep(nil)
	d.Forget("s")
}
