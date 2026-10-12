package builtin_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	_ "github.com/opskat/opskat/internal/dbdriver/builtin"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
)

func TestBuiltinDriversPassConformance(t *testing.T) {
	// 这四个 id 就是已存资产配置里 driver 的取值：少一个或改了名，现有资产就成了「驱动不可用」。
	require.Equal(t, []string{"mssql", "mysql", "postgresql", "sqlite"}, dbdriver.IDs())

	for _, d := range dbdriver.List() {
		t.Run(string(d.Descriptor().ID), func(t *testing.T) {
			dbdrivertest.RunConformance(t, d)
		})
	}
}
