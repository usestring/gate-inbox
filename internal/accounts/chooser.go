package accounts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// The core launches a session on the CLI's own login, or on an account
// somebody named. Choosing an account for a launch that names none belongs
// to the build's extension, through extension.AccountChooser; a build with
// none launches on the CLI's own login.

// The launch-account modes the operator picks between in settings.
const (
	// Own launches a session that names no account on the CLI's own login.
	Own = "own"
	// Extension asks the build's account chooser.
	Extension = "extension"
)

// ErrNoChooser is the extension mode with no chooser in the build.
var ErrNoChooser = errors.New("no extension in this build chooses accounts; set launch accounts to own login in settings")

// chooseTimeout bounds one question put to the chooser, which a launch
// waits on.
const chooseTimeout = 30 * time.Second

var (
	chooserMu      sync.Mutex
	resolveChooser = func() (extension.AccountChooser, error) { return nil, nil }
)

// UseChooser sets how this process finds the build's account chooser:
// resolve is called whenever a launch needs one, and returns nil when the
// build has none. It returns a func restoring the previous resolver, for
// tests.
func UseChooser(resolve func() (extension.AccountChooser, error)) (restore func()) {
	chooserMu.Lock()
	defer chooserMu.Unlock()
	previous := resolveChooser
	resolveChooser = resolve
	return func() {
		chooserMu.Lock()
		defer chooserMu.Unlock()
		resolveChooser = previous
	}
}

// Chooser is the build's account chooser, or nil when it has none.
func Chooser() (extension.AccountChooser, error) {
	chooserMu.Lock()
	resolve := resolveChooser
	chooserMu.Unlock()
	return resolve()
}

// ChooserAvailable reports whether an enabled extension chooses accounts,
// so the extension mode is worth offering.
func ChooserAvailable() bool {
	chooser, err := Chooser()
	return err == nil && chooser != nil
}

// Mode is the operator's launch-account mode. Anything but own login in the
// setting, including a value an older build wrote, is the extension's.
func Mode(st *store.Store) (string, error) {
	mode, err := st.Setting(store.AccountRoutingSetting)
	if err != nil {
		return "", err
	}
	if mode == "" || mode == Own {
		return Own, nil
	}
	return Extension, nil
}

// Tool is a tool's account settings as the extension API names them.
func Tool(tool config.Tool) extension.AccountTool {
	return extension.AccountTool{
		Env:    tool.AccountEnv,
		Secret: tool.AccountSecret,
		Listed: func() ([]string, error) { return List(tool) },
	}
}

// Request is what a launch tells the chooser about itself.
type Request struct {
	SessionID string
	ToolName  string
	Reason    extension.LaunchReason
	From      string
}

// Select is the account a launch runs on: the one named, else the
// chooser's pick while the operator has the extension choosing, else the
// CLI's own login.
func Select(st *store.Store, tool config.Tool, named string, req Request) (string, error) {
	return selectAccount(st, tool, named, req, false)
}

// Preview is the account Select would give now, without the chooser
// committing to anything.
func Preview(st *store.Store, tool config.Tool, named string, req Request) (string, error) {
	return selectAccount(st, tool, named, req, true)
}

func selectAccount(st *store.Store, tool config.Tool, named string, req Request, preview bool) (string, error) {
	if tool.AccountEnv == "" {
		return named, nil
	}
	if named = Normalize(named); named != "" {
		return named, nil
	}
	mode, err := Mode(st)
	if err != nil || mode == Own {
		return "", err
	}
	chooser, err := Chooser()
	if err != nil {
		return "", err
	}
	if chooser == nil {
		return "", ErrNoChooser
	}
	return ask(chooser, tool, req, preview)
}

// Route is the account a migration runs on. A migration never names one:
// the chooser picks whatever the launch-account mode, and a build with no
// chooser moves the conversation onto the CLI's own login.
func Route(tool config.Tool, req Request) (string, error) {
	if tool.AccountEnv == "" {
		return "", nil
	}
	chooser, err := Chooser()
	if err != nil || chooser == nil {
		return "", err
	}
	return ask(chooser, tool, req, false)
}

func ask(chooser extension.AccountChooser, tool config.Tool, req Request, preview bool) (chosen string, err error) {
	started := time.Now()
	defer func() {
		tracing.Record("account.selection", started, time.Now(), err,
			tracing.Attr{Key: "session", Value: req.SessionID},
			tracing.Attr{Key: "tool", Value: req.ToolName},
			tracing.Attr{Key: "account.name", Value: chosen},
			tracing.Attr{Key: "launch.reason", Value: string(req.Reason)})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), chooseTimeout)
	defer cancel()
	chosen, err = chooser.ChooseAccount(ctx, extension.AccountRequest{
		SessionID: req.SessionID,
		ToolName:  req.ToolName,
		Tool:      Tool(tool),
		Reason:    req.Reason,
		From:      req.From,
		Preview:   preview,
	})
	if err != nil {
		return "", fmt.Errorf("choosing an account: %w", err)
	}
	return Normalize(chosen), nil
}

// RecordLaunch traces which account a session was launched on.
func RecordLaunch(sessionID, toolName, account string) {
	if account == "" {
		return
	}
	now := time.Now()
	tracing.Record("account.launch", now, now, nil,
		tracing.Attr{Key: "session", Value: sessionID},
		tracing.Attr{Key: "tool", Value: toolName},
		tracing.Attr{Key: "account.name", Value: Normalize(account)})
}
