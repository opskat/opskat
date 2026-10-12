package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 202603290001 只改得到它执行那一刻库里的行。之后才写进来的数字 ID——旧版本程序在已迁移
// 的库上继续写、或导入了旧版本导出的备份——它管不到，而带数字 ID 的策略整条解析不了。
func TestMigration202610120001_RepairsLegacyPolicyGroupIDs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:migration202610120001?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE assets (id INTEGER PRIMARY KEY, command_policy TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE groups (id INTEGER PRIMARY KEY, command_policy TEXT, query_policy TEXT, redis_policy TEXT)`).Error)

	const alreadyMigrated = `{"allow_list":["ls *"],"groups":["builtin:linux-readonly","7"]}`
	require.NoError(t, db.Exec(`INSERT INTO assets (id, command_policy) VALUES
		(31, '{"allow_list":["*","cp:*"],"groups":[-1,-4,"builtin:linux-readonly"]}'),
		(3,  '{"groups":[-5,-6]}'),
		(40, ?),
		(41, '')`, alreadyMigrated).Error)
	require.NoError(t, db.Exec(`INSERT INTO groups (id, command_policy, query_policy, redis_policy) VALUES
		(5, '{"groups":[-4,12]}', '{"groups":[-5]}', '{"groups":[-8]}')`).Error)

	m := migration202610120001()
	require.NoError(t, m.Migrate(db), "首次执行")
	require.NoError(t, m.Migrate(db), "重复执行不能报错")

	assetPolicy := func(id int) string {
		var policy string
		require.NoError(t, db.Raw(`SELECT command_policy FROM assets WHERE id = ?`, id).Scan(&policy).Error)
		return policy
	}
	// 数字 ID 与它早先被手工补上的字符串 ID 指向同一个组，只留一份
	assert.JSONEq(t, `{"allow_list":["*","cp:*"],"groups":["builtin:linux-readonly","builtin:dangerous-deny"]}`, assetPolicy(31))
	assert.JSONEq(t, `{"groups":["builtin:sql-readonly","builtin:sql-dangerous-deny"]}`, assetPolicy(3))
	assert.Equal(t, alreadyMigrated, assetPolicy(40), "已经是字符串 ID 的行不改写")
	assert.Equal(t, "", assetPolicy(41))

	var group struct{ CommandPolicy, QueryPolicy, RedisPolicy string }
	require.NoError(t, db.Raw(`SELECT command_policy, query_policy, redis_policy FROM groups WHERE id = 5`).Scan(&group).Error)
	assert.JSONEq(t, `{"groups":["builtin:dangerous-deny","12"]}`, group.CommandPolicy)
	assert.JSONEq(t, `{"groups":["builtin:sql-readonly"]}`, group.QueryPolicy)
	assert.JSONEq(t, `{"groups":["builtin:redis-dangerous-deny"]}`, group.RedisPolicy)
}
