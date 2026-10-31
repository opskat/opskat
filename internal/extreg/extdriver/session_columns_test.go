package extdriver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 驱动声明自增只能在新表上设时，改表请求里要自增的新列在宿主这一侧就被拒绝、不交给扩展：按旧版本构建的
// 扩展也许会照样写出一条执行必败的语句。拒绝发生在任何调用之前，所以会话不需要真的登录过。
func TestDriverSessionRefusesAutoIncrementOnANewColumnForNewTablesOnly(t *testing.T) {
	s := &driverSession{
		drivers: newDBDriverPlugin(t),
		driver:  "extdb",
		columns: dbdriver.Descriptor{AutoIncrement: true, AutoIncrementNewTablesOnly: true}.ColumnFeatures(),
	}
	_, err := s.AlterTableStatements(context.Background(), "app", "users", dbdriver.AlterTableRequest{
		Columns: []dbdriver.AlterColumn{{Name: "seq", Type: "INTEGER", AutoIncrement: true}},
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "not sent to the extension")
	assert.Contains(t, err.Error(), `"seq"`)
}
