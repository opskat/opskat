package query

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// 树的浏览（列库、列表、表元数据、对象列表、对象源码）由驱动在自己的会话上完成，
// 假驱动的结果原样呈现；它的会话不是 *sql.DB，也没有任何语句经过共享代码。
func TestBrowsingRunsOnTheDriverSession(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	databases, err := q.ListDatabases(1)
	require.NoError(t, err)
	assert.Equal(t, fake.Databases, databases)

	tables, err := q.ListTables(1, "reporting")
	require.NoError(t, err)
	assert.Equal(t, fake.Tables["reporting"], tables)

	meta, err := q.GetTableMetadata(1, "reporting", "users")
	require.NoError(t, err)
	assertSameJSON(t, fake.Metadata, meta)

	objects, err := q.ListDatabaseObjects(1, "reporting")
	require.NoError(t, err)
	assertSameJSON(t, fake.Objects, objects)

	source, err := q.GetObjectSource(1, "reporting", "view", "v_users", "")
	require.NoError(t, err)
	assert.Equal(t, fake.Sources["view:v_users"], source)

	sessions := fake.Sessions()
	require.NotEmpty(t, sessions)
	for _, s := range sessions {
		assert.Empty(t, s.Statements(), "the driver answers browsing itself; no statement goes through Execute")
	}
}

// 驱动没提供的操作明确报「不支持」，且不执行任何语句。
func TestBrowsingRefusesWhatTheDriverDoesNotProvide(t *testing.T) {
	fake := dbdrivertest.NewFake() // 只声明了视图，没有存储过程
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.GetObjectSource(1, "", "procedure", "p_users", "")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.Statements())
	}
}

// 数据库返回的错误原样到达调用方。
func TestBrowsingReturnsTheDriversError(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.BrowseErr = assert.AnError
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.ListDatabases(1)
	require.ErrorIs(t, err, fake.BrowseErr)
	_, err = q.ListTables(1, "x")
	require.ErrorIs(t, err, fake.BrowseErr)
}

// 绑定返回的类型与驱动的类型是两套定义；它们序列化给前端的 JSON 必须一致。
func assertSameJSON(t *testing.T, want, got any) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantJSON), string(gotJSON))
}
