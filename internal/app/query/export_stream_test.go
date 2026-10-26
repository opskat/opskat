package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 整表的 CSV / TSV 导出：一次对驱动的读取（一个游标），分批交给前端；批数再多也只有一次取数。
func TestTableExportCursorReadsTheWholeTableInOneDriverCall(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Rows = nil
	for i := 0; i < 2500; i++ {
		fake.Rows = append(fake.Rows, []any{i, fmt.Sprintf("n%d", i)})
	}
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})
	filters := []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "name", Operator: dbdriver.OpContains, Value: "n"}}
	sorts := []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortDescending}}

	cursor, err := q.OpenTableExport(1, "reporting", "users", TableExportStreamRequest{Filters: filters, Sorts: sorts})
	require.NoError(t, err)
	assert.Equal(t, fake.Columns, cursor.Columns)

	var total, reads int
	for done := false; !done; reads++ {
		chunk, err := q.ReadTableExport(cursor.ID, 1000)
		require.NoError(t, err)
		if total == 0 {
			assert.Equal(t, map[string]any{"id": 0, "name": "n0"}, chunk.Rows[0])
		}
		total += len(chunk.Rows)
		done = chunk.Done
	}
	assert.Equal(t, 2500, total)
	assert.GreaterOrEqual(t, reads, 3)
	require.NoError(t, q.CloseTableExport(cursor.ID))

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []dbdrivertest.TableCall{
		{Op: "stream", Database: "reporting", Table: "users", Query: dbdriver.TableQuery{Filters: filters, Sorts: sorts}},
	}, sessions[0].TableCalls(), "one driver call however many batches the frontend reads")
	assert.Empty(t, sessions[0].Statements())

	_, err = q.ReadTableExport(cursor.ID, 10)
	require.Error(t, err, "a closed cursor cannot be read")
}

func TestTableExportCursorReturnsTheDriversError(t *testing.T) {
	for name, tableErr := range map[string]error{
		"a database error":            errors.New("fakedb: relation \"users\" does not exist"),
		"a driver without table rows": fmt.Errorf("%w: fakedb does not browse tables", dbdriver.ErrUnsupported),
	} {
		t.Run(name, func(t *testing.T) {
			fake := dbdrivertest.NewFake()
			fake.TableErr = tableErr
			dbdrivertest.Register(t, fake)
			q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

			_, err := q.OpenTableExport(1, "", "users", TableExportStreamRequest{})
			require.ErrorIs(t, err, tableErr)
			for _, s := range fake.Sessions() {
				assert.Empty(t, s.Statements())
			}
		})
	}
}

// 内置驱动：SQLite 真库上整表读完，行与列同表里一致。
func TestTableExportCursorWorksForABuiltInDriver(t *testing.T) {
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	for _, statement := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)",
		"INSERT INTO users (id, name) VALUES (1, 'alice'), (2, 'bob'), (3, 'carol')",
	} {
		_, err := q.ExecuteSQL(1, statement, "")
		require.NoError(t, err)
	}
	cursor, err := q.OpenTableExport(1, "", "users", TableExportStreamRequest{
		Sorts: []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortDescending}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, cursor.Columns)
	chunk, err := q.ReadTableExport(cursor.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"id": int64(3), "name": "carol"}, {"id": int64(2), "name": "bob"}}, chunk.Rows)
	assert.False(t, chunk.Done)
	chunk, err = q.ReadTableExport(cursor.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"id": int64(1), "name": "alice"}}, chunk.Rows)
	assert.True(t, chunk.Done)
	require.NoError(t, q.CloseTableExport(cursor.ID))
}

// 整表导出可以比面板缓存的空闲时限长：游标读着的时候，它底下的会话不能被当成空闲的收走
// （远端 SQLite 的会话一关，文件系统就跟着关了，导出读到一半失败）。游标关了才重新算空闲。
func TestTableExportCursorKeepsItsSessionFromIdleEviction(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	cursor, err := q.OpenTableExport(1, "", "users", TableExportStreamRequest{})
	require.NoError(t, err)
	sessions := fake.Sessions()
	require.Len(t, sessions, 1)

	q.dbPanelCache.evictOnce(time.Now().Add(time.Hour))
	assert.False(t, sessions[0].Closed(), "a session an export cursor is reading from is not idle")
	chunk, err := q.ReadTableExport(cursor.ID, 10)
	require.NoError(t, err)
	assert.Len(t, chunk.Rows, len(fake.Rows))

	require.NoError(t, q.CloseTableExport(cursor.ID))
	q.dbPanelCache.evictOnce(time.Now().Add(time.Hour))
	assert.True(t, sessions[0].Closed(), "once the cursor is closed the session idles out as before")
}

// singleConnectionSQLite 是经 sqlitevfs 打开的远端 SQLite（只有一条连接），远端文件系统换成本地
// 目录：会话与真实的远端 SQLite 走同一条路径，只是不经 SSH。
type singleConnectionSQLite struct{ dbdriver.Driver }

const singleConnectionSQLiteID asset_entity.DatabaseDriver = "sqliteoneconn"

func (d singleConnectionSQLite) Descriptor() dbdriver.Descriptor {
	desc := d.Driver.Descriptor()
	desc.ID, desc.Aliases = singleConnectionSQLiteID, []string{}
	return desc
}

func (d singleConnectionSQLite) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	cfg := *req.Config
	cfg.Driver, cfg.SQLiteSource = asset_entity.DriverSQLite, string(asset_entity.SQLiteSourceRemoteSSHVFS)
	req.Config = &cfg
	req.Transport.RemoteFS = func(context.Context) (dbdriver.RemoteFS, error) { return localFS{}, nil }
	return d.Driver.Open(ctx, req)
}

type localFS struct{}

func (localFS) OpenFile(name string, flag int) (dbdriver.RemoteFile, error) {
	return os.OpenFile(name, flag, 0o600) //nolint:gosec // test files under t.TempDir()
}
func (localFS) Remove(name string) error              { return os.Remove(name) }
func (localFS) Stat(name string) (os.FileInfo, error) { return os.Stat(name) }
func (localFS) Close() error                          { return nil }

// 整表导出为 SQL：远端 SQLite 的会话只有一条连接，导出的读游标一直占着它。语句的生成不能再要
// 第二条连接，否则要等到查询超时，导出失败（「打开数据库会话失败: context deadline exceeded」）。
// 游标交出的语句与 GetRowStatements 对前端拿到的同样几行给出的一字不差。
func TestExportAllAsSQLNeedsNoSecondConnection(t *testing.T) {
	inner, err := dbregistry.Lookup(asset_entity.DriverSQLite)
	require.NoError(t, err)
	dbdrivertest.Register(t, singleConnectionSQLite{inner})
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{
		Driver: singleConnectionSQLiteID, Path: filepath.Join(t.TempDir(), "remote.db"), QueryTimeoutSeconds: 1,
	})
	for _, statement := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, data BLOB, score REAL, created DATETIME)",
		"INSERT INTO users VALUES (1, 'O''Reilly C:\\new', X'4142', 1.5, '2026-10-18 12:00:00'), (2, NULL, NULL, NULL, NULL)",
	} {
		_, err := q.ExecuteSQL(1, statement, "")
		require.NoError(t, err)
	}
	columns := []string{"id", "name", "data", "score", "created"}
	sorts := []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortAscending}}

	cursor, err := q.OpenTableExport(1, "", "users", TableExportStreamRequest{Sorts: sorts, InsertColumns: columns})
	require.NoError(t, err)
	// 每批一行：第一批读完时游标还没到头，仍占着那条连接。
	var statements []string
	for done := false; !done; {
		chunk, err := q.ReadTableExport(cursor.ID, 1)
		require.NoError(t, err)
		assert.Empty(t, chunk.Rows, "exporting as SQL hands over statements, not rows")
		statements = append(statements, chunk.Statements...)
		done = chunk.Done
	}
	require.NoError(t, q.CloseTableExport(cursor.ID))
	require.Len(t, statements, 2)
	// 日期时间列读出来是 time.Time，写成前端拿到的文本。
	assert.Equal(t, `INSERT INTO "users" ("id", "name", "data", "score", "created") VALUES ('1', 'O''Reilly C:\new', X'4142', '1.5', '2026-10-18T12:00:00Z');`, statements[0])
	assert.Equal(t, `INSERT INTO "users" ("id", "name", "data", "score", "created") VALUES ('2', NULL, NULL, NULL, NULL);`, statements[1])

	// 同样几行经前端（JSON）交给 GetRowStatements：一次读完整张表，游标到头即还回连接。
	cursor, err = q.OpenTableExport(1, "", "users", TableExportStreamRequest{Sorts: sorts})
	require.NoError(t, err)
	chunk, err := q.ReadTableExport(cursor.ID, 1000)
	require.NoError(t, err)
	require.NoError(t, q.CloseTableExport(cursor.ID))
	data, err := json.Marshal(chunk)
	require.NoError(t, err)
	var sent TableExportChunk
	require.NoError(t, json.Unmarshal(data, &sent))
	fromRows, err := q.GetRowStatements(1, "", "users", dbdriver.RowStatementsRequest{Kind: dbdriver.RowInsert, Columns: columns, Rows: sent.Rows})
	require.NoError(t, err)
	assert.Equal(t, fromRows, statements)
}

// 导出为 SQL：生成语句要的一切在打开读取之前向驱动备好，之后每批的语句由备好的 writer 给出，
// 没有别的驱动调用，也没有语句被执行。
func TestTableExportAsSQLPreparesTheStatementsBeforeTheRead(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapExportSQL)
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	cursor, err := q.OpenTableExport(1, "reporting", "users", TableExportStreamRequest{InsertColumns: []string{"id"}})
	require.NoError(t, err)
	first, err := q.ReadTableExport(cursor.ID, 2)
	require.NoError(t, err)
	rest, err := q.ReadTableExport(cursor.ID, 2)
	require.NoError(t, err)
	require.NoError(t, q.CloseTableExport(cursor.ID))
	assert.Equal(t, []string{"FAKE INSERT ROW 1 OF reporting.users", "FAKE INSERT ROW 2 OF reporting.users"}, first.Statements)
	assert.Equal(t, []string{"FAKE INSERT ROW 1 OF reporting.users"}, rest.Statements)
	assert.True(t, rest.Done)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	calls := sessions[0].TableCalls()
	require.Len(t, calls, 4)
	assert.Equal(t, []string{"prepare_row_statements", "stream", "row_statements", "row_statements"},
		[]string{calls[0].Op, calls[1].Op, calls[2].Op, calls[3].Op})
	assert.Equal(t, &dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"id"}, Rows: []map[string]any{{"id": float64(1), "name": "alice"}, {"id": float64(2), "name": "bob"}},
	}, calls[2].RowStatements)
	assert.Empty(t, sessions[0].Statements())
}

// 没有声明导出为 SQL 的驱动：打开导出为 SQL 的游标以 ErrUnsupported 拒绝，表也不读。
func TestTableExportAsSQLRefusedWhenTheDriverDoesNotDeclareIt(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.OpenTableExport(1, "", "users", TableExportStreamRequest{InsertColumns: []string{"id"}})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.TableCalls())
	}
}
