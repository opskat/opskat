package query

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
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
