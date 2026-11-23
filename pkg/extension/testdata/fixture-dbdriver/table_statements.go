//go:build !fixture_hostabi10 && !fixture_cursor_dump

package main

import (
	"context"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// TableStatements writes a dump's table rows in the guest (sqldb.TableStatementsWriter):
// the session's cursor read and its row writer written a row at a time, so the
// values never cross to the host and the file is the one the host writes through
// the cursor and the writer. Built with the fixture_cursor_dump tag the dialect has
// no such hook, and the host dumps the rows that way.
func (dialect) TableStatements(ctx context.Context, table sqldb.TableStatementsTable) (dbdriver.StatementCursor, error) {
	return table.RowInsertStatements(ctx)
}
