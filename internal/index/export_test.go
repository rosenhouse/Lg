package index

import (
	"context"
	"database/sql"
)

func (ix *Index) DB() *sql.DB { return ix.db }

type Tx = *sql.Tx

func (ix *Index) InTx(ctx context.Context, f func(Tx) error) error {
	return ix.inTx(ctx, func(ctx context.Context, tx *sql.Tx) error { return f(tx) })
}

var Reset = reset
