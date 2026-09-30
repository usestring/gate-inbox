package sessioncmd

import (
	"sync"

	"github.com/usestring/gate-inbox/internal/store"
)

// An answerDriver answers the question dialog of one CLI. An answer whose
// Question is empty is for the question the dialog is showing.
type answerDriver interface {
	answer(r *runtime, s *Sessions, target store.Session, raw string, answers []QuestionAnswer, submit bool,
		guard *answerGuard, by, byID string) (AnsweredQuestion, error)
}

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
