package sqlite

import "github.com/opskat/opskat/pkg/dbdriver/sqldb"

// 清空：SQLite 没有 TRUNCATE，用 DELETE FROM；也没有新建数据库。
var _ sqldb.TableTruncator = dialect{}

func (dialect) TruncateTableSQL(tableRef string) string { return "DELETE FROM " + tableRef }
