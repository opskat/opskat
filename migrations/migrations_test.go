package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigrationSSHAgentSourcesCreatesTable(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	require.NoError(t, migration202608040001().Migrate(gdb))

	var hasTable int
	require.NoError(t, gdb.Raw(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='ssh_agent_sources'",
	).Scan(&hasTable).Error)
	assert.Equal(t, 1, hasTable)

	// 列结构与实体一致：数字 ID、名称、端点类型、端点、可选描述、创建/更新时间。
	cols := map[string]string{}
	rows, err := gdb.Raw("PRAGMA table_info(ssh_agent_sources)").Rows()
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt interface{}
		require.NoError(t, rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk))
		cols[name] = typ
	}
	for _, col := range []string{"id", "name", "endpoint_type", "endpoint", "description", "createtime", "updatetime"} {
		assert.NotEmpty(t, cols[col], "missing column %s", col)
	}
}
