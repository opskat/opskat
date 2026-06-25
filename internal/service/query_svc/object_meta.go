package query_svc

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ColumnMeta describes one column for the object browser.
type ColumnMeta struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey"`
}

// IndexMeta describes one index.
type IndexMeta struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// ForeignKeyMeta describes one foreign-key column mapping.
type ForeignKeyMeta struct {
	Name             string `json:"name"`
	Column           string `json:"column"`
	ReferencedTable  string `json:"referencedTable"`
	ReferencedColumn string `json:"referencedColumn"`
}

// TableMetadata is the full object-browser detail for a single table.
type TableMetadata struct {
	Columns     []ColumnMeta     `json:"columns"`
	Indexes     []IndexMeta      `json:"indexes"`
	ForeignKeys []ForeignKeyMeta `json:"foreignKeys"`
}

// ObjectMeta names a schema object (view / routine / trigger).
type ObjectMeta struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// DatabaseObjects groups non-table schema objects for the object browser.
type DatabaseObjects struct {
	Views      []ObjectMeta `json:"views"`
	Procedures []ObjectMeta `json:"procedures"`
	Functions  []ObjectMeta `json:"functions"`
	Triggers   []ObjectMeta `json:"triggers"`
}

// GetTableMetadata returns columns + indexes + foreign keys for a table.
// Columns reuse the same per-driver query as OpenTable; indexes / foreign keys
// are best-effort per driver (SQLite/MySQL are precise; PostgreSQL/MSSQL pull
// from the catalog with the common cases covered).
func GetTableMetadata(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, database, table string) (*TableMetadata, error) {
	cols, types, rules, err := queryColumns(ctx, conn, driver, database, table)
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}
	pks, err := queryPrimaryKeys(ctx, conn, driver, database, table)
	if err != nil {
		return nil, fmt.Errorf("primary keys: %w", err)
	}
	pkSet := make(map[string]struct{}, len(pks))
	for _, pk := range pks {
		pkSet[pk] = struct{}{}
	}
	nullableByName := make(map[string]bool, len(rules))
	for _, r := range rules {
		nullableByName[r.Name] = r.Nullable
	}

	columns := make([]ColumnMeta, 0, len(cols))
	for _, c := range cols {
		_, isPK := pkSet[c]
		columns = append(columns, ColumnMeta{
			Name:       c,
			Type:       types[c],
			Nullable:   nullableByName[c],
			PrimaryKey: isPK,
		})
	}

	indexes, err := queryIndexes(ctx, conn, driver, database, table)
	if err != nil {
		return nil, fmt.Errorf("indexes: %w", err)
	}
	fks, err := queryForeignKeys(ctx, conn, driver, database, table)
	if err != nil {
		return nil, fmt.Errorf("foreign keys: %w", err)
	}

	return &TableMetadata{Columns: columns, Indexes: indexes, ForeignKeys: fks}, nil
}

func queryIndexes(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, database, table string) ([]IndexMeta, error) {
	switch driver {
	case asset_entity.DriverSQLite:
		return sqliteIndexes(ctx, conn, database, table)
	case asset_entity.DriverMySQL:
		return mysqlIndexes(ctx, conn, database, table)
	case asset_entity.DriverPostgreSQL:
		return pgIndexes(ctx, conn, table)
	case asset_entity.DriverMSSQL:
		return mssqlIndexes(ctx, conn, table)
	default:
		return nil, nil
	}
}

func sqliteIndexes(ctx context.Context, conn *sql.Conn, database, table string) ([]IndexMeta, error) {
	listSQL := "SELECT name, \"unique\" FROM pragma_index_list(?)"
	if database != "" {
		listSQL = "SELECT name, \"unique\" FROM " + QuoteIdent(database, asset_entity.DriverSQLite) + ".pragma_index_list(?)" //nolint:gosec // schema identifier-quoted
	}
	rows, err := conn.QueryContext(ctx, listSQL, table)
	if err != nil {
		return nil, err
	}
	type idx struct {
		name   string
		unique bool
	}
	var list []idx
	for rows.Next() {
		var name string
		var uniq int
		if err := rows.Scan(&name, &uniq); err != nil {
			_ = rows.Close()
			return nil, err
		}
		list = append(list, idx{name: name, unique: uniq == 1})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	out := make([]IndexMeta, 0, len(list))
	for _, it := range list {
		colRows, err := conn.QueryContext(ctx, "SELECT name FROM pragma_index_info(?)", it.name)
		if err != nil {
			return nil, err
		}
		var cols []string
		for colRows.Next() {
			var c sql.NullString
			if err := colRows.Scan(&c); err != nil {
				_ = colRows.Close()
				return nil, err
			}
			if c.Valid {
				cols = append(cols, c.String)
			}
		}
		_ = colRows.Close()
		out = append(out, IndexMeta{Name: it.name, Columns: cols, Unique: it.unique})
	}
	return out, nil
}

func mysqlIndexes(ctx context.Context, conn *sql.Conn, _, table string) ([]IndexMeta, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX", table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	order := []string{}
	byName := map[string]*IndexMeta{}
	for rows.Next() {
		var name, col string
		var nonUnique int
		if err := rows.Scan(&name, &nonUnique, &col); err != nil {
			return nil, err
		}
		m, ok := byName[name]
		if !ok {
			m = &IndexMeta{Name: name, Unique: nonUnique == 0}
			byName[name] = m
			order = append(order, name)
		}
		m.Columns = append(m.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]IndexMeta, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

func pgIndexes(ctx context.Context, conn *sql.Conn, table string) ([]IndexMeta, error) {
	schema, tbl := splitPGSchemaTable(table)
	rows, err := conn.QueryContext(ctx,
		"SELECT i.relname, ix.indisunique, a.attname "+
			"FROM pg_class t "+
			"JOIN pg_namespace n ON n.oid = t.relnamespace "+
			"JOIN pg_index ix ON ix.indrelid = t.oid "+
			"JOIN pg_class i ON i.oid = ix.indexrelid "+
			"JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(ix.indkey) "+
			"WHERE n.nspname = $1 AND t.relname = $2 "+
			"ORDER BY i.relname, a.attnum", schema, tbl)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	order := []string{}
	byName := map[string]*IndexMeta{}
	for rows.Next() {
		var name, col string
		var unique bool
		if err := rows.Scan(&name, &unique, &col); err != nil {
			return nil, err
		}
		m, ok := byName[name]
		if !ok {
			m = &IndexMeta{Name: name, Unique: unique}
			byName[name] = m
			order = append(order, name)
		}
		m.Columns = append(m.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]IndexMeta, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

func mssqlIndexes(ctx context.Context, conn *sql.Conn, table string) ([]IndexMeta, error) {
	schema, tbl := splitMSSQLSchemaTable(table)
	rows, err := conn.QueryContext(ctx,
		"SELECT i.name, i.is_unique, c.name "+
			"FROM sys.indexes i "+
			"JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id "+
			"JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id "+
			"JOIN sys.objects o ON o.object_id = i.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE s.name = @p1 AND o.name = @p2 AND i.name IS NOT NULL "+
			"ORDER BY i.name, ic.key_ordinal",
		sql.Named("p1", schema), sql.Named("p2", tbl))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	order := []string{}
	byName := map[string]*IndexMeta{}
	for rows.Next() {
		var name, col string
		var unique bool
		if err := rows.Scan(&name, &unique, &col); err != nil {
			return nil, err
		}
		m, ok := byName[name]
		if !ok {
			m = &IndexMeta{Name: name, Unique: unique}
			byName[name] = m
			order = append(order, name)
		}
		m.Columns = append(m.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]IndexMeta, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

func queryForeignKeys(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, database, table string) ([]ForeignKeyMeta, error) {
	switch driver {
	case asset_entity.DriverSQLite:
		return sqliteForeignKeys(ctx, conn, database, table)
	case asset_entity.DriverMySQL:
		return mysqlForeignKeys(ctx, conn, table)
	case asset_entity.DriverPostgreSQL:
		return pgForeignKeys(ctx, conn, table)
	case asset_entity.DriverMSSQL:
		return mssqlForeignKeys(ctx, conn, table)
	default:
		return nil, nil
	}
}

func sqliteForeignKeys(ctx context.Context, conn *sql.Conn, database, table string) ([]ForeignKeyMeta, error) {
	listSQL := "SELECT \"table\", \"from\", \"to\" FROM pragma_foreign_key_list(?)"
	if database != "" {
		listSQL = "SELECT \"table\", \"from\", \"to\" FROM " + QuoteIdent(database, asset_entity.DriverSQLite) + ".pragma_foreign_key_list(?)" //nolint:gosec // schema identifier-quoted
	}
	rows, err := conn.QueryContext(ctx, listSQL, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ForeignKeyMeta
	for rows.Next() {
		var refTable, from, to string
		if err := rows.Scan(&refTable, &from, &to); err != nil {
			return nil, err
		}
		out = append(out, ForeignKeyMeta{Column: from, ReferencedTable: refTable, ReferencedColumn: to})
	}
	return out, rows.Err()
}

func mysqlForeignKeys(ctx context.Context, conn *sql.Conn, table string) ([]ForeignKeyMeta, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME "+
			"FROM information_schema.KEY_COLUMN_USAGE "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND REFERENCED_TABLE_NAME IS NOT NULL "+
			"ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION", table)
	if err != nil {
		return nil, err
	}
	return scanForeignKeyRows(rows)
}

func pgForeignKeys(ctx context.Context, conn *sql.Conn, table string) ([]ForeignKeyMeta, error) {
	schema, tbl := splitPGSchemaTable(table)
	rows, err := conn.QueryContext(ctx,
		"SELECT tc.constraint_name, kcu.column_name, ccu.table_name, ccu.column_name "+
			"FROM information_schema.table_constraints tc "+
			"JOIN information_schema.key_column_usage kcu ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema "+
			"JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema "+
			"WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = $1 AND tc.table_name = $2 "+
			"ORDER BY tc.constraint_name", schema, tbl)
	if err != nil {
		return nil, err
	}
	return scanForeignKeyRows(rows)
}

func mssqlForeignKeys(ctx context.Context, conn *sql.Conn, table string) ([]ForeignKeyMeta, error) {
	schema, tbl := splitMSSQLSchemaTable(table)
	rows, err := conn.QueryContext(ctx,
		"SELECT fk.name, pc.name, rt.name, rc.name "+
			"FROM sys.foreign_keys fk "+
			"JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id "+
			"JOIN sys.tables pt ON pt.object_id = fk.parent_object_id "+
			"JOIN sys.schemas s ON s.schema_id = pt.schema_id "+
			"JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id "+
			"JOIN sys.tables rt ON rt.object_id = fkc.referenced_object_id "+
			"JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id "+
			"WHERE s.name = @p1 AND pt.name = @p2 ORDER BY fk.name",
		sql.Named("p1", schema), sql.Named("p2", tbl))
	if err != nil {
		return nil, err
	}
	return scanForeignKeyRows(rows)
}

func scanForeignKeyRows(rows *sql.Rows) ([]ForeignKeyMeta, error) {
	defer func() { _ = rows.Close() }()
	var out []ForeignKeyMeta
	for rows.Next() {
		var name, col, refTable, refCol sql.NullString
		if err := rows.Scan(&name, &col, &refTable, &refCol); err != nil {
			return nil, err
		}
		out = append(out, ForeignKeyMeta{
			Name:             name.String,
			Column:           col.String,
			ReferencedTable:  refTable.String,
			ReferencedColumn: refCol.String,
		})
	}
	return out, rows.Err()
}

// ListDatabaseObjects returns views / procedures / functions / triggers for the
// database. SQLite has no stored procedures/functions, so those groups stay empty.
func ListDatabaseObjects(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, database string) (*DatabaseObjects, error) {
	switch driver {
	case asset_entity.DriverSQLite:
		return sqliteObjects(ctx, conn, database)
	case asset_entity.DriverMySQL:
		return mysqlObjects(ctx, conn)
	case asset_entity.DriverPostgreSQL:
		return pgObjects(ctx, conn)
	case asset_entity.DriverMSSQL:
		return mssqlObjects(ctx, conn)
	default:
		return &DatabaseObjects{}, nil
	}
}

func sqliteObjects(ctx context.Context, conn *sql.Conn, database string) (*DatabaseObjects, error) {
	master := "sqlite_master"
	if database != "" {
		master = QuoteIdent(database, asset_entity.DriverSQLite) + ".sqlite_master"
	}
	rows, err := conn.QueryContext(ctx, "SELECT type, name FROM "+master+" WHERE type IN ('view','trigger') ORDER BY name") //nolint:gosec // schema identifier-quoted
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := &DatabaseObjects{}
	for rows.Next() {
		var typ, name string
		if err := rows.Scan(&typ, &name); err != nil {
			return nil, err
		}
		if typ == "view" {
			out.Views = append(out.Views, ObjectMeta{Name: name, Type: "view"})
		} else {
			out.Triggers = append(out.Triggers, ObjectMeta{Name: name, Type: "trigger"})
		}
	}
	return out, rows.Err()
}

func mysqlObjects(ctx context.Context, conn *sql.Conn) (*DatabaseObjects, error) {
	out := &DatabaseObjects{}
	if err := collectObjects(ctx, conn,
		"SELECT TABLE_NAME FROM information_schema.VIEWS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME",
		"view", &out.Views); err != nil {
		return nil, err
	}
	if err := collectObjects(ctx, conn,
		"SELECT ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = 'PROCEDURE' ORDER BY ROUTINE_NAME",
		"procedure", &out.Procedures); err != nil {
		return nil, err
	}
	if err := collectObjects(ctx, conn,
		"SELECT ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = 'FUNCTION' ORDER BY ROUTINE_NAME",
		"function", &out.Functions); err != nil {
		return nil, err
	}
	if err := collectObjects(ctx, conn,
		"SELECT TRIGGER_NAME FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() ORDER BY TRIGGER_NAME",
		"trigger", &out.Triggers); err != nil {
		return nil, err
	}
	return out, nil
}

func pgObjects(ctx context.Context, conn *sql.Conn) (*DatabaseObjects, error) {
	out := &DatabaseObjects{}
	if err := collectObjects(ctx, conn,
		"SELECT table_schema || '.' || table_name FROM information_schema.views WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY 1",
		"view", &out.Views); err != nil {
		return nil, err
	}
	if err := collectObjects(ctx, conn,
		"SELECT routine_schema || '.' || routine_name FROM information_schema.routines WHERE routine_schema NOT IN ('pg_catalog','information_schema') AND routine_type = 'PROCEDURE' ORDER BY 1",
		"procedure", &out.Procedures); err != nil {
		return nil, err
	}
	if err := collectObjects(ctx, conn,
		"SELECT routine_schema || '.' || routine_name FROM information_schema.routines WHERE routine_schema NOT IN ('pg_catalog','information_schema') AND routine_type = 'FUNCTION' ORDER BY 1",
		"function", &out.Functions); err != nil {
		return nil, err
	}
	if err := collectObjects(ctx, conn,
		"SELECT DISTINCT trigger_schema || '.' || trigger_name FROM information_schema.triggers WHERE trigger_schema NOT IN ('pg_catalog','information_schema') ORDER BY 1",
		"trigger", &out.Triggers); err != nil {
		return nil, err
	}
	return out, nil
}

func mssqlObjects(ctx context.Context, conn *sql.Conn) (*DatabaseObjects, error) {
	out := &DatabaseObjects{}
	if err := collectObjects(ctx, conn,
		"SELECT s.name + '.' + v.name FROM sys.views v JOIN sys.schemas s ON s.schema_id = v.schema_id ORDER BY 1",
		"view", &out.Views); err != nil {
		return nil, err
	}
	if err := collectObjects(ctx, conn,
		"SELECT s.name + '.' + p.name FROM sys.procedures p JOIN sys.schemas s ON s.schema_id = p.schema_id ORDER BY 1",
		"procedure", &out.Procedures); err != nil {
		return nil, err
	}
	if err := collectObjects(ctx, conn,
		"SELECT s.name + '.' + o.name FROM sys.objects o JOIN sys.schemas s ON s.schema_id = o.schema_id WHERE o.type IN ('FN','IF','TF') ORDER BY 1",
		"function", &out.Functions); err != nil {
		return nil, err
	}
	if err := collectObjects(ctx, conn,
		"SELECT tr.name FROM sys.triggers tr WHERE tr.is_ms_shipped = 0 ORDER BY tr.name",
		"trigger", &out.Triggers); err != nil {
		return nil, err
	}
	return out, nil
}

func collectObjects(ctx context.Context, conn *sql.Conn, sqlText, typ string, dst *[]ObjectMeta) error {
	rows, err := conn.QueryContext(ctx, sqlText)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		*dst = append(*dst, ObjectMeta{Name: name, Type: typ})
	}
	return rows.Err()
}

// GetObjectSource returns the source / definition of a view / routine / trigger
// for read-only display. SQLite reads sqlite_master.sql; the others use their
// catalog definition function.
func GetObjectSource(ctx context.Context, conn *sql.Conn, driver asset_entity.DatabaseDriver, database, objType, name string) (string, error) {
	switch driver {
	case asset_entity.DriverSQLite:
		master := "sqlite_master"
		if database != "" {
			master = QuoteIdent(database, asset_entity.DriverSQLite) + ".sqlite_master"
		}
		var src sql.NullString
		err := conn.QueryRowContext(ctx, "SELECT sql FROM "+master+" WHERE name = ?", name).Scan(&src) //nolint:gosec // schema identifier-quoted
		if err != nil {
			return "", err
		}
		return src.String, nil
	case asset_entity.DriverMySQL:
		return mysqlObjectSource(ctx, conn, objType, name)
	case asset_entity.DriverPostgreSQL:
		return pgObjectSource(ctx, conn, objType, name)
	case asset_entity.DriverMSSQL:
		var src sql.NullString
		err := conn.QueryRowContext(ctx, "SELECT OBJECT_DEFINITION(OBJECT_ID(@p1))", sql.Named("p1", name)).Scan(&src)
		if err != nil {
			return "", err
		}
		return src.String, nil
	default:
		return "", fmt.Errorf("unsupported driver: %s", driver)
	}
}

func mysqlObjectSource(ctx context.Context, conn *sql.Conn, objType, name string) (string, error) {
	var stmt string
	switch strings.ToLower(objType) {
	case "view":
		stmt = "SHOW CREATE VIEW " + QuoteIdent(name, asset_entity.DriverMySQL)
	case "procedure":
		stmt = "SHOW CREATE PROCEDURE " + QuoteIdent(name, asset_entity.DriverMySQL)
	case "function":
		stmt = "SHOW CREATE FUNCTION " + QuoteIdent(name, asset_entity.DriverMySQL)
	case "trigger":
		stmt = "SHOW CREATE TRIGGER " + QuoteIdent(name, asset_entity.DriverMySQL)
	default:
		return "", fmt.Errorf("unsupported object type: %s", objType)
	}
	rows, err := conn.QueryContext(ctx, stmt) //nolint:gosec // identifier-quoted
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if !rows.Next() {
		return "", fmt.Errorf("object not found: %s", name)
	}
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err
	}
	// SHOW CREATE ... puts the DDL in the column named "Create <Type>".
	row := zipRow(cols, values)
	for _, k := range []string{"Create View", "Create Procedure", "Create Function", "Create Trigger", "SQL Original Statement"} {
		if s := pickString(row, k); s != "" {
			return s, nil
		}
	}
	// Fallback: the last column is conventionally the definition.
	return pickString(row, cols[len(cols)-1]), nil
}

func pgObjectSource(ctx context.Context, conn *sql.Conn, objType, name string) (string, error) {
	schema, obj := splitPGSchemaTable(name)
	var src sql.NullString
	switch strings.ToLower(objType) {
	case "view":
		err := conn.QueryRowContext(ctx, "SELECT pg_get_viewdef(($1 || '.' || $2)::regclass, true)", schema, obj).Scan(&src)
		if err != nil {
			return "", err
		}
	default: // procedure / function
		err := conn.QueryRowContext(ctx,
			"SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace "+
				"WHERE n.nspname = $1 AND p.proname = $2 LIMIT 1", schema, obj).Scan(&src)
		if err != nil {
			return "", err
		}
	}
	return src.String, nil
}
