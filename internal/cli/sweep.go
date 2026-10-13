package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sweep"
)

const usageSweep = `sweep [--json] [--db <path>] [--idle 72h] [--waiting 24h] [--errored 24h] [--dead 1h] [--mail 1h]`

func sweepSection() section {
	return section{
		title: "Board hygiene",
		commands: []command{
			{name: "sweep", usage: usageSweep, about: "list stale sessions and undelivered mail with the call that would clear each; reads state.db read-only and changes nothing", run: configCommand(runSweep)},
		},
	}
}

func runSweep(out io.Writer, args []string, _, configDir string) error {
	set := cmdline.NewFlagSet(usageSweep)
	asJSON := cmdline.JSONFlag(set)
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
	path := *db
	if path == "" {
		path = filepath.Join(configDir, "state.db")
	}
	st, err := store.OpenReadOnly(path)
	if err != nil {
		return err
	}
	defer st.Close()
	sessions, err := st.ListSessions(true)
	if err != nil {
		return err
	}
	messages, err := st.WaitingMessages()
	if err != nil {
		return err
	}
	pending := hooks.NewManager(configDir)
	report := sweep.Sweep(sweep.Input{
		Sessions: sessions,
		Messages: messages,
		Question: func(id string) (sweep.Question, bool) { return pendingQuestion(pending.PendingAskFile(id)) },
		Now:      time.Now(),
	}, limits)
	return cmdline.Emit(out, *asJSON, report, sweep.Format(report))
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
