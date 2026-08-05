package ssh_agent_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestRequireSourceExists(t *testing.T) {
	Convey("RequireSourceExists 引用完整性检查", t, func() {
		gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, gdb.AutoMigrate(&ssh_agent_source_entity.SSHAgentSource{}, &asset_entity.Asset{}))
		db.SetDefault(gdb)
		ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
		asset_repo.RegisterAsset(asset_repo.NewAsset())
		ctx := context.Background()

		Convey("存在的来源通过", func() {
			src, err := Create(ctx, SourceInput{Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
			require.NoError(t, err)
			assert.NoError(t, RequireSourceExists(ctx, src.ID))
		})

		Convey("不存在的来源返回 ssh_agent_source_not_found", func() {
			err := RequireSourceExists(ctx, 999)
			assert.Error(t, err)
			code, ok := CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, CodeSourceNotFound, code)
		})
	})
}
