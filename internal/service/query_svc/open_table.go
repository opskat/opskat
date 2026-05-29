package query_svc

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TableColumnRule 描述一列的约束,用于前端 INSERT 校验。
// 与 frontend/src/components/query/TableDataTab.tsx 现有结构对齐。
type TableColumnRule struct {
	Name          string `json:"name"`
	Nullable      bool   `json:"nullable"`
	HasDefault    bool   `json:"hasDefault"`
	AutoIncrement bool   `json:"autoIncrement"`
}

// OpenTableResult 是 OpenTable 一次返回的全部首屏数据。
// 把原来前端 4 次 ExecuteSQL 合并为一次响应。
type OpenTableResult struct {
	Columns     []string          `json:"columns"`
	ColumnTypes map[string]string `json:"columnTypes"`
	ColumnRules []TableColumnRule `json:"columnRules"`
	PrimaryKeys []string          `json:"primaryKeys"`
	TotalCount  int64             `json:"totalCount"`
	FirstPage   []map[string]any  `json:"firstPage"`
	PageSize    int               `json:"pageSize"`
}

// OpenTable 在同一条 *sql.Conn 上顺序执行 4 条 SQL 并组装结果。
// 调用方负责传入已通过缓存复用的 *sql.DB,本函数自己 db.Conn(ctx) 一次。
//
// 4 条查询:
//  1. primary keys (SHOW KEYS / PG information_schema)
//  2. columns + types + rules (SHOW COLUMNS / PG information_schema)
//  3. SELECT COUNT(*) FROM <table>
//  4. SELECT * FROM <table> LIMIT pageSize OFFSET 0
//
// 任一步失败立即返回错误;不做"部分成功"的容错,前端按整次失败处理。
func OpenTable(ctx context.Context, db *sql.DB, driver asset_entity.DatabaseDriver, database, table string, pageSize int) (*OpenTableResult, error) {
	if pageSize <= 0 {
		pageSize = 1000
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open db conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	pks, err := queryPrimaryKeys(ctx, conn, driver, database, table)
	if err != nil {
		return nil, fmt.Errorf("query primary keys: %w", err)
	}

	cols, types, rules, err := queryColumns(ctx, conn, driver, database, table)
	if err != nil {
		return nil, fmt.Errorf("query columns: %w", err)
	}

	tableRef := QuoteTableRef(database, table, driver)

	var total int64
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableRef).Scan(&total); err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	firstPage, dataCols, err := queryFirstPage(ctx, conn, driver, tableRef, pageSize)
	if err != nil {
		return nil, fmt.Errorf("first page: %w", err)
	}

	// 优先使用 SELECT * 的列顺序(与实际 SELECT 行结果对齐);
	// 退化:若空表/无列,落回 SHOW COLUMNS 的结果。
	finalCols := dataCols
	if len(finalCols) == 0 {
		finalCols = cols
	}

	return &OpenTableResult{
		Columns:     finalCols,
		ColumnTypes: types,
		ColumnRules: rules,
		PrimaryKeys: pks,
		TotalCount:  total,
		FirstPage:   firstPage,
		PageSize:    pageSize,
	}, nil
}

func queryPrimaryKeys(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, database, table string) ([]string, error) {
	switch driver {
	case asset_entity.DriverPostgreSQL:
		schema, tbl := splitPGSchemaTable(table)
		sqlText := "SELECT kcu.column_name FROM information_schema.table_constraints tc " +
			"JOIN information_schema.key_column_usage kcu " +
			"ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema " +
			"WHERE tc.table_schema = $1" +
			" AND tc.table_name = $2" +
			" AND tc.constraint_type = 'PRIMARY KEY' ORDER BY kcu.ordinal_position"
		rows, err := conn.QueryContext(ctx, sqlText, schema, tbl)
		if err != nil {
			return nil, err
		}
		return scanPKRows(rows, "column_name")
	case asset_entity.DriverMSSQL:
		schema, tbl := splitMSSQLSchemaTable(table)
		sqlText := "SELECT kcu.COLUMN_NAME FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc " +
			"JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu " +
			"ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME AND tc.TABLE_SCHEMA = kcu.TABLE_SCHEMA " +
			"WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY' " +
			"AND tc.TABLE_SCHEMA = @p1 AND tc.TABLE_NAME = @p2 " +
			"ORDER BY kcu.ORDINAL_POSITION"
		rows, err := conn.QueryContext(ctx, sqlText, sql.Named("p1", schema), sql.Named("p2", tbl))
		if err != nil {
			return nil, err
		}
		return scanPKRows(rows, "COLUMN_NAME")
	case asset_entity.DriverSQLite:
		sqlText := "SELECT name FROM " + sqlitePragmaTableInfo(database) + " WHERE pk > 0 ORDER BY pk" //nolint:gosec // schema is identifier-quoted by sqlitePragmaTableInfo; table name is bound as a parameter.
		rows, err := conn.QueryContext(ctx, sqlText, table)
		if err != nil {
			return nil, err
		}
		return scanPKRows(rows, "name")
	default: // MySQL
		sqlText := "SHOW KEYS FROM " + QuoteTableRef(database, table, driver) + " WHERE Key_name = 'PRIMARY'" //nolint:gosec // table reference is identifier-quoted by QuoteTableRef.
		rows, err := conn.QueryContext(ctx, sqlText)
		if err != nil {
			return nil, err
		}
		return scanPKRows(rows, "Column_name")
	}
}

func scanPKRows(rows *sql.Rows, primaryColName string) ([]string, error) {
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, 4)
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := zipRow(cols, values)
		name := pickString(row, primaryColName, "Column_name", "column_name", "COLUMN_NAME")
		if name != "" {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

func queryColumns(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, database, table string) ([]string, map[string]string, []TableColumnRule, error) {
	var sqlText string
	var args []any
	switch driver {
	case asset_entity.DriverPostgreSQL:
		schema, tbl := splitPGSchemaTable(table)
		sqlText = "SELECT column_name, data_type, udt_name, is_nullable, column_default " +
			"FROM information_schema.columns WHERE table_schema = $1" +
			" AND table_name = $2 ORDER BY ordinal_position"
		args = []any{schema, tbl}
	case asset_entity.DriverMSSQL:
		schema, tbl := splitMSSQLSchemaTable(table)
		sqlText = "SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT " +
			"FROM INFORMATION_SCHEMA.COLUMNS " +
			"WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2 " +
			"ORDER BY ORDINAL_POSITION"
		args = []any{sql.Named("p1", schema), sql.Named("p2", tbl)}
	case asset_entity.DriverSQLite:
		sqlText = `SELECT name, type, CASE "notnull" WHEN 0 THEN 'YES' ELSE 'NO' END AS is_nullable, dflt_value FROM ` + sqlitePragmaTableInfo(database)
		args = []any{table}
	default: // MySQL
		sqlText = "SHOW COLUMNS FROM " + QuoteTableRef(database, table, driver)
	}
	rows, err := conn.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, nil, err
	}
	var names []string
	types := make(map[string]string)
	var rules []TableColumnRule
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, nil, err
		}
		row := zipRow(cols, values)
		name := pickString(row, "column_name", "name", "Field", "field", "COLUMN_NAME")
		if name == "" {
			continue
		}
		typeStr := pickString(row, "data_type", "Type", "type", "udt_name", "DATA_TYPE")
		nullableRaw := strings.ToUpper(pickString(row, "is_nullable", "Null", "null", "IS_NULLABLE"))
		extra := strings.ToLower(pickString(row, "Extra", "extra"))
		defaultRaw, hasDefault := row["column_default"]
		if !hasDefault || defaultRaw == nil {
			defaultRaw, hasDefault = row["Default"]
		}
		if !hasDefault || defaultRaw == nil {
			defaultRaw, hasDefault = row["default"]
		}
		if !hasDefault || defaultRaw == nil {
			defaultRaw, hasDefault = row["COLUMN_DEFAULT"]
		}
		if !hasDefault || defaultRaw == nil {
			defaultRaw = row["dflt_value"]
		}
		names = append(names, name)
		if typeStr != "" {
			types[name] = typeStr
		}
		rules = append(rules, TableColumnRule{
			Name:          name,
			Nullable:      nullableRaw == "YES",
			HasDefault:    defaultRaw != nil,
			AutoIncrement: strings.Contains(extra, "auto_increment"),
		})
	}
	return names, types, rules, rows.Err()
}

func queryFirstPage(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, tableRef string, pageSize int) ([]map[string]any, []string, error) {
	var sqlText string
	switch driver {
	case asset_entity.DriverMSSQL:
		sqlText = fmt.Sprintf("SELECT TOP %d * FROM %s", pageSize, tableRef)
	default:
		sqlText = fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET 0", tableRef, pageSize)
	}
	rows, err := conn.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	out := make([]map[string]any, 0, pageSize)
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			row[col] = val
		}
		out = append(out, row)
	}
	return out, cols, rows.Err()
}

func zipRow(cols []string, values []any) map[string]any {
	row := make(map[string]any, len(cols))
	for i, col := range cols {
		val := values[i]
		if b, ok := val.([]byte); ok {
			val = string(b)
		}
		row[col] = val
	}
	return row
}

func sqlitePragmaTableInfo(database string) string {
	if database == "" {
		return "pragma_table_info(?)"
	}
	return QuoteIdent(database, asset_entity.DriverSQLite) + ".pragma_table_info(?)"
}

// splitPGSchemaTable 把可能带 schema 前缀的 PG 表名拆成 (schema, table)。
// 输入 "events" → ("public", "events");"reporting.events" → ("reporting", "events");
// 与 QuoteTableRef/quoteQualified 的输入空间保持一致——后者会按点号拆分加引号,
// 这里也按同样规则提取 information_schema 查询所需的两段。
func splitPGSchemaTable(table string) (string, string) {
	parts := strings.Split(table, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) >= 2 {
		return out[0], out[len(out)-1]
	}
	if len(out) == 1 {
		return "public", out[0]
	}
	return "public", ""
}

// splitMSSQLSchemaTable 把可能带 schema 前缀的 MSSQL 表名拆成 (schema, table)。
// 输入 "users" → ("dbo", "users");"dbo.users" → ("dbo", "users");
// "sales.orders" → ("sales", "orders")。裸表名默认 dbo schema，与
// QuoteTableRef 的输入空间保持一致——侧边栏列表会以 schema.table 形式下发。
func splitMSSQLSchemaTable(table string) (string, string) {
	parts := strings.Split(table, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) >= 2 {
		return out[0], out[len(out)-1]
	}
	if len(out) == 1 {
		return "dbo", out[0]
	}
	return "dbo", ""
}

func pickString(row map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := row[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}
