package sessioncmd

import (
	"errors"
	"sync"

	"github.com/usestring/gate-inbox/internal/store"
)

type answerDriver interface {
	answer(r *runtime, s *Sessions, target store.Session, raw string, answers []QuestionAnswer, submit bool,
		guard *answerGuard, by, byID string) (AnsweredQuestion, error)
}

// errAskShaped is a driver handing the pane back: what it shows is a
// question dialog in Claude Code's own shape, answered by the Claude path.
var errAskShaped = errors.New("the dialog is a question in Claude Code's shape")

var (
	driversMu     sync.RWMutex
	answerDrivers = map[string]answerDriver{}
)

func registerAnswerDriver(tool string, driver answerDriver) {
	driversMu.Lock()
	defer driversMu.Unlock()
	answerDrivers[tool] = driver
}

func answerDriverFor(tool string) (answerDriver, bool) {
	driversMu.RLock()
	defer driversMu.RUnlock()
	driver, ok := answerDrivers[tool]
	return driver, ok
}
