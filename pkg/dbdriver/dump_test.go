package dbdriver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
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
	// tables 是 ListTables 给出的表与视图；functions / procedures / triggers 是 ListObjects 给出的其余对象，
	// sources 是它们的定义（键是「种类 名字」）。
	tables     []string
	functions  []string
	procedures []string
	triggers   []ObjectMeta
	sources    map[string]string
	sourceErr  map[string]error
	ddlErr     map[string]error
	// streamErr 在读完 streamErrAfter 行之后让游标以它失败。
	streamErr      map[string]error
	streamErrAfter int
	calls          []string
	// written 是 writer 收到的请求。
	written []RowStatementsRequest
	// onRow 在游标每前进一行时调用，测试用它在读取中途取消。
	onRow func()
	// children 是各表的子表（分区、继承的子表）：读一张表默认连子表的行一起读出，TableQuery.OwnRows 时只读它自己的。
	children map[string][]string
}

// tableRows 是读一张表得到的行：它自己的，不只读自己的行时接着是子表（一路下去）的。
func (s *dumpSource) tableRows(table string, ownRows bool) [][]any {
	rows := s.rows[table]
	if ownRows {
		return rows
	}
	for _, child := range s.children[table] {
		rows = append(slices.Clone(rows), s.tableRows(child, false)...)
	}
	return rows
}

func (s *dumpSource) ListObjects(_ context.Context, database string) (*DatabaseObjects, error) {
	s.calls = append(s.calls, "objects "+database)
	objects := &DatabaseObjects{}
	for _, name := range s.views {
		objects.Views = append(objects.Views, ObjectMeta{Name: name, Type: ObjectView})
	}
	for _, name := range s.functions {
		objects.Functions = append(objects.Functions, ObjectMeta{Name: name, Type: ObjectFunction})
	}
	for _, name := range s.procedures {
		objects.Procedures = append(objects.Procedures, ObjectMeta{Name: name, Type: ObjectProcedure})
	}
	objects.Triggers = s.triggers
	return objects, nil
}

func (s *dumpSource) ListTables(_ context.Context, database string) ([]string, error) {
	s.calls = append(s.calls, "tables "+database)
	return s.tables, nil
}

func (s *dumpSource) ObjectSource(_ context.Context, database, objType, name, table string) (string, error) {
	s.calls = append(s.calls, "source "+database+" "+objType+" "+name+" "+table)
	if err := s.sourceErr[name]; err != nil {
		return "", err
	}
	return s.sources[objType+" "+name], nil
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
	return &dumpStream{source: s, columns: s.columns[table], rows: s.tableRows(table, query.OwnRows), err: s.streamErr[table], next: -1}, nil
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
	// insertModifier 是各表的 InsertModifier。
	insertModifier map[string]string
	// dependencies 是各对象带种类的依赖（方言给出的 Dependencies），键是「种类 名字」。
	dependencies map[string][]DumpDependency
	// partitions 是各表的分区（方言给出的 Partitions）。
	partitions map[string][]string
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
		Drop:         []string{fmt.Sprintf("DROP %s IF EXISTS %s;", strings.ToUpper(object.Kind), object.Name)},
		Definition:   []string{object.Definition + ";"},
		Dependencies: d.dependencies[object.Kind+" "+object.Name],
		Partitions:   d.partitions[object.Name],
	}
	if d.plain || object.Kind != DumpTable {
		return script, nil
	}
	// 索引由方言按对象带来的选项写或不写。
	if object.Options.Indexes {
		script.Definition = append(script.Definition, "CREATE INDEX ix_"+object.Name+";")
	}
	script.DropForeignKeys = []string{"DROP FK " + object.Name + ";"}
	script.DataColumns = d.dataColumn[object.Name]
	script.InsertModifier = d.insertModifier[object.Name]
	script.DataPrologue = []string{"BEGIN DATA " + object.Name + ";"}
	script.DataEpilogue = []string{"END DATA " + object.Name + ";"}
	script.ForeignKeys = []string{"ADD FK " + object.Name + ";"}
	script.PostData = []string{"SYNC " + object.Name + ";"}
	return script, nil
}

// allOptions 是全部勾上的导出选项。
var allOptions = DumpOptions{
	Indexes: true, ForeignKeys: true, Views: true, Routines: true, Triggers: true, Drops: true, DisableForeignKeyChecks: true,
}

// fullFile 是方言给出整份文件各部分的样子。
var fullFile = DumpFileScript{
	Opening: []string{"SET NAMES utf8;"}, DisableForeignKeyChecks: []string{"SET FK OFF;"},
	Types: []string{"CREATE TYPE mood;"}, DropTypes: []string{"DROP TYPE IF EXISTS mood;"},
	EnableForeignKeyChecks: []string{"SET FK ON;"}, Closing: []string{"RESET;"},
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

// 一份结构和数据、选项全勾的转储：注释头（版本、资产、库、内容、所选的选项、时间，没有主机端口账号）、
// 驱动给的文件开头与关闭外键检查、删除段（先删外键，再按倒序删视图、表，最后删类型）、类型、每张表依次是
// 注释分隔行、定义（含方言按选项写的索引）、数据（前后有驱动的开关），之后是全部外键、视图（只有定义）、
// 数据之后的收尾、恢复外键检查与驱动给的文件结尾。视图排在表之后，不读它的数据。
func TestDumpWritesTheFileInItsSections(t *testing.T) {
	source := newDumpSource()
	dialect := &dumpDialect{file: fullFile}
	var out bytes.Buffer

	result, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"v_big", "users", "orders"}, WithData: true, Options: allOptions,
		Header: dumpHeader(), Writer: &out,
	}, source, dialect)
	require.NoError(t, err)

	assert.Equal(t, `-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: demo-db
-- Database: shop
-- Content: structure and data
-- Options: indexes, foreign keys, drop statements, disable foreign key checks
-- Date: 2026-11-16 09:30:05 +08:00
--
-- File encoding: UTF-8

SET NAMES utf8;
SET FK OFF;

-- ----------------------------
-- Drop existing objects
-- ----------------------------
DROP FK users;
DROP FK orders;
DROP VIEW IF EXISTS v_big;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS users;
DROP TYPE IF EXISTS mood;

CREATE TYPE mood;

-- ----------------------------
-- Table structure for users
-- ----------------------------
CREATE TABLE users (id int, name text);
CREATE INDEX ix_users;

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
CREATE TABLE orders (id int, user_id int);
CREATE INDEX ix_orders;

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
CREATE VIEW v_big AS SELECT * FROM orders;

-- ----------------------------
-- Sequence and auto-increment values
-- ----------------------------
SYNC users;
SYNC orders;

SET FK ON;
RESET;
`, out.String())
	assert.Equal(t, DumpResult{Objects: 3, Rows: 3, Bytes: int64(out.Len())}, *result)
	assert.Equal(t, []DumpScope{{Database: "shop", Tables: []string{"v_big", "users", "orders"}, WithData: true, Options: allOptions}}, dialect.scopes)
	assert.Equal(t, []DumpObject{
		{Database: "shop", Kind: DumpTable, Name: "users", Definition: source.ddl["users"], WithData: true, Options: allOptions},
		{Database: "shop", Kind: DumpTable, Name: "orders", Definition: source.ddl["orders"], WithData: true, Options: allOptions},
		{Database: "shop", Kind: ObjectView, Name: "v_big", Definition: source.ddl["v_big"], Options: allOptions},
	}, dialect.objects)
	assert.NotContains(t, source.calls, "stream shop.v_big")
	assert.NotContains(t, source.calls, "prepare shop.v_big")
}

// 仅结构、什么选项都不勾：不读任何行，不备 writer，也没有记录段；没有删除语句，只有定义。
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
-- Options: none
-- Date: 2026-11-16 09:30:05 +08:00
--
-- File encoding: UTF-8

-- ----------------------------
-- Table structure for users
-- ----------------------------
CREATE TABLE users (id int, name text);
`, out.String())
	assert.Equal(t, DumpResult{Objects: 1, Rows: 0, Bytes: int64(out.Len())}, *result)
	assert.Equal(t, []string{"objects shop", "ddl shop.users"}, source.calls)
}

// 行的写法与「复制为 INSERT」一样：行先变成前端拿到再带回的样子（经 JSON：时间成了字符串；整数
// 保持整数，见 TestDumpKeepsBigIntegersExact），INSERT 只写方言给出的列（生成列不在其中）。
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
		Kind: RowInsert, Columns: []string{"id", "name", "created"}, OmitDatabase: true,
		Rows: []map[string]any{{"id": int64(1), "name": "alice", "upper_name": "ALICE", "created": "2026-01-02T03:04:05Z"}},
	}, source.written[0])
	// writer 在游标打开之前备好（远端 SQLite 只有一条连接，游标占着它时不能再查列类型）。
	assert.Equal(t, []string{"objects shop", "ddl shop.users", "prepare shop.users", "stream shop.users", "close stream"}, source.calls)
}

// 二进制的值（不是合法 UTF-8 的字符串、[]byte）以字节交给 writer，不经 JSON 往返：JSON 会把非法字节换成
// U+FFFD、把 []byte 变成 base64，转储出的数据就不是原来的了。
func TestDumpKeepsBinaryValuesAsBytes(t *testing.T) {
	source := newDumpSource()
	source.ddl["blobs"] = "CREATE TABLE blobs (id int)"
	source.columns["blobs"] = []string{"id", "raw", "text", "bytes"}
	source.rows["blobs"] = [][]any{{int64(1), "\x00\xff\x10", "plain", []byte{0xfe, 0x01}}}

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"blobs"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
	}, source, &dumpDialect{})
	require.NoError(t, err)

	require.Len(t, source.written, 1)
	assert.Equal(t, []map[string]any{{"id": int64(1), "raw": []byte{0x00, 0xff, 0x10}, "text": "plain", "bytes": []byte{0xfe, 0x01}}}, source.written[0].Rows)
}

// 超过 2^53 的整数照原值交给 writer：经 JSON 往返成 float64 会丢精度，转储出的数据就不是原来的了。
func TestDumpKeepsBigIntegersExact(t *testing.T) {
	source := newDumpSource()
	source.rows["users"] = [][]any{{int64(9007199254740993), "big"}}

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
	}, source, &dumpDialect{})
	require.NoError(t, err)

	require.Len(t, source.written, 1)
	assert.Equal(t, []map[string]any{{"id": int64(9007199254740993), "name": "big"}}, source.written[0].Rows)
}

// 浮点列里的 NaN 与正负无穷（PostgreSQL 的 float 能存）不让转储停下：JSON 写不出它们，它们以文字
// 'NaN' / 'Infinity' / '-Infinity' 交给 writer，写成浮点列认得的字面量。
func TestDumpWritesNonFiniteFloatsAsText(t *testing.T) {
	source := newDumpSource()
	source.rows["users"] = [][]any{{math.NaN(), "nan"}, {math.Inf(1), "inf"}, {float32(math.Inf(-1)), "-inf"}, {1.5, "finite"}}

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
	}, source, &dumpDialect{})
	require.NoError(t, err)

	require.Len(t, source.written, 1)
	assert.Equal(t, []map[string]any{
		{"id": "NaN", "name": "nan"}, {"id": "Infinity", "name": "inf"}, {"id": "-Infinity", "name": "-inf"}, {"id": 1.5, "name": "finite"},
	}, source.written[0].Rows)
}

// 方言给出的 InsertModifier（PostgreSQL 的 OVERRIDING SYSTEM VALUE）随行的请求交给 writer。
func TestDumpPassesTheInsertModifierToTheWriter(t *testing.T) {
	source := newDumpSource()
	dialect := &dumpDialect{insertModifier: map[string]string{"users": "OVERRIDING SYSTEM VALUE"}}

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users", "orders"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
	}, source, dialect)
	require.NoError(t, err)

	require.Len(t, source.written, 2)
	assert.Equal(t, "OVERRIDING SYSTEM VALUE", source.written[0].InsertModifier)
	assert.Empty(t, source.written[1].InsertModifier)
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
		Database: "shop", Tables: []string{"users"}, WithData: true, Options: DumpOptions{Drops: true}, Header: dumpHeader(), Writer: &out,
	}, source, &dumpDialect{plain: true, file: DumpFileScript{Opening: []string{"SET A;"}, BatchTerminator: "GO"}})
	require.NoError(t, err)

	text := out.String()
	assert.Contains(t, text, "SET A;\nGO\n")
	assert.Contains(t, text, "DROP TABLE IF EXISTS users;\nGO\n")
	assert.Contains(t, text, "CREATE TABLE users (id int, name text);\nGO\n")
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
		// 定义在写任何内容之前都已读出（删除段要用到），后面的表的行不再读。
		assert.NotContains(t, source.calls, "stream shop.orders")
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

// newWholeDumpSource 是一个整库：两张表、一个视图、一个函数、一个过程与一个表上的触发器。
func newWholeDumpSource() *dumpSource {
	source := newDumpSource()
	source.tables = []string{"orders", "users", "v_big"}
	source.functions = []string{"f_total"}
	source.procedures = []string{"p_clean"}
	source.triggers = []ObjectMeta{{Name: "trg_audit", Type: ObjectTrigger, Table: "users"}}
	source.sources = map[string]string{
		"function f_total":  "CREATE FUNCTION f_total() RETURNS int",
		"procedure p_clean": "CREATE PROCEDURE p_clean()",
		"trigger trg_audit": "CREATE TRIGGER trg_audit AFTER INSERT ON users",
	}
	return source
}

// 整库转储：表来自 ListTables（视图除外），视图、函数、过程与触发器来自 ListObjects；文件依次是表与数据、
// 外键、函数、过程与视图（同一段，没有依赖时按这个次序）、触发器（在所有数据之后，导入数据时不会触发）、数据之后的收尾与文件结尾。函数、
// 过程与触发器的定义来自 ObjectSource（触发器带上它的表），表与视图的来自 TableDDL。
func TestDumpWholeDatabaseWritesEveryObjectInItsSection(t *testing.T) {
	source := newWholeDumpSource()
	dialect := &dumpDialect{file: DumpFileScript{Opening: []string{"SET FK OFF;"}, Closing: []string{"SET FK ON;"}}}
	var out bytes.Buffer
	options := DumpOptions{ForeignKeys: true, Views: true, Routines: true, Triggers: true}

	result, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Whole: true, WithData: true, Options: options, Header: dumpHeader(), Writer: &out,
	}, source, dialect)
	require.NoError(t, err)

	assert.Equal(t, `-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: demo-db
-- Database: shop
-- Content: structure and data
-- Options: foreign keys, views, functions and procedures, triggers
-- Date: 2026-11-16 09:30:05 +08:00
--
-- File encoding: UTF-8

SET FK OFF;

-- ----------------------------
-- Table structure for orders
-- ----------------------------
CREATE TABLE orders (id int, user_id int);

-- ----------------------------
-- Records of orders
-- ----------------------------
BEGIN DATA orders;
INSERT INTO orders (id, user_id) VALUES (10, 1);
END DATA orders;

-- ----------------------------
-- Table structure for users
-- ----------------------------
CREATE TABLE users (id int, name text);

-- ----------------------------
-- Records of users
-- ----------------------------
BEGIN DATA users;
INSERT INTO users (id, name) VALUES (1, alice);
INSERT INTO users (id, name) VALUES (2, <nil>);
END DATA users;

-- ----------------------------
-- Foreign keys
-- ----------------------------
ADD FK orders;
ADD FK users;

-- ----------------------------
-- Function structure for f_total
-- ----------------------------
CREATE FUNCTION f_total() RETURNS int;

-- ----------------------------
-- Procedure structure for p_clean
-- ----------------------------
CREATE PROCEDURE p_clean();

-- ----------------------------
-- View structure for v_big
-- ----------------------------
CREATE VIEW v_big AS SELECT * FROM orders;

-- ----------------------------
-- Trigger structure for trg_audit
-- ----------------------------
CREATE TRIGGER trg_audit AFTER INSERT ON users;

-- ----------------------------
-- Sequence and auto-increment values
-- ----------------------------
SYNC orders;
SYNC users;

SET FK ON;
`, out.String())
	assert.Equal(t, DumpResult{Objects: 6, Rows: 3, Bytes: int64(out.Len())}, *result)
	assert.Equal(t, []DumpScope{{Database: "shop", Tables: []string{"orders", "users", "v_big"}, WithData: true, Whole: true, Options: options}}, dialect.scopes)
	assert.Equal(t, []DumpObject{
		{Database: "shop", Kind: DumpTable, Name: "orders", Definition: source.ddl["orders"], WithData: true, Options: options},
		{Database: "shop", Kind: DumpTable, Name: "users", Definition: source.ddl["users"], WithData: true, Options: options},
		{Database: "shop", Kind: ObjectFunction, Name: "f_total", Definition: source.sources["function f_total"], Options: options},
		{Database: "shop", Kind: ObjectProcedure, Name: "p_clean", Definition: source.sources["procedure p_clean"], Options: options},
		{Database: "shop", Kind: ObjectView, Name: "v_big", Definition: source.ddl["v_big"], Options: options},
		{Database: "shop", Kind: ObjectTrigger, Name: "trg_audit", Table: "users", Definition: source.sources["trigger trg_audit"], Options: options},
	}, dialect.objects)
	assert.Contains(t, source.calls, "source shop trigger trg_audit users")
	assert.NotContains(t, source.calls, "stream shop.v_big")
}

// 整个 schema：只有这个 schema 里的对象（名字是 ListTables / ListObjects 给出的「schema.名字」），别的
// schema 与不属于任何 schema 的对象（库级触发器）都不在其中；方言拿到的范围也只有这个 schema 的表。
func TestDumpWholeSchemaKeepsToItsSchema(t *testing.T) {
	source := &dumpSource{
		ddl: map[string]string{
			"public.a": "CREATE TABLE public.a ()", "sales.b": "CREATE TABLE sales.b ()",
			"sales_archive.c": "CREATE TABLE sales_archive.c ()", "sales.v": "CREATE VIEW sales.v AS SELECT 1",
			"public.v": "CREATE VIEW public.v AS SELECT 1",
		},
		tables:     []string{"public.a", "public.v", "sales.b", "sales.v", "sales_archive.c"},
		views:      []string{"public.v", "sales.v"},
		functions:  []string{"public.f", "sales.f"},
		procedures: []string{"sales_archive.p"},
		triggers: []ObjectMeta{
			{Name: "public.t", Type: ObjectTrigger, Table: "a"}, {Name: "sales.t", Type: ObjectTrigger, Table: "b"},
			{Name: "ddl_guard", Type: ObjectTrigger},
		},
		sources: map[string]string{"function sales.f": "CREATE FUNCTION sales.f()", "trigger sales.t": "CREATE TRIGGER sales.t"},
	}
	dialect := &dumpDialect{plain: true}

	result, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Schema: "sales", Whole: true, Options: allOptions, Header: dumpHeader(), Writer: &bytes.Buffer{},
	}, source, dialect)
	require.NoError(t, err)

	names := make([]string, 0, len(dialect.objects))
	for _, object := range dialect.objects {
		names = append(names, object.Kind+" "+object.Name)
	}
	assert.Equal(t, []string{"table sales.b", "function sales.f", "view sales.v", "trigger sales.t"}, names)
	assert.Equal(t, 4, result.Objects)
	assert.Equal(t, []DumpScope{{Database: "shop", Schema: "sales", Tables: []string{"sales.b", "sales.v"}, Whole: true, Options: allOptions}}, dialect.scopes)
}

// 整库或整个 schema 的视图取自 ListObjects：ListTables 只列表的驱动（如 Oracle 扩展）照样转储视图。ListTables
// 也列出的视图按它的次序、只写一次、不当作表，只有 ListObjects 知道的接在后面（按它的次序）；方言拿到的范围里
// 也有它们。不勾「视图」时一个视图也不写。
func TestDumpWholeTakesViewsFromListObjects(t *testing.T) {
	source := func() *dumpSource {
		return &dumpSource{
			ddl: map[string]string{
				"sales.b": "CREATE TABLE sales.b ()", "sales.v_listed": "CREATE VIEW sales.v_listed AS SELECT 1",
				"sales.v_only": "CREATE VIEW sales.v_only AS SELECT 1", "public.v": "CREATE VIEW public.v AS SELECT 1",
			},
			tables: []string{"sales.b", "sales.v_listed"},
			views:  []string{"public.v", "sales.v_only", "sales.v_listed"},
		}
	}
	dump := func(t *testing.T, options DumpOptions) (*dumpDialect, string) {
		t.Helper()
		dialect := &dumpDialect{plain: true}
		var out bytes.Buffer
		_, err := Dump(context.Background(), DumpRequest{
			Database: "shop", Schema: "sales", Whole: true, Options: options, Header: dumpHeader(), Writer: &out,
		}, source(), dialect)
		require.NoError(t, err)
		return dialect, out.String()
	}

	t.Run("views on", func(t *testing.T) {
		dialect, _ := dump(t, allOptions)
		names := make([]string, 0, len(dialect.objects))
		for _, object := range dialect.objects {
			names = append(names, object.Kind+" "+object.Name)
		}
		assert.Equal(t, []string{"table sales.b", "view sales.v_listed", "view sales.v_only"}, names)
		assert.Equal(t, []DumpScope{{
			Database: "shop", Schema: "sales", Tables: []string{"sales.b", "sales.v_listed", "sales.v_only"}, Whole: true, Options: allOptions,
		}}, dialect.scopes)
	})

	t.Run("views off", func(t *testing.T) {
		options := allOptions
		options.Views = false
		dialect, out := dump(t, options)
		require.Len(t, dialect.objects, 1)
		assert.Equal(t, "sales.b", dialect.objects[0].Name)
		assert.NotContains(t, out, "VIEW")
	})
}

// 转储选中的表时只有表与选中的视图：不列库里的表，不写函数、过程与触发器。
func TestDumpSelectedTablesLeavesOutRoutinesAndTriggers(t *testing.T) {
	source := newWholeDumpSource()
	var out bytes.Buffer

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users"}, Options: allOptions, Header: dumpHeader(), Writer: &out,
	}, source, &dumpDialect{plain: true})
	require.NoError(t, err)

	assert.NotContains(t, out.String(), "f_total")
	assert.NotContains(t, out.String(), "trg_audit")
	assert.Equal(t, []string{"objects shop", "ddl shop.users"}, source.calls)
}

// definitionsSource 是只有视图、函数与过程（和一张表）的库：视图的定义来自 TableDDL，其余的来自 ObjectSource。
func definitionsSource(functions, procedures, views []string) *dumpSource {
	source := &dumpSource{
		ddl: map[string]string{"users": "CREATE TABLE users"}, sources: map[string]string{},
		tables: append([]string{"users"}, views...), views: views, functions: functions, procedures: procedures,
	}
	for _, name := range views {
		source.ddl[name] = "CREATE VIEW " + name
	}
	for _, name := range functions {
		source.sources[ObjectFunction+" "+name] = "CREATE FUNCTION " + name
	}
	for _, name := range procedures {
		source.sources[ObjectProcedure+" "+name] = "CREATE PROCEDURE " + name
	}
	return source
}

// definitionOrder 是文件里视图、函数与过程建出的次序（「种类 名字」），以及删除段里删除它们的次序。
func definitionOrder(t *testing.T, source *dumpSource, dialect *dumpDialect, req DumpRequest) (created, dropped []string) {
	t.Helper()
	var out bytes.Buffer
	req.Header, req.Writer = dumpHeader(), &out
	_, err := Dump(context.Background(), req, source, dialect)
	require.NoError(t, err)
	for _, line := range strings.Split(out.String(), "\n") {
		for _, kind := range []string{ObjectView, ObjectFunction, ObjectProcedure} {
			if name, ok := strings.CutPrefix(line, "CREATE "+strings.ToUpper(kind)+" "); ok {
				created = append(created, kind+" "+strings.TrimSuffix(name, ";"))
			}
			if name, ok := strings.CutPrefix(line, "DROP "+strings.ToUpper(kind)+" IF EXISTS "); ok {
				dropped = append(dropped, kind+" "+strings.TrimSuffix(name, ";"))
			}
		}
	}
	return created, dropped
}

func reversedNames(names []string) []string {
	out := slices.Clone(names)
	slices.Reverse(out)
	return out
}

// 视图、函数与过程在同一段里按依赖排序，被依赖的在前：视图可以依赖函数，函数也可以依赖视图；方言经
// Dependencies（带种类）给出依赖，不在这一段里的名字（表）不算。没给依赖的对象函数与过程
// 在前、视图在后，各自按列出的次序。删除段按这个次序倒过来删。
func TestDumpOrdersViewsFunctionsAndProceduresByTheirDependencies(t *testing.T) {
	whole := DumpRequest{Database: "shop", Whole: true, Options: allOptions}

	t.Run("across kinds", func(t *testing.T) {
		source := definitionsSource([]string{"f_reads", "f_used"}, []string{"p_run"}, []string{"v_calls", "v_base"})
		dialect := &dumpDialect{plain: true, dependencies: map[string][]DumpDependency{
			// 引用视图的函数（PostgreSQL 的 BEGIN ATOMIC、SQL Server 的 SCHEMABINDING）。
			"function f_reads": {{Kind: ObjectView, Name: "v_base"}},
			// 调用函数的视图。
			"view v_calls": {{Kind: ObjectFunction, Name: "f_used"}},
			"view v_base":  {{Kind: DumpTable, Name: "users"}},
			"procedure p_run": {{Kind: ObjectFunction, Name: "f_reads"}, {Kind: ObjectView, Name: "v_calls"},
				{Kind: ObjectProcedure, Name: "p_run"}},
		}}
		created, dropped := definitionOrder(t, source, dialect, whole)
		want := []string{"view v_base", "function f_reads", "function f_used", "view v_calls", "procedure p_run"}
		assert.Equal(t, want, created)
		assert.Equal(t, reversedNames(want), dropped)
	})
	t.Run("without dependencies functions and procedures come before views", func(t *testing.T) {
		source := definitionsSource([]string{"f_b", "f_a"}, []string{"p_z"}, []string{"a_view", "z_view"})
		created, dropped := definitionOrder(t, source, &dumpDialect{plain: true}, whole)
		want := []string{"function f_b", "function f_a", "procedure p_z", "view a_view", "view z_view"}
		assert.Equal(t, want, created)
		assert.Equal(t, reversedNames(want), dropped)
	})
	t.Run("a view and a function of the same name are told apart by their kind", func(t *testing.T) {
		source := definitionsSource([]string{"same"}, nil, []string{"v_a", "same"})
		dialect := &dumpDialect{plain: true, dependencies: map[string][]DumpDependency{
			"view v_a":  {{Kind: ObjectFunction, Name: "same"}},
			"view same": {{Kind: ObjectView, Name: "v_a"}},
		}}
		created, _ := definitionOrder(t, source, dialect, whole)
		assert.Equal(t, []string{"function same", "view v_a", "view same"}, created)
	})
	t.Run("selected views keep the selected order where nothing depends", func(t *testing.T) {
		source := definitionsSource(nil, nil, []string{"a_report", "b_mid", "c_base", "d_alone"})
		dialect := &dumpDialect{plain: true, dependencies: map[string][]DumpDependency{
			"view a_report": {{Kind: DumpTable, Name: "users"}, {Kind: ObjectView, Name: "b_mid"}},
			"view b_mid":    {{Kind: ObjectView, Name: "c_base"}},
		}}
		created, _ := definitionOrder(t, source, dialect, DumpRequest{Database: "shop", Tables: []string{"d_alone", "a_report", "c_base", "b_mid"}})
		assert.Equal(t, []string{"view d_alone", "view c_base", "view b_mid", "view a_report"}, created)
	})
}

// 依赖成环时（如 SQL Server 里互相调用的过程，延迟名称解析，谁先建都行）转储照样写出：环在回到正在放的对象处断开，
// 环外被依赖的仍在前；删除段按这个次序倒过来删。
func TestDumpBreaksADependencyCycle(t *testing.T) {
	source := definitionsSource([]string{"f_loop"}, []string{"p_alone"}, []string{"v_a", "v_b"})
	dialect := &dumpDialect{plain: true, dependencies: map[string][]DumpDependency{
		"function f_loop": {{Kind: ObjectView, Name: "v_a"}},
		"view v_a":        {{Kind: ObjectView, Name: "v_b"}},
		"view v_b":        {{Kind: ObjectFunction, Name: "f_loop"}, {Kind: ObjectProcedure, Name: "p_alone"}},
	}}

	created, dropped := definitionOrder(t, source, dialect, DumpRequest{Database: "shop", Whole: true, Options: allOptions})
	want := []string{"procedure p_alone", "view v_b", "view v_a", "function f_loop"}
	assert.Equal(t, want, created)
	assert.Equal(t, reversedNames(want), dropped)
}

// 整库转储里函数、过程或触发器的定义读不到时同样立即停下，错误说出那个对象。
func TestDumpStopsAtARoutineThatFailed(t *testing.T) {
	source := newWholeDumpSource()
	source.sourceErr = map[string]error{"p_clean": errors.New("permission denied")}

	_, err := Dump(context.Background(), DumpRequest{Database: "shop", Whole: true, Options: allOptions, Header: dumpHeader(), Writer: &bytes.Buffer{}}, source, &dumpDialect{})
	var objectErr *DumpObjectError
	require.ErrorAs(t, err, &objectErr)
	assert.Equal(t, "p_clean", objectErr.Object)
	assert.ErrorContains(t, err, "permission denied")
	assert.NotContains(t, source.calls, "source shop trigger trg_audit users")
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

// wholeDump 是整库、结构和数据、按 options 的一份转储（方言给出文件的每一部分）。
func wholeDump(t *testing.T, options DumpOptions) string {
	t.Helper()
	var out bytes.Buffer
	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Whole: true, WithData: true, Options: options, Header: dumpHeader(), Writer: &out,
	}, newWholeDumpSource(), &dumpDialect{file: fullFile})
	require.NoError(t, err)
	return out.String()
}

// withoutOptionsLine 去掉注释头里列出选项的那一行：比较两份选项不同的文件时只看正文。
func withoutOptionsLine(text string) string {
	lines := strings.Split(text, "\n")
	return strings.Join(slices.DeleteFunc(lines, func(line string) bool { return strings.HasPrefix(line, "-- Options: ") }), "\n")
}

// 删除段：在文件开头（开头与关闭外键检查之后、类型之前），先删外键，再按倒序删触发器、视图与过程和函数、表，
// 最后删类型；对象自己的位置上不再有删除语句。
func TestDumpWritesTheDropSectionFirst(t *testing.T) {
	full := wholeDump(t, allOptions)

	assert.Contains(t, full, `SET NAMES utf8;
SET FK OFF;

-- ----------------------------
-- Drop existing objects
-- ----------------------------
DROP FK orders;
DROP FK users;
DROP TRIGGER IF EXISTS trg_audit;
DROP VIEW IF EXISTS v_big;
DROP PROCEDURE IF EXISTS p_clean;
DROP FUNCTION IF EXISTS f_total;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS orders;
DROP TYPE IF EXISTS mood;

CREATE TYPE mood;

-- ----------------------------
-- Table structure for orders
-- ----------------------------
CREATE TABLE orders (id int, user_id int);
`)
	assert.Equal(t, 9, strings.Count(full, "DROP "), "the drop statements are written once, all in the drop section")
}

// 每个选项不勾时，文件里正好少了它的那一部分，其余一字不差（注释头里列出的选项除外）。
func TestDumpOptionsLeaveOutExactlyTheirPart(t *testing.T) {
	full := wholeDump(t, allOptions)
	section := func(title string, statements ...string) string {
		return "\n-- ----------------------------\n-- " + title + "\n-- ----------------------------\n" + strings.Join(statements, "\n") + "\n"
	}
	cases := map[string]struct {
		off   func(*DumpOptions)
		parts []string
	}{
		"indexes":      {func(o *DumpOptions) { o.Indexes = false }, []string{"CREATE INDEX ix_orders;\n", "CREATE INDEX ix_users;\n"}},
		"foreign keys": {func(o *DumpOptions) { o.ForeignKeys = false }, []string{section("Foreign keys", "ADD FK orders;", "ADD FK users;")}},
		"views": {func(o *DumpOptions) { o.Views = false }, []string{
			"DROP VIEW IF EXISTS v_big;\n", section("View structure for v_big", "CREATE VIEW v_big AS SELECT * FROM orders;"),
		}},
		"functions and procedures": {func(o *DumpOptions) { o.Routines = false }, []string{
			"DROP PROCEDURE IF EXISTS p_clean;\n", "DROP FUNCTION IF EXISTS f_total;\n",
			section("Function structure for f_total", "CREATE FUNCTION f_total() RETURNS int;"),
			section("Procedure structure for p_clean", "CREATE PROCEDURE p_clean();"),
		}},
		"triggers": {func(o *DumpOptions) { o.Triggers = false }, []string{
			"DROP TRIGGER IF EXISTS trg_audit;\n", section("Trigger structure for trg_audit", "CREATE TRIGGER trg_audit AFTER INSERT ON users;"),
		}},
		"drop statements": {func(o *DumpOptions) { o.Drops = false }, []string{section("Drop existing objects",
			"DROP FK orders;", "DROP FK users;", "DROP TRIGGER IF EXISTS trg_audit;", "DROP VIEW IF EXISTS v_big;",
			"DROP PROCEDURE IF EXISTS p_clean;", "DROP FUNCTION IF EXISTS f_total;", "DROP TABLE IF EXISTS users;",
			"DROP TABLE IF EXISTS orders;", "DROP TYPE IF EXISTS mood;")}},
		"disable foreign key checks": {func(o *DumpOptions) { o.DisableForeignKeyChecks = false }, []string{"SET FK OFF;\n", "SET FK ON;\n"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			options := allOptions
			tc.off(&options)
			want := withoutOptionsLine(full)
			for _, part := range tc.parts {
				require.Contains(t, want, part)
				want = strings.Replace(want, part, "", 1)
			}
			assert.Equal(t, want, withoutOptionsLine(wholeDump(t, options)))
		})
	}
}

// 「写入删除语句」不勾时文件里没有任何删除语句：方言给出的存在则删除、外键与类型的删除都不写。
func TestDumpWithoutDropsWritesNoDropStatement(t *testing.T) {
	options := allOptions
	options.Drops = false

	assert.NotContains(t, wholeDump(t, options), "DROP")
}

// 注释头列出所选的选项；整库时才有视图、函数与过程、触发器这几项。
func TestDumpHeaderListsTheChosenOptions(t *testing.T) {
	assert.Contains(t, wholeDump(t, allOptions),
		"-- Options: indexes, foreign keys, views, functions and procedures, triggers, drop statements, disable foreign key checks\n")
	assert.Contains(t, wholeDump(t, DumpOptions{Views: true, Drops: true}), "-- Options: views, drop statements\n")
	assert.Contains(t, wholeDump(t, DumpOptions{}), "-- Options: none\n")
}

// partitionedSource 是有分区表与继承表的库：events 按年分区（events_2026 再按地区分区，events_eu 是它的分区），
// 继承的子表 a_archive 排在它的父表 notes 前面。每张表只有它自己的一行；读父表默认连子表的行一起读出。
func partitionedSource() *dumpSource {
	source := &dumpSource{ddl: map[string]string{}, columns: map[string][]string{}, rows: map[string][][]any{}, children: map[string][]string{
		"events": {"events_2025", "events_2026"}, "events_2026": {"events_eu"}, "notes": {"a_archive"},
	}}
	for i, table := range []string{"a_archive", "events", "events_2025", "events_2026", "events_eu", "notes"} {
		source.tables = append(source.tables, table)
		source.ddl[table] = "CREATE TABLE " + table
		source.columns[table] = []string{"id"}
		if table != "events" && table != "events_2026" { // 分区表的父表自己没有行
			source.rows[table] = [][]any{{int64(i)}}
		}
	}
	return source
}

// partitionedDialect 给出分区与父表：分区表的父表列出它的分区，分区与继承的子表依赖它们的父表。
func partitionedDialect() *dumpDialect {
	parent := func(name string) []DumpDependency { return []DumpDependency{{Kind: DumpTable, Name: name}} }
	return &dumpDialect{plain: true,
		partitions: map[string][]string{"events": {"events_2025", "events_2026"}, "events_2026": {"events_eu"}},
		dependencies: map[string][]DumpDependency{
			"table events_2025": parent("events"), "table events_2026": parent("events"), "table events_eu": parent("events_2026"),
			"table a_archive": parent("notes"),
		},
	}
}

// tableOrder 是文件里建表的次序，以及删除段里删表的次序。
func tableOrder(file string) (created, dropped []string) {
	for _, line := range strings.Split(file, "\n") {
		if name, ok := strings.CutPrefix(line, "CREATE TABLE "); ok {
			created = append(created, strings.TrimSuffix(name, ";"))
		}
		if name, ok := strings.CutPrefix(line, "DROP TABLE IF EXISTS "); ok {
			dropped = append(dropped, strings.TrimSuffix(name, ";"))
		}
	}
	return created, dropped
}

// 分区与继承的子表写在它们的父表之后（方言经 Dependencies 给出父表），删除段里先删子表；其余的表保持列出的次序。
// 每张表只写它自己的行：读表时不带子表的行，父表的行不会再在子表里写一遍。
func TestDumpWritesChildTablesAfterTheirParentsWithTheirOwnRows(t *testing.T) {
	source := partitionedSource()
	var out bytes.Buffer

	result, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Whole: true, WithData: true, Options: DumpOptions{Drops: true}, Header: dumpHeader(), Writer: &out,
	}, source, partitionedDialect())
	require.NoError(t, err)

	created, dropped := tableOrder(out.String())
	want := []string{"notes", "a_archive", "events", "events_2025", "events_2026", "events_eu"}
	assert.Equal(t, want, created)
	assert.Equal(t, reversedNames(want), dropped)
	assert.EqualValues(t, 4, result.Rows, "each row is written once, by the table it belongs to")
	for _, insert := range []string{"a_archive (id) VALUES (0)", "events_2025 (id) VALUES (2)", "events_eu (id) VALUES (4)", "notes (id) VALUES (5)"} {
		assert.Equal(t, 1, strings.Count(out.String(), "INSERT INTO "+insert+";"), insert)
	}
	// 分区表（给出了分区的表）自己没有行：不读它的行，也没有它的记录段。
	for _, partitioned := range []string{"events", "events_2026"} {
		assert.NotContains(t, out.String(), "-- Records of "+partitioned+"\n")
		assert.NotContains(t, source.calls, "stream shop."+partitioned)
	}
	assert.Contains(t, out.String(), "-- Records of notes\n")
}

// 只转储选中的表：选中分区表的父表时它的全部分区（一路下去）随它写出，排在它之后，方言拿到的范围里也有它们；
// 选中的分区不再重复写。单独选中一个分区或继承的子表时只写它自己。
func TestDumpBringsAlongThePartitionsOfASelectedTable(t *testing.T) {
	dump := func(t *testing.T, tables ...string) ([]string, *dumpDialect) {
		t.Helper()
		dialect := partitionedDialect()
		var out bytes.Buffer
		_, err := Dump(context.Background(), DumpRequest{Database: "shop", Tables: tables, Header: dumpHeader(), Writer: &out},
			partitionedSource(), dialect)
		require.NoError(t, err)
		created, _ := tableOrder(out.String())
		return created, dialect
	}

	selected := append(make([]string, 0, 8), "notes", "events_2026", "events")
	created, dialect := dump(t, selected...)
	assert.Equal(t, []string{"notes", "events", "events_2026", "events_eu", "events_2025"}, created)
	assert.Equal(t, make([]string, 5), selected[len(selected):cap(selected)], "the caller's selection is left as it was")
	require.Len(t, dialect.scopes, 1)
	assert.Equal(t, []string{"notes", "events_2026", "events", "events_eu", "events_2025"}, dialect.scopes[0].Tables)

	created, _ = dump(t, "events_eu", "a_archive")
	assert.Equal(t, []string{"events_eu", "a_archive"}, created)
}
