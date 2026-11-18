package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sweep"
)

const usageSweep = `sweep [--apply [--dry-run] [--revive] [--kill-waiting] [--force]] [--json] [--db <path>] [--idle 72h] [--waiting 24h] [--errored 24h] [--dead 1h] [--mail 1h]`

func sweepSection() section {
	return section{
		title: "Board hygiene",
		commands: []command{
			{name: "sweep", usage: usageSweep, about: "list stale sessions and undelivered mail with the call that would clear each; reads state.db read-only and changes nothing unless --apply is passed, which hibernates or archives the rows nobody is using and holds every row that would lose mail, children, task claims or running work", run: configCommand(runSweep)},
		},
	}
}

func runSweep(out io.Writer, args []string, sessionID, configDir string) error {
	set := cmdline.NewFlagSet(usageSweep)
	asJSON := cmdline.JSONFlag(set)
	apply := set.Bool("apply", false, "carry out the archive proposals: hibernate each row hibernate would take, archive the rest, and hold any row with mail waiting, children on the board, task claims or work running under it")
	var opts sessioncmd.SweepApplyOptions
	set.BoolVar(&opts.DryRun, "dry-run", false, "change nothing, which is what a sweep without --apply does; with --apply, make every check and print what an apply would do")
	set.BoolVar(&opts.Revive, "revive", false, "with --apply, carry out the revive proposals too; each starts a process")
	set.BoolVar(&opts.KillWaiting, "kill-waiting", false, "with --apply, stop sessions parked on a dialog nobody answered; the question is lost")
	set.BoolVar(&opts.Force, "force", false, "with --apply, archive or kill a row although it has mail waiting, children on the board, task claims or work running under it")
	db := set.String("db", "", "state.db to read instead of the board's own, such as a copy")
	limits := sweep.DefaultThresholds
	set.DurationVar(&limits.Idle, "idle", limits.Idle, "how long an idle or finished session sits before it is stale")
	set.DurationVar(&limits.Waiting, "waiting", limits.Waiting, "how long a session waits on a dialog before it is stale")
	set.DurationVar(&limits.Errored, "errored", limits.Errored, "how long an errored session sits before it is stale")
	set.DurationVar(&limits.Dead, "dead", limits.Dead, "how long an ended session stays on the list before it is stale")
	set.DurationVar(&limits.Mail, "mail", limits.Mail, "how old an undelivered message is before it is reported")
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	if !*apply && (opts.Revive || opts.KillWaiting || opts.Force) {
		return fmt.Errorf("usage: %s %s: --revive, --kill-waiting and --force choose what --apply carries out, and do nothing without it", program, usageSweep)
	}
	if *apply && *db != "" {
		return fmt.Errorf("usage: %s %s: --apply acts on the board's own sessions, so it cannot be pointed at another state.db with --db", program, usageSweep)
	}
	path := *db
	if path == "" {
		path = filepath.Join(configDir, "state.db")
	}
	report, err := readSweep(path, configDir, limits)
	if err != nil {
		return err
	}
	if *apply {
		applied, err := sessioncmd.NewSessions(configDir, sessioncmd.CLIVocabulary()).SweepApply(sessionID, report.Proposals, opts)
		if err != nil {
			return err
		}
		report.Applied = &applied
	}
	if err := cmdline.Emit(out, *asJSON, report, sweep.Format(report)); err != nil {
		return err
	}
	if report.Applied != nil && report.Applied.Counts[sweep.Failed] > 0 {
		return fmt.Errorf("%d proposal(s) could not be applied; the report says why", report.Applied.Counts[sweep.Failed])
	}
	return nil
}

// readSweep builds the report from a read-only handle, closed before an
// apply opens the board's own.
func readSweep(path, configDir string, limits sweep.Thresholds) (sweep.Report, error) {
	st, err := store.OpenReadOnly(path)
	if err != nil {
		return sweep.Report{}, err
	}
	defer st.Close()
	sessions, err := st.ListSessions(true)
	if err != nil {
		return sweep.Report{}, err
	}
	messages, err := st.WaitingMessages()
	if err != nil {
		return sweep.Report{}, err
	}
	hibernated, err := st.Hibernated()
	if err != nil {
		return sweep.Report{}, err
	}
	pending := hooks.NewManager(configDir)
	return sweep.Sweep(sweep.Input{
		Sessions:   sessions,
		Messages:   messages,
		Hibernated: hibernated,
		Question:   func(id string) (sweep.Question, bool) { return pendingQuestion(pending.PendingAskFile(id)) },
		Now:        time.Now(),
	}, limits), nil
}

// pendingQuestion reads the first question of the dialog the ask-pending
// hook saved for a session.
func pendingQuestion(path string) (sweep.Question, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sweep.Question{}, false
	}
	var ask convo.AskCall
	if json.Unmarshal(raw, &ask) != nil || len(ask.Questions) == 0 {
		return sweep.Question{}, false
	}
	return sweep.Question{Header: ask.Questions[0].Header, Text: ask.Questions[0].Question, AskedAt: ask.AskedAt}, true
}
