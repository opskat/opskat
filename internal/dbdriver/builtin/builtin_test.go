package builtin_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	_ "github.com/opskat/opskat/internal/dbdriver/builtin"
)

// 一致性测试在各驱动自己的包里跑（它需要每个驱动给一条不依赖真库的会话）；这里只固定
// 「空导入本包就链接上了哪些驱动」。
func TestBuiltinLinksTheFourDrivers(t *testing.T) {
	// 这四个 id 就是已存资产配置里 driver 的取值：少一个或改了名，现有资产就成了「驱动不可用」。
	// 顺序是它们各自声明的展示顺序，也就是资产表单驱动下拉一直以来的顺序。
	require.Equal(t, []string{"mysql", "postgresql", "mssql", "sqlite"}, dbregistry.IDs())
}
