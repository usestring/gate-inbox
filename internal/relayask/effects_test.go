package relayask

import (
	"strings"
	"testing"
)

// derive is the command a child stopped on and its parent relayed to
// its user as a 600-character question, its job ids and paths renamed.
const derive = `S=/var/agent/5e1f0c2a/scratchpad; cd $S; rm -f out/*; for j in job-1 job-2 job-3 job-4; do for v in base fix; do systemd-run --user --scope -q -p MemoryMax=4G ./bin/graph-$v derive -har gcs/$j/capture.har -record gcs/$j/capture.json -o out/$j-$v.json 2>out/$j-$v.log || prlimit --as=8000000000 ./bin/graph-$v derive -har gcs/$j/capture.har -record gcs/$j/capture.json -o out/$j-$v.json 2>>out/$j-$v.log; echo "$j $v $?"; done; done; ls -la out/*.json`

func TestTheDeriveReplayReadsAsScratchpadOnly(t *testing.T) {
	e := Read(derive)
	if want := []string{"scratchpad/out/*"}; strings.Join(e.Deletes, "|") != strings.Join(want, "|") {
		t.Errorf("deletes %q, want %q", e.Deletes, want)
	}
	for _, w := range e.Writes {
		if !strings.HasPrefix(w, "scratchpad/out/") {
			t.Errorf("write %q is outside the scratchpad", w)
		}
	}
	if len(e.Writes) != 2 || len(e.Network) != 0 || len(e.Cloud) != 0 || e.Root {
		t.Errorf("effects %+v", e)
	}
	if strings.Join(e.Limits, "|") != "MemoryMax=4G|as=8000000000" {
		t.Errorf("limits %q", e.Limits)
	}
	want := "Seen in the command: deletes scratchpad/out/*; writes scratchpad/out/$j-$v.json, " +
		"scratchpad/out/$j-$v.log; no network, cloud or production command; limited to MemoryMax=4G, as=8000000000."
	if got := e.Summary(); got != want {
		t.Errorf("summary\n got %s\nwant %s", got, want)
	}
}

func TestEffectsNameNetworkCloudAndRoot(t *testing.T) {
	for command, want := range map[string]string{
		`curl -fsS https://api.example.com/v1/x -o "$TMPDIR/x.json"`: "writes $TMPDIR/x.json; names nothing it deletes; network: api.example.com, curl; no cloud or production command",
		`git push origin HEAD`:                          "names no file it writes or deletes; network: git push; no cloud or production command",
		`gcloud run deploy api --project durable-alpha`: "cloud or production: gcloud run",
		`sudo rm -rf /var/cache/x`:                      "deletes /var/cache/x; names nothing else it writes; no network, cloud or production command; runs as root",
		`da msg run --env production feed`:              "cloud or production: names production",
		`bash -c 'cd /srv/x && rm -rf build'`:           "deletes /srv/x/build;",
		`echo "$(rm -f "$S"/*.tmp)" done`:               "deletes $S/*.tmp;",
		`gsutil cp out.json gs://bucket-prod/x`:         "cloud or production: names prod, gsutil cp",
		`echo hi > /dev/null 2>&1`:                      "names no file it writes or deletes; no network",
		`timeout 60 go test ./... | tee "log 1.txt"`:    "writes log 1.txt; names nothing it deletes; no network, cloud or production command; limited to timeout 60",
	} {
		if got := Read(command).Summary(); !strings.Contains(got, want) {
			t.Errorf("%s\n got %s\nwant it to hold %s", command, got, want)
		}
	}
}
