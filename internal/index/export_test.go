package index

import (
	"context"
	"database/sql"
	"time"
)

func (ix *Index) DB() *sql.DB { return ix.db }

type Tx = *sql.Tx

func (ix *Index) InTx(ctx context.Context, f func(Tx) error) error {
	return ix.inTx(ctx, func(ctx context.Context, tx *sql.Tx) error { return f(tx) })
}

var Reset = reset

// SetBusyTimeout sets the busy timeout of the indexes opened next, and gives the one it replaces.
func SetBusyTimeout(d time.Duration) time.Duration {
	old := busyTimeout
	busyTimeout = d
	return old
}
