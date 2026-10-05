package ui

import "github.com/usestring/gate-inbox/internal/store"

// Asking again before a destructive action is per action. Silencing the kill
// dialog must not also silence the restart one, because the two cost
// different things: a kill files an agent away and U brings it back, while a
// restart leaves the conversation behind. So each action names its own
// setting, and the dialog's "don't ask again" writes only that one.
const (
	confirmAlways = "always"
	confirmNever  = "never"

	deleteConfirmSetting  = "delete_confirm"
	restartConfirmSetting = "restart_confirm"
)

// confirmAskModes is every confirm setting's cycle order.
var confirmAskModes = []string{confirmAlways, confirmNever}

// confirmAskSetting is the setting that silences one destructive action's
// dialog, or "" for an action the board always asks about.
func confirmAskSetting(action string) string {
	switch action {
	case actionArchive:
		return archiveConfirmSetting
	case actionDelete:
		return deleteConfirmSetting
	case actionRestart:
		return restartConfirmSetting
	}
	return ""
}

// storedConfirmAsk reads one confirm setting. An unreadable store or an
// unknown value reads as "always": the safe end of the setting is asking,
// never skipping.
func storedConfirmAsk(st *store.Store, setting string) string {
	chosen, err := st.Setting(setting)
	if err != nil {
		return confirmAlways
	}
	return normalizeConfirmAsk(chosen)
}

func normalizeConfirmAsk(chosen string) string {
	for _, mode := range confirmAskModes {
		if chosen == mode {
			return mode
		}
	}
	return confirmAlways
}

// confirmSilenceable reports whether the dialog now built can be answered
// "don't ask again" for its own action, and so whether the dialog offers that
// choice at all.
//
// One picked-out thing only, and never a wide answer: x on a group takes its
// whole subtree and X takes every row on screen, and neither is a keystroke
// aimed at something the operator chose -- which is the thing that makes a
// silent answer safe. X keeps its tick for the same reason it has one.
func (m *Model) confirmSilenceable() bool {
	if m.confirm.ack != "" {
		return false
	}
	switch m.confirm.action {
	case actionArchive:
		return !m.confirm.isGroup && len(m.confirm.sessions) > 0
	case actionRestart:
		return !m.confirm.isGroup && len(m.confirm.sessions) == 1
	case actionDelete:
		// Deleting an empty group is one row the operator picked out and
		// nothing runs under it, so there is no agent to lose.
		return m.confirm.isGroup
	}
	return false
}

// skipsConfirm reports whether the dialog now built can be answered without
// being shown, because its action's own setting says to stop asking.
func (m *Model) skipsConfirm() bool {
	if !m.confirmSilenceable() {
		return false
	}
	setting := confirmAskSetting(m.confirm.action)
	return setting != "" && storedConfirmAsk(m.store, setting) == confirmNever
}

// silenceConfirm turns this action's dialog off for good: its setting goes to
// never, so the next one of its kind is answered without a dialog. The
// in-memory mirror is updated with it, since the board reads that until the
// settings screen next reloads it.
func (m *Model) silenceConfirm() error {
	setting := confirmAskSetting(m.confirm.action)
	if setting == "" {
		return nil
	}
	if err := m.store.SetSetting(setting, confirmNever); err != nil {
		return err
	}
	switch m.confirm.action {
	case actionArchive:
		m.archiveConfirm = confirmNever
	case actionDelete:
		m.deleteConfirm = confirmNever
	case actionRestart:
		m.restartConfirm = confirmNever
	}
	return nil
}
