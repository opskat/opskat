package dbdriver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dumpSource 是测试用的库：每张表有建表语句与行；views 里的名字是视图。它记下收到的调用，
// 证明结构只读定义、行只经备好的 writer 与游标读出。
type dumpSource struct {
	ddl     map[string]string
	columns map[string][]string
	rows    map[string][][]any
	views   []string
	ddlErr  map[string]error
	// streamErr 在读完 streamErrAfter 行之后让游标以它失败。
	streamErr      map[string]error
	streamErrAfter int
	calls          []string
	// written 是 writer 收到的请求。
	written []RowStatementsRequest
	// onRow 在游标每前进一行时调用，测试用它在读取中途取消。
	onRow func()
}

func (s *dumpSource) ListObjects(_ context.Context, database string) (*DatabaseObjects, error) {
	s.calls = append(s.calls, "objects "+database)
	objects := &DatabaseObjects{}
	for _, name := range s.views {
		objects.Views = append(objects.Views, ObjectMeta{Name: name, Type: ObjectView})
	}
	return objects, nil
}

func (s *dumpSource) TableDDL(_ context.Context, database, table string) (string, error) {
	s.calls = append(s.calls, "ddl "+database+"."+table)
	if err := s.ddlErr[table]; err != nil {
		return "", err
	}
	return s.ddl[table], nil
}

func (s *dumpSource) PrepareRowStatements(_ context.Context, database, table string) (RowStatementWriter, error) {
	s.calls = append(s.calls, "prepare "+database+"."+table)
	return dumpWriter{source: s, table: table}, nil
}

func (s *dumpSource) StreamTableRows(_ context.Context, database, table string, query TableQuery) (RowStream, error) {
	s.calls = append(s.calls, "stream "+database+"."+table)
	if len(query.Filters) > 0 || len(query.Sorts) > 0 {
		return nil, errors.New("a dump reads the whole table, unfiltered and unsorted")
	}
	return &dumpStream{source: s, columns: s.columns[table], rows: s.rows[table], err: s.streamErr[table], next: -1}, nil
}

type dumpStream struct {
	source  *dumpSource
	columns []string
	rows    [][]any
	err     error
	next    int
	closed  bool
}

func (r *dumpStream) Columns() []string { return r.columns }
func (r *dumpStream) Next() bool {
	if r.source.onRow != nil {
		r.source.onRow()
	}
	r.next++
	if r.err != nil && r.next >= r.source.streamErrAfter {
		return false
	}
	return r.next < len(r.rows)
}
func (r *dumpStream) Row() []any { return r.rows[r.next] }
func (r *dumpStream) Err() error {
	if r.err != nil && r.next >= r.source.streamErrAfter {
		return r.err
	}
	return nil
}
func (r *dumpStream) Close() error {
	r.closed = true
	r.source.calls = append(r.source.calls, "close stream")
	return nil
}

// dumpWriter 写出一眼能认出的 INSERT：列名与各值的 %v（值是经 JSON 往返后的样子）。
type dumpWriter struct {
	source *dumpSource
	table  string
}

func (w dumpWriter) RowStatements(req RowStatementsRequest) ([]string, error) {
	w.source.written = append(w.source.written, req)
	statements := make([]string, 0, len(req.Rows))
	for _, row := range req.Rows {
		values := make([]string, len(req.Columns))
		for i, column := range req.Columns {
			values[i] = fmt.Sprintf("%v", row[column])
		}
		statements = append(statements, fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", w.table, strings.Join(req.Columns, ", "), strings.Join(values, ", ")))
	}
	return statements, nil
}

// dumpDialect 是测试用的方言：每一处钩子都给出带对象名的语句，证明编排把它们写在约定的位置。
type dumpDialect struct {
	file       DumpFileScript
	scopes     []DumpScope
	objects    []DumpObject
	dataColumn map[string][]string
	// plain 为真时对象只给出存在则删除与定义（像什么钩子都没实现的方言）。
	plain bool
}

func (d *dumpDialect) DumpFile(_ context.Context, scope DumpScope) (*DumpFileScript, error) {
	d.scopes = append(d.scopes, scope)
	file := d.file
	return &file, nil
}

func (d *dumpDialect) DumpObject(_ context.Context, object DumpObject) (*DumpObjectScript, error) {
	d.objects = append(d.objects, object)
	script := &DumpObjectScript{
		Drop:       []string{fmt.Sprintf("DROP %s %s;", strings.ToUpper(object.Kind), object.Name)},
		Definition: []string{object.Definition + ";"},
	}
	if d.plain || object.Kind != DumpTable {
		return script, nil
	}
	script.DataColumns = d.dataColumn[object.Name]
	script.DataPrologue = []string{"BEGIN DATA " + object.Name + ";"}
	script.DataEpilogue = []string{"END DATA " + object.Name + ";"}
	script.ForeignKeys = []string{"ADD FK " + object.Name + ";"}
	script.PostData = []string{"SYNC " + object.Name + ";"}
	return script, nil
}

func dumpHeader() DumpHeader {
	return DumpHeader{Version: "1.16.0", Asset: "demo-db", Time: time.Date(2026, 11, 16, 9, 30, 5, 0, time.FixedZone("CST", 8*3600))}
}

func newDumpSource() *dumpSource {
	return &dumpSource{
		ddl: map[string]string{
			"users":  "CREATE TABLE users (id int, name text)",
			"orders": "CREATE TABLE orders (id int, user_id int)",
			"v_big":  "CREATE VIEW v_big AS SELECT * FROM orders",
		},
		columns: map[string][]string{"users": {"id", "name"}, "orders": {"id", "user_id"}, "v_big": {"id", "user_id"}},
		rows: map[string][][]any{
			"users":  {{int64(1), "alice"}, {int64(2), nil}},
			"orders": {{int64(10), int64(1)}},
			"v_big":  {{int64(10), int64(1)}},
		},
		views: []string{"v_big"},
	}
}

// 一份结构和数据的转储：注释头（版本、资产、库、内容、时间，没有主机端口账号）、驱动给的文件开头与
// 类型、每张表依次是注释分隔行、存在则删除、定义、数据（前后有驱动的开关），之后是全部外键、视图
// （只有定义）、数据之后的收尾、驱动给的文件结尾。视图排在表之后，不读它的数据。
func TestDumpWritesTheFileInItsSections(t *testing.T) {
	source := newDumpSource()
	dialect := &dumpDialect{file: DumpFileScript{
		Opening: []string{"SET FK OFF;"}, Types: []string{"CREATE TYPE mood;"}, Closing: []string{"SET FK ON;"},
	}}
	var out bytes.Buffer

	result, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"v_big", "users", "orders"}, WithData: true,
		Header: dumpHeader(), Writer: &out,
	}, source, dialect)
	require.NoError(t, err)

	assert.Equal(t, `-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: demo-db
-- Database: shop
-- Content: structure and data
-- Date: 2026-11-16 09:30:05 +08:00
--
-- File encoding: UTF-8

SET FK OFF;

CREATE TYPE mood;

-- ----------------------------
-- Table structure for users
-- ----------------------------
DROP TABLE users;
CREATE TABLE users (id int, name text);

-- ----------------------------
-- Records of users
-- ----------------------------
BEGIN DATA users;
INSERT INTO users (id, name) VALUES (1, alice);
INSERT INTO users (id, name) VALUES (2, <nil>);
END DATA users;

-- ----------------------------
-- Table structure for orders
-- ----------------------------
DROP TABLE orders;
CREATE TABLE orders (id int, user_id int);

-- ----------------------------
-- Records of orders
-- ----------------------------
BEGIN DATA orders;
INSERT INTO orders (id, user_id) VALUES (10, 1);
END DATA orders;

-- ----------------------------
-- Foreign keys
-- ----------------------------
ADD FK users;
ADD FK orders;

-- ----------------------------
-- View structure for v_big
-- ----------------------------
DROP VIEW v_big;
CREATE VIEW v_big AS SELECT * FROM orders;

-- ----------------------------
-- Sequence and auto-increment values
-- ----------------------------
SYNC users;
SYNC orders;

SET FK ON;
`, out.String())
	assert.Equal(t, DumpResult{Objects: 3, Rows: 3, Bytes: int64(out.Len())}, *result)
	assert.Equal(t, []DumpScope{{Database: "shop", Tables: []string{"v_big", "users", "orders"}, WithData: true}}, dialect.scopes)
	assert.Equal(t, []DumpObject{
		{Database: "shop", Kind: DumpTable, Name: "users", Definition: source.ddl["users"], WithData: true},
		{Database: "shop", Kind: DumpTable, Name: "orders", Definition: source.ddl["orders"], WithData: true},
		{Database: "shop", Kind: ObjectView, Name: "v_big", Definition: source.ddl["v_big"]},
	}, dialect.objects)
	assert.NotContains(t, source.calls, "stream shop.v_big")
	assert.NotContains(t, source.calls, "prepare shop.v_big")
}

// 仅结构：不读任何行，不备 writer，也没有记录段；方言什么钩子都没实现时，只有存在则删除与定义。
func TestDumpStructureOnlyReadsNoRows(t *testing.T) {
	source := newDumpSource()
	var out bytes.Buffer

	result, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Schema: "public", Tables: []string{"users"}, Header: dumpHeader(), Writer: &out,
	}, source, &dumpDialect{plain: true})
	require.NoError(t, err)

	assert.Equal(t, `-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: demo-db
-- Database: shop
-- Schema: public
-- Content: structure only
-- Date: 2026-11-16 09:30:05 +08:00
--
-- File encoding: UTF-8

-- ----------------------------
-- Table structure for users
-- ----------------------------
DROP TABLE users;
CREATE TABLE users (id int, name text);
`, out.String())
	assert.Equal(t, DumpResult{Objects: 1, Rows: 0, Bytes: int64(out.Len())}, *result)
	assert.Equal(t, []string{"objects shop", "ddl shop.users"}, source.calls)
}

// 行的写法与「复制为 INSERT」一样：行先变成前端拿到再带回的样子（经 JSON：时间成了字符串、
// 整数成了数字），INSERT 只写方言给出的列（生成列不在其中）。
func TestDumpWritesRowsLikeCopyAsInsertWithTheDialectsColumns(t *testing.T) {
	source := newDumpSource()
	source.columns["users"] = []string{"id", "name", "upper_name", "created"}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	source.rows["users"] = [][]any{{int64(1), "alice", "ALICE", created}}
	dialect := &dumpDialect{dataColumn: map[string][]string{"users": {"id", "name", "created"}}}

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
	}, source, dialect)
	require.NoError(t, err)

	require.Len(t, source.written, 1)
	assert.Equal(t, RowStatementsRequest{
		Kind: RowInsert, Columns: []string{"id", "name", "created"},
		Rows: []map[string]any{{"id": float64(1), "name": "alice", "upper_name": "ALICE", "created": "2026-01-02T03:04:05Z"}},
	}, source.written[0])
	// writer 在游标打开之前备好（远端 SQLite 只有一条连接，游标占着它时不能再查列类型）。
	assert.Equal(t, []string{"objects shop", "ddl shop.users", "prepare shop.users", "stream shop.users", "close stream"}, source.calls)
}

// 方言有批分隔符（SQL Server 的 GO）时：每条结构语句之后、每一块数据之后都写一行分隔符。
func TestDumpWritesTheBatchTerminatorAfterStatementsAndDataChunks(t *testing.T) {
	source := newDumpSource()
	rows := make([][]any, dumpChunkRows+1)
	for i := range rows {
		rows[i] = []any{int64(i), "u"}
	}
	source.rows["users"] = rows
	var out bytes.Buffer

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users"}, WithData: true, Header: dumpHeader(), Writer: &out,
	}, source, &dumpDialect{plain: true, file: DumpFileScript{Opening: []string{"SET A;"}, BatchTerminator: "GO"}})
	require.NoError(t, err)

	text := out.String()
	assert.Contains(t, text, "SET A;\nGO\n")
	assert.Contains(t, text, "DROP TABLE users;\nGO\nCREATE TABLE users (id int, name text);\nGO\n")
	inserts := strings.Count(text, "INSERT INTO users")
	assert.Equal(t, dumpChunkRows+1, inserts)
	// 两块数据：第一块之后、第二块（一行）之后各一个 GO；结构里还有三个（开头、删除、定义）。
	assert.Equal(t, 5, strings.Count(text, "\nGO\n"))
	assert.Contains(t, text, fmt.Sprintf("VALUES (%d, u);\nGO\n", dumpChunkRows-1))
}

// 进度：每个对象开始时一次，写完时一次（带上它写了几行），行多时中途按块报。
func TestDumpReportsProgressPerObject(t *testing.T) {
	source := newDumpSource()
	var progress []DumpProgress

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users", "v_big"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
		Progress: func(p DumpProgress) { progress = append(progress, p) },
	}, source, &dumpDialect{})
	require.NoError(t, err)

	var finished []DumpedObject
	for _, p := range progress {
		assert.Equal(t, 2, p.Objects)
		if p.Finished != nil {
			finished = append(finished, *p.Finished)
		}
	}
	assert.Equal(t, []DumpedObject{
		{Name: "users", Kind: DumpTable, Data: true, Rows: 2},
		{Name: "v_big", Kind: ObjectView},
	}, finished)
	assert.Equal(t, DumpProgress{Objects: 2, Object: 1, Name: "users"}, progress[0])
	last := progress[len(progress)-1]
	assert.Equal(t, 2, last.Object)
	assert.EqualValues(t, 2, last.Rows)
}

// 任何一个对象读取失败都立即停下：错误说出停在哪个对象，后面的对象不再读。
func TestDumpStopsAtTheObjectThatFailed(t *testing.T) {
	t.Run("its definition", func(t *testing.T) {
		source := newDumpSource()
		source.ddlErr = map[string]error{"users": errors.New("permission denied")}

		_, err := Dump(context.Background(), DumpRequest{
			Database: "shop", Tables: []string{"users", "orders"}, Header: dumpHeader(), Writer: &bytes.Buffer{},
		}, source, &dumpDialect{})
		var objectErr *DumpObjectError
		require.ErrorAs(t, err, &objectErr)
		assert.Equal(t, "users", objectErr.Object)
		assert.ErrorContains(t, err, "permission denied")
		assert.NotContains(t, source.calls, "ddl shop.orders")
	})
	t.Run("an empty definition", func(t *testing.T) {
		source := newDumpSource()
		delete(source.ddl, "users")

		_, err := Dump(context.Background(), DumpRequest{
			Database: "shop", Tables: []string{"users"}, Header: dumpHeader(), Writer: &bytes.Buffer{},
		}, source, &dumpDialect{})
		var objectErr *DumpObjectError
		require.ErrorAs(t, err, &objectErr)
		assert.Equal(t, "users", objectErr.Object)
	})
	t.Run("reading its rows", func(t *testing.T) {
		source := newDumpSource()
		source.streamErr = map[string]error{"users": errors.New("invalid connection")}
		source.streamErrAfter = 1

		_, err := Dump(context.Background(), DumpRequest{
			Database: "shop", Tables: []string{"users", "orders"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
		}, source, &dumpDialect{})
		var objectErr *DumpObjectError
		require.ErrorAs(t, err, &objectErr)
		assert.Equal(t, "users", objectErr.Object)
		assert.ErrorContains(t, err, "invalid connection")
		assert.Contains(t, source.calls, "close stream")
		assert.NotContains(t, source.calls, "ddl shop.orders")
	})
}

// 被取消（ctx）时在读行的中途停下，错误是取消本身。
func TestDumpStopsWhenCanceled(t *testing.T) {
	source := newDumpSource()
	rows := make([][]any, 5*dumpChunkRows)
	for i := range rows {
		rows[i] = []any{int64(i), "u"}
	}
	source.rows["users"] = rows
	ctx, cancel := context.WithCancel(context.Background())
	read := 0
	source.onRow = func() {
		read++
		if read == dumpChunkRows+1 {
			cancel()
		}
	}

	_, err := Dump(ctx, DumpRequest{
		Database: "shop", Tables: []string{"users"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
	}, source, &dumpDialect{})
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, read, len(rows))
}

// 注释头与分隔行里的名字不能带换行：带了的话后面的内容就跑出注释、成了会被执行的语句。
func TestDumpKeepsNamesInsideTheirComments(t *testing.T) {
	source := newDumpSource()
	source.ddl["a\nDROP TABLE x;"] = "CREATE TABLE t (id int)"
	header := dumpHeader()
	header.Asset = "prod\r\nDROP DATABASE prod;"
	var out bytes.Buffer

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"a\nDROP TABLE x;"}, Header: header, Writer: &out,
	}, source, &dumpDialect{plain: true})
	require.NoError(t, err)

	for _, line := range strings.Split(out.String(), "\n") {
		assert.False(t, strings.HasPrefix(line, "DROP DATABASE"), "line %q escaped its comment", line)
	}
	assert.Contains(t, out.String(), "-- Asset: prod  DROP DATABASE prod;\n")
	assert.Contains(t, out.String(), "-- Table structure for a DROP TABLE x;\n")
}

// 没有要转储的表时不写任何东西就拒绝。
func TestDumpNeedsTables(t *testing.T) {
	source := newDumpSource()
	var out bytes.Buffer
	_, err := Dump(context.Background(), DumpRequest{Database: "shop", Header: dumpHeader(), Writer: &out}, source, &dumpDialect{})
	require.Error(t, err)
	assert.Empty(t, out.String())
	assert.Empty(t, source.calls)
}

// dumpSession 把 Dump 交给编排：DumpToFile 只经 Session.Dump 写文件。
type dumpSession struct {
	Session
	source  *dumpSource
	dialect *dumpDialect
}

func (s dumpSession) Dump(ctx context.Context, req DumpRequest) (*DumpResult, error) {
	return Dump(ctx, req, s.source, s.dialect)
}

// 写文件：先写到目标旁边的临时文件，成功才换成目标；失败时目标保持原样（原来没有的仍然没有，
// 原来有的仍是原来的内容），临时文件也不留下。
func TestDumpToFileReplacesTheTargetOnlyOnSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shop.sql")
	failing := newDumpSource()
	failing.ddlErr = map[string]error{"orders": errors.New("lost connection")}
	req := DumpRequest{Database: "shop", Tables: []string{"users", "orders"}, Header: dumpHeader()}

	_, err := DumpToFile(context.Background(), dumpSession{source: failing, dialect: &dumpDialect{}}, req, target)
	require.Error(t, err)
	assert.NoFileExists(t, target)
	assertOnlyFiles(t, dir)

	require.NoError(t, os.WriteFile(target, []byte("previous dump"), 0o600))
	_, err = DumpToFile(context.Background(), dumpSession{source: failing, dialect: &dumpDialect{}}, req, target)
	require.Error(t, err)
	content, readErr := os.ReadFile(filepath.Clean(target))
	require.NoError(t, readErr)
	assert.Equal(t, "previous dump", string(content))
	assertOnlyFiles(t, dir, "shop.sql")

	result, err := DumpToFile(context.Background(), dumpSession{source: newDumpSource(), dialect: &dumpDialect{}}, req, target)
	require.NoError(t, err)
	content, readErr = os.ReadFile(filepath.Clean(target))
	require.NoError(t, readErr)
	assert.True(t, strings.HasPrefix(string(content), "-- OpsKat SQL Dump\n"))
	assert.EqualValues(t, len(content), result.Bytes)
	assertOnlyFiles(t, dir, "shop.sql")
}

func assertOnlyFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	assert.ElementsMatch(t, append([]string{}, names...), got)
}
