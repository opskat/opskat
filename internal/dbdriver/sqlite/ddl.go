package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 建表语句：SQLite 把原文存在 sqlite_master.sql 里。库名是 schema 名。
var _ sqldb.DDL = dialect{}

func (dialect) TableDDL(ctx context.Context, conn *sql.Conn, database, table string) (string, error) {
	var text sql.NullString
	err := conn.QueryRowContext(ctx,
		"SELECT sql FROM "+qualify(database, "sqlite_master")+" WHERE type IN ('table', 'view') AND name = ? LIMIT 1", table).
		Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text.String, err
}
