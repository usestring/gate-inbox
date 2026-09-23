package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

const (
	Own      = "own"
	Smart    = "smart"
	usageTTL = time.Minute
	// observedFreshness is how old a pool's reading may be at its source
	// before the account counts as having none.
	observedFreshness = 2 * time.Minute
)

type Window struct {
	Utilization *float64  `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

type Snapshot struct {
	At         time.Time          `json:"at"`
	ObservedAt time.Time          `json:"observed_at,omitempty"`
	Windows    map[string]*Window `json:"windows"`
}

func Mode(st *store.Store) (string, error) {
	mode, err := st.Setting(store.AccountRoutingSetting)
	if err != nil {
		return "", err
	}
	if mode == "" {
		return Own, nil
	}
	if mode != Own && mode != Smart {
		return "", fmt.Errorf("unknown account routing mode %q", mode)
	}
	return mode, nil
}

func (s Snapshot) pressure(now time.Time) (float64, bool) {
	if s.At.After(now) || now.Sub(s.At) > usageTTL || (!s.ObservedAt.IsZero() && (s.ObservedAt.After(now) || now.Sub(s.ObservedAt) > observedFreshness)) {
		return 0, false
	}
	if s.Windows["five_hour"] == nil || s.Windows["seven_day"] == nil {
		return 0, false
	}
	pressure := 0.0
	for name, window := range s.Windows {
		if window == nil {
			continue
		}
		duration := 7 * 24 * time.Hour
		if name == "five_hour" {
			duration = 5 * time.Hour
		}
		if window.Utilization == nil {
			return 0, false
		}
		used := *window.Utilization
		if math.IsNaN(used) || used < 0 || used >= 100 {
			return 0, false
		}
		if used == 0 && window.ResetsAt.IsZero() {
			continue
		}
		if !window.ResetsAt.After(now) {
			return 0, false
		}
		remaining := math.Min(1, window.ResetsAt.Sub(now).Seconds()/duration.Seconds())
		// Reserve 15% after a reset, tapering to 5% just before the next.
		if used >= 95-10*remaining {
			return 0, false
		}
		pressure = math.Max(pressure, used*remaining)
	}
	return pressure, true
}

func snapshot(st *store.Store, tool config.Tool, account string) (Snapshot, error) {
	key := "account_usage:v2:" + tool.AccountSecret + ":" + account
	raw, err := st.Setting(key)
	if err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	if raw != "" && json.Unmarshal([]byte(raw), &snap) == nil {
		fresh := !snap.At.After(time.Now()) && time.Since(snap.At) < usageTTL
		for _, window := range snap.Windows {
			if window != nil && !window.ResetsAt.IsZero() && !window.ResetsAt.After(time.Now()) {
				fresh = false
			}
		}
		if fresh && (snap.ObservedAt.IsZero() || (!snap.ObservedAt.After(time.Now()) && time.Since(snap.ObservedAt) <= observedFreshness)) {
			return snap, nil
		}
	}
	snap, err = readUsage(tool, account)
	if err != nil {
		return Snapshot{}, err
	}
	encoded, err := json.Marshal(snap)
	if err != nil {
		return Snapshot{}, err
	}
	if err := st.SetSetting(key, string(encoded)); err != nil {
		return Snapshot{}, err
	}
	now := time.Now()
	for name, window := range snap.Windows {
		if window == nil || window.Utilization == nil {
			continue
		}
		attrs := []tracing.Attr{
			tracing.Attr{Key: "account.lender", Value: account},
			tracing.Attr{Key: "quota.window", Value: name},
			tracing.Attr{Key: "quota.utilization", Value: *window.Utilization},
		}
		if !window.ResetsAt.IsZero() {
			attrs = append(attrs,
				tracing.Attr{Key: "quota.resets_at", Value: window.ResetsAt.Format(time.RFC3339)},
				tracing.Attr{Key: "quota.reset_seconds", Value: window.ResetsAt.Sub(now).Seconds()})
		}
		tracing.Record("account.usage", now, now, nil, attrs...)
	}
	return snap, nil
}

func Select(st *store.Store, tool config.Tool, named, sessionID string) (chosen string, err error) {
	if tool.AccountEnv == "" {
		return named, nil
	}
	own, loginErr := activeBorrower()
	mode, err := Mode(st)
	if err != nil {
		return "", err
	}
	started := time.Now()
	reason := "own_login"
	defer func() {
		tracing.Record("account.selection", started, time.Now(), err,
			tracing.Attr{Key: "session", Value: sessionID},
			tracing.Attr{Key: "account.borrower", Value: own},
			tracing.Attr{Key: "account.lender", Value: chosen},
			tracing.Attr{Key: "account.mode", Value: mode},
			tracing.Attr{Key: "account.reason", Value: reason})
	}()
	chosen = Normalize(named)
	if chosen != "" {
		reason = "explicit"
	} else if mode == Own {
		chosen = ""
	} else {
		if loginErr != nil {
			reason = "pool_unavailable"
			return "", loginErr
		}
		if tool.AccountEnv != "CLAUDE_CODE_OAUTH_TOKEN" {
			return "", errors.New("smart routing has no usage source for this tool")
		}
		chosen, reason, err = selectSmart(st, tool, own)
		if err != nil {
			return "", err
		}
	}
	if sessionID != "" {
		if err := st.SetSetting("account_borrower:"+sessionID, own); err != nil {
			return "", err
		}
	}
	return chosen, nil
}

func selectSmart(st *store.Store, tool config.Tool, own string) (string, string, error) {
	names, err := members(tool)
	if err != nil {
		return "", "pool_unavailable", fmt.Errorf("cannot list shared subscriptions: %w", err)
	}
	for i := range names {
		names[i] = Normalize(names[i])
	}
	slices.Sort(names)
	names = slices.Compact(names)
	bestBand, ownBand := 10, 10
	var eligible, owned []string
	for _, name := range names {
		snap, err := snapshot(st, tool, name)
		if err != nil {
			now := time.Now()
			tracing.Record("account.unavailable", now, now, err, tracing.Attr{Key: "account.lender", Value: name})
			continue
		}
		pressure, ok := snap.pressure(time.Now())
		if !ok {
			continue
		}
		band := int(pressure / 10)
		if ownedBy(name, own) {
			if band < ownBand {
				ownBand, owned = band, nil
			}
			if band == ownBand {
				owned = append(owned, name)
			}
			continue
		}
		if band < bestBand {
			bestBand, eligible = band, nil
		}
		if band == bestBand {
			eligible = append(eligible, name)
		}
	}
	if len(owned) > 0 {
		chosen, err := st.NextAccount(tool.AccountSecret+":own:"+own, owned)
		return chosen, "own_headroom", err
	}
	if len(eligible) == 0 {
		return "", "pool_exhausted", errors.New("no shared subscription has fresh usable quota; choose own subscription or retry after reset")
	}
	chosen, err := st.NextAccount(tool.AccountSecret, eligible)
	return chosen, "pool_round_robin", err
}

func CarryBorrower(st *store.Store, sourceID, targetID string) error {
	borrower, err := st.Setting("account_borrower:" + sourceID)
	if err != nil {
		return err
	}
	return st.SetSetting("account_borrower:"+targetID, borrower)
}

func RecordLaunch(st *store.Store, sessionID, toolName, account string) {
	if account == "" {
		return
	}
	borrower, err := attribute(st, sessionID)
	now := time.Now()
	tracing.Record("account.launch", now, now, err,
		tracing.Attr{Key: "session", Value: sessionID},
		tracing.Attr{Key: "tool", Value: toolName},
		tracing.Attr{Key: "account.borrower", Value: borrower},
		tracing.Attr{Key: "account.lender", Value: Normalize(account)},
		tracing.Attr{Key: "account.attributed", Value: borrower != ""},
		tracing.Attr{Key: "account.shared", Value: borrower != "" && !ownedBy(account, borrower)})
}

// attribute is the borrower a launch onto an account is charged to. Select
// records one for the sessions it routes; a launch that arrives with its
// account already chosen -- a revive, unpark, switch or restart -- is charged
// to the active login rather than to nobody.
func attribute(st *store.Store, sessionID string) (string, error) {
	borrower, err := st.Setting("account_borrower:" + sessionID)
	if err != nil || borrower != "" {
		return borrower, err
	}
	own, err := activeBorrower()
	if errors.Is(err, ErrNoPool) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return own, st.SetSetting("account_borrower:"+sessionID, own)
}
