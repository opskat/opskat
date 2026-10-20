package mysql

import (
	"context"
	"database/sql"
	"regexp"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 建表语句：MySQL 自己存着原文，SHOW CREATE TABLE 直接给。表名只加引号，不带库名——连接
// 已经限定了库。
var _ sqldb.DDL = dialect{}

// createStatement 认出结果集里装着建表语句的那一列。
var createStatement = regexp.MustCompile(`(?i)CREATE\s+(TABLE|VIEW)`)

func (dialect) TableDDL(ctx context.Context, conn *sql.Conn, _, table string) (string, error) {
	rows, err := conn.QueryContext(ctx, "SHOW CREATE TABLE "+quoteIdent(table))
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if !rows.Next() {
		return "", rows.Err()
	}
	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	if err := rows.Scan(targets...); err != nil {
		return "", err
	}
	return pickCreateStatement(values), nil
}

// pickCreateStatement 取第一个像 CREATE TABLE / CREATE VIEW 的值；都不像时（视图的定义以
// CREATE ALGORITHM=… 开头）取第二列，没有第二列取第一列。
func pickCreateStatement(values []any) string {
	texts := make([]string, len(values))
	for i, value := range values {
		switch v := value.(type) {
		case string:
			texts[i] = v
		case []byte:
			texts[i] = string(v)
		default:
			continue
		}
		if createStatement.MatchString(texts[i]) {
			return texts[i]
		}
	}
	switch {
	case len(texts) > 1:
		return texts[1]
	case len(texts) == 1:
		return texts[0]
	}
	return ""
}
