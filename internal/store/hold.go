package store

import "context"

// Hold takes the store's one connection and keeps it until release is
// called, the way a slow query elsewhere in the process would. Every other
// call on this store waits until then.
//
// It exists for tests that prove a caller never waits on the store. The
// board's event loop is the one that matters: the poll pass, the extensions
// and the usage monitor all queue on this connection (SetMaxOpenConns(1),
// see connect), and an event loop that read a setting behind them froze the
// board for as long as they held it.
func (s *Store) Hold(ctx context.Context) (release func(), err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return func() { _ = conn.Close() }, nil
}
