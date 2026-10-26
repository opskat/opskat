package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// DDL 是方言里「一张表的建表语句怎么得到」：各驱动在一条 *sql.Conn 上用自己的方式取（读
// 数据库存着的原文，或从 information_schema 重建）。表不存在时返回空串。
//
// 实现 Dialect 的类型同时实现 DDL 才提供查看建表语句；没实现的方言，Session.TableDDL
// 返回 dbdriver.ErrUnsupported。
type DDL interface {
	TableDDL(ctx context.Context, conn *sql.Conn, database, table string) (string, error)
}

// TableDDL 在一条独立连接上取建表语句。
func (s *Session) TableDDL(ctx context.Context, database, table string) (string, error) {
	ddl, ok := s.dialect.(DDL)
	if !ok {
		return "", fmt.Errorf("%w: this driver does not provide table DDL", dbdriver.ErrUnsupported)
	}
	return onConn(ctx, s, func(conn *sql.Conn) (string, error) {
		return ddl.TableDDL(ctx, conn, database, table)
	})
}

// StarterQuery 用方言的表引用与取前几行的写法生成起始查询语句；不碰数据库。
func (s *Session) StarterQuery(_ context.Context, database, table string, limit int) (string, error) {
	tables, err := s.tables()
	if err != nil {
		return "", err
	}
	return tables.StarterSQL(tables.TableRef(database, table), limit), nil
}

// DDLColumn 是从目录重建建表语句时的一列。
type DDLColumn struct {
	Name     string
	Type     string
	Nullable bool
	// Default 是默认值表达式，原文；空串表示没有默认值。
	Default string
}

// BuildCreateTable 把列与主键写成一条 CREATE TABLE。tableRef 已按方言加好引号；没有列时
// 返回空串（表不存在）。
func BuildCreateTable(tableRef string, columns []DDLColumn, primaryKeys []string, quote func(string) string) string {
	if len(columns) == 0 {
		return ""
	}
	defs := make([]string, 0, len(columns)+1)
	for _, column := range columns {
		def := quote(column.Name) + " " + column.Type
		if !column.Nullable {
			def += " NOT NULL"
		}
		if column.Default != "" {
			def += " DEFAULT " + column.Default
		}
		defs = append(defs, def)
	}
	if len(primaryKeys) > 0 {
		quoted := make([]string, len(primaryKeys))
		for i, key := range primaryKeys {
			quoted[i] = quote(key)
		}
		defs = append(defs, "PRIMARY KEY ("+strings.Join(quoted, ", ")+")")
	}
	return fmt.Sprintf("CREATE TABLE %s (\n  %s\n);", tableRef, strings.Join(defs, ",\n  "))
}
