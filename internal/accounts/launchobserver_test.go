package accounts

import (
	"context"
	"errors"
	"github.com/usestring/gate-inbox/extension"
	"testing"
	"time"
)

type launchObserver struct {
	notices []extension.AccountLaunch
	fail    bool
	panics  bool
}

func (*launchObserver) ChooseAccount(context.Context, extension.AccountRequest) (string, error) {
	panic("must not choose again")
}
func (o *launchObserver) AccountLaunched(ctx context.Context, n extension.AccountLaunch) error {
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded context")
	}
	o.notices = append(o.notices, n)
	if o.panics {
		panic("observer failed")
	}
	if o.fail {
		return errors.New("observer failed")
	}
	return nil
}

func TestSuccessfulLaunchNoticePreservesActualRouteAndOwnLogin(t *testing.T) {
	o := &launchObserver{}
	t.Cleanup(UseChooser(func() (extension.AccountChooser, error) { return o, nil }))
	before := time.Now()
	RecordLaunch("example", "claude", " pool-a ", "CLAUDE_CODE_OAUTH_TOKEN")
	RecordLaunch("example", "codex", "", "")
	if len(o.notices) != 2 {
		t.Fatalf("notices = %d", len(o.notices))
	}
	n := o.notices[0]
	if n.SessionID != "example" || n.ToolName != "claude" || n.Account != "POOL-A" || n.AccountEnv != "CLAUDE_CODE_OAUTH_TOKEN" || n.At.Before(before) || n.At.Location() != time.UTC {
		t.Fatalf("notice = %+v", n)
	}
	if o.notices[1].Account != "" {
		t.Fatal("own login gained an account")
	}
}

func TestLaunchNoticeCannotUndoStartedProcess(t *testing.T) {
	for _, o := range []*launchObserver{{fail: true}, {panics: true}} {
		restore := UseChooser(func() (extension.AccountChooser, error) { return o, nil })
		RecordLaunch("example", "claude", "pool-a", "")
		restore()
		if len(o.notices) != 1 {
			t.Fatal("notice was not attempted")
		}
	}
	restore := UseChooser(func() (extension.AccountChooser, error) { return nil, errors.New("unavailable") })
	defer restore()
	RecordLaunch("example", "claude", "pool-a", "")
}
