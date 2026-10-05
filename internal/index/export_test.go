package index

import "database/sql"

func (ix *Index) DB() *sql.DB { return ix.db }
