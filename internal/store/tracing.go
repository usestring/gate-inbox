package store

import (
	"time"

	"github.com/usestring/gate-inbox/internal/tracing"
)

// The store is traced at the level of its operations rather than its
// statements.
//
// Every caller here shares one connection -- SetMaxOpenConns(1), for the
// reason Reopen gives -- so a slow query is never slow only for whoever
// issued it: the poll pass, the keypress behind it and every `gate-inbox
// mcp` process queue on the same handle. What that queue costs has had no
// answer. A pass's phase timings say its eleven-odd queries took three and a
// half milliseconds between them without saying that two of those were one
// unindexed scan of the sessions table, and a statement-level timer would
// name the statement without saying which pass was waiting on it. A span
// carries both, so "what did the database cost this pass" and "which query
// was slow" become one query each.
//
// Only the operations something waits on carry one. A span per accessor
// would allocate on the hot path to report a primary-key lookup nobody has
// ever waited for, which is noise bought with the very time this measures.
//
// Each traced method keeps its body under an unexported name and the
// exported one becomes the guard. The default build therefore reaches the
// same code it always did past one atomic load, and a reader can see at the
// call site that nothing is built while nothing is recording.

// recordOp emits one store operation's span, closing it at the moment of the
// call.
//
// It exists so that every span this package writes carries the same
// attribute under the same name: spans that disagree about what a field is
// called cannot be grouped by it, and grouping reads against writes is most
// of what these are for. Whatever is particular to an operation -- the row
// count it returned, the flag that decides which of two queries it ran --
// the caller adds.
func recordOp(name string, start time.Time, err error, write bool, attrs ...tracing.Attr) {
	spanAttrs := make([]tracing.Attr, 0, len(attrs)+1)
	spanAttrs = append(spanAttrs, tracing.Attr{Key: "write", Value: write})
	spanAttrs = append(spanAttrs, attrs...)
	tracing.Record(name, start, time.Now(), err, spanAttrs...)
}
