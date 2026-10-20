package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// Catalog 是方言里「数据库自己怎么回答树的浏览」的那一半：各驱动在一条 *sql.Conn 上用自己
// 的目录视图查询。Session 为每次操作取一条连接交给它——MySQL 的 DATABASE()、SQLite 的 pragma
// 都要求前后的语句落在同一条连接上。
//
// 实现 Dialect 的类型同时实现 Catalog 才提供浏览；没实现的方言，Session 的浏览方法一律
// 返回 dbdriver.ErrUnsupported。
type Catalog interface {
	// Descriptor 是驱动的描述：Session 据此在查询之前拒绝没有声明的对象类型。
	Descriptor() dbdriver.Descriptor
	ListDatabases(ctx context.Context, conn *sql.Conn) ([]string, error)
	ListTables(ctx context.Context, conn *sql.Conn, database string) ([]string, error)
	TableMetadata(ctx context.Context, conn *sql.Conn, database, table string) (*dbdriver.TableMetadata, error)
	ListObjects(ctx context.Context, conn *sql.Conn, database string) (*dbdriver.DatabaseObjects, error)
	// ObjectSource 被调用时 objType 已经通过 Descriptor 的检查。
	ObjectSource(ctx context.Context, conn *sql.Conn, database, objType, name, table string) (string, error)
}

func (s *Session) catalog() (Catalog, error) {
	catalog, ok := s.dialect.(Catalog)
	if !ok {
		return nil, fmt.Errorf("%w: this driver does not provide object browsing", dbdriver.ErrUnsupported)
	}
	return catalog, nil
}

// withConn 在会话的一条独立连接上对方言的目录运行 fn。
func withConn[T any](ctx context.Context, s *Session, fn func(Catalog, *sql.Conn) (T, error)) (T, error) {
	catalog, err := s.catalog()
	if err != nil {
		var zero T
		return zero, err
	}
	return onConn(ctx, s, func(conn *sql.Conn) (T, error) { return fn(catalog, conn) })
}

// onConn 在会话的一条独立连接上运行 fn，之后归还；归还失败与 fn 的错误一并返回。
func onConn[T any](ctx context.Context, s *Session, fn func(*sql.Conn) (T, error)) (result T, err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("打开数据库会话失败: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("关闭数据库会话失败: %w", closeErr))
		}
	}()
	return fn(conn)
}

func (s *Session) ListDatabases(ctx context.Context) ([]string, error) {
	return withConn(ctx, s, func(c Catalog, conn *sql.Conn) ([]string, error) {
		return c.ListDatabases(ctx, conn)
	})
}

func (s *Session) ListTables(ctx context.Context, database string) ([]string, error) {
	return withConn(ctx, s, func(c Catalog, conn *sql.Conn) ([]string, error) {
		return c.ListTables(ctx, conn, database)
	})
}

func (s *Session) TableMetadata(ctx context.Context, database, table string) (*dbdriver.TableMetadata, error) {
	return withConn(ctx, s, func(c Catalog, conn *sql.Conn) (*dbdriver.TableMetadata, error) {
		return c.TableMetadata(ctx, conn, database, table)
	})
}

func (s *Session) ListObjects(ctx context.Context, database string) (*dbdriver.DatabaseObjects, error) {
	return withConn(ctx, s, func(c Catalog, conn *sql.Conn) (*dbdriver.DatabaseObjects, error) {
		return c.ListObjects(ctx, conn, database)
	})
}

func (s *Session) ObjectSource(ctx context.Context, database, objType, name, table string) (string, error) {
	catalog, err := s.catalog()
	if err != nil {
		return "", err
	}
	if err := catalog.Descriptor().CheckObjectType(objType); err != nil {
		return "", err
	}
	return withConn(ctx, s, func(c Catalog, conn *sql.Conn) (string, error) {
		return c.ObjectSource(ctx, conn, database, objType, name, table)
	})
}

// --- 各驱动的目录查询共用的助手 ---

// Wrap 给目录查询的错误标上是哪一步（columns、primary keys……），保留原错误。
func Wrap(what string, err error) error {
	return fmt.Errorf("%s: %w", what, err)
}

// QueryNames 执行一条查询，取每行第一列作名字；NULL 与空串跳过。
func QueryNames(ctx context.Context, conn *sql.Conn, query string, args ...any) ([]string, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		if name := PickString(ZipRow(cols, values), cols[0]); name != "" {
			names = append(names, name)
		}
	}
	return names, rows.Err()
}

// CollectObjects 执行一条返回名字的查询，把结果以 typ 类型追加到 dst。
func CollectObjects(ctx context.Context, conn *sql.Conn, query, typ string, dst *[]dbdriver.ObjectMeta) error {
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		*dst = append(*dst, dbdriver.ObjectMeta{Name: name, Type: typ})
	}
	return rows.Err()
}

// QueryForeignKeys 执行一条返回四列（约束名、列、被引用表、被引用列）的外键查询。
func QueryForeignKeys(ctx context.Context, conn *sql.Conn, query string, args ...any) ([]dbdriver.ForeignKeyMeta, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]dbdriver.ForeignKeyMeta, 0)
	for rows.Next() {
		var name, col, refTable, refCol sql.NullString
		if err := rows.Scan(&name, &col, &refTable, &refCol); err != nil {
			return nil, err
		}
		out = append(out, dbdriver.ForeignKeyMeta{
			Name:             name.String,
			Column:           col.String,
			ReferencedTable:  refTable.String,
			ReferencedColumn: refCol.String,
		})
	}
	return out, rows.Err()
}

// IndexBuilder 把按（索引名、列位置）排好序的行归并成索引，保持首次出现的顺序。
type IndexBuilder struct {
	order  []string
	byName map[string]*dbdriver.IndexMeta
}

// Add 把索引 name 的下一列并进去；unique 取该索引第一次出现时的值。
func (b *IndexBuilder) Add(name string, unique bool, column string) {
	if b.byName == nil {
		b.byName = map[string]*dbdriver.IndexMeta{}
	}
	m, ok := b.byName[name]
	if !ok {
		m = &dbdriver.IndexMeta{Name: name, Unique: unique}
		b.byName[name] = m
		b.order = append(b.order, name)
	}
	m.Columns = append(m.Columns, column)
}

// Indexes 返回归并后的索引；没有索引时是空切片（序列化成 []）。
func (b *IndexBuilder) Indexes() []dbdriver.IndexMeta {
	out := make([]dbdriver.IndexMeta, 0, len(b.order))
	for _, name := range b.order {
		out = append(out, *b.byName[name])
	}
	return out
}

// QueryPrimaryKeys 执行一条主键查询。nameColumn 是方言里主键列名所在的结果列（也认其他
// 方言的叫法）。
func QueryPrimaryKeys(ctx context.Context, conn *sql.Conn, nameColumn, query string, args ...any) ([]string, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
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
		row := ZipRow(cols, values)
		if name := PickString(row, nameColumn, "Column_name", "column_name", "COLUMN_NAME"); name != "" {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

// AssembleMetadata 把列、主键、索引、外键拼成表元数据，并在列上标出主键。
func AssembleMetadata(columns []dbdriver.ColumnMeta, primaryKeys []string, indexes []dbdriver.IndexMeta, foreignKeys []dbdriver.ForeignKeyMeta) *dbdriver.TableMetadata {
	pk := make(map[string]struct{}, len(primaryKeys))
	for _, name := range primaryKeys {
		pk[name] = struct{}{}
	}
	for i := range columns {
		_, columns[i].PrimaryKey = pk[columns[i].Name]
	}
	return &dbdriver.TableMetadata{Columns: columns, Indexes: indexes, ForeignKeys: foreignKeys}
}

// SplitSchemaTable 把列表给出的表名拆成（schema, table），只在第一个点处拆：
// "events" → (defaultSchema, "events")；"reporting.events" → ("reporting", "events")；
// "public.a.b" → ("public", "a.b")（名字带点的表作为一个整体，schema 名本身带点的不在此列）。
func SplitSchemaTable(table, defaultSchema string) (string, string) {
	if schema, name, qualified := strings.Cut(table, "."); qualified {
		return schema, name
	}
	return defaultSchema, table
}

// QueryRecords 执行一条查询，把每一行按结果列名取成 map（[]byte 转成字符串，见 ZipRow）。
func QueryRecords(ctx context.Context, conn *sql.Conn, query string, args ...any) ([]map[string]any, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var records []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		records = append(records, ZipRow(cols, values))
	}
	return records, rows.Err()
}

// ZipRow 把一行的值按列名组成 map，[]byte 转成字符串。
func ZipRow(cols []string, values []any) map[string]any {
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

// PickString 取 row 里第一个存在且非 NULL 的键的值，转成字符串；都没有返回空串。
func PickString(row map[string]any, keys ...string) string {
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
