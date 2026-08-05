package ssh_agent_svc

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func setupServiceTest(t *testing.T) context.Context {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&ssh_agent_source_entity.SSHAgentSource{}, &asset_entity.Asset{}))
	db.SetDefault(gdb)
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	return context.Background()
}

// createAgentAsset inserts an active SSH asset whose config references the
// given source (auth_type=agent + agent_source_id).
func createAgentAsset(t *testing.T, ctx context.Context, sourceID int64) int64 {
	t.Helper()
	asset := &asset_entity.Asset{
		Name:       "box",
		Type:       asset_entity.AssetTypeSSH,
		Status:     asset_entity.StatusActive,
		Createtime: 1,
		Config:     fmt.Sprintf(`{"host":"h","port":22,"username":"u","auth_type":"agent","agent_source_id":%d}`, sourceID),
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, asset))
	return asset.ID
}

func TestSource_Create_ExplicitSave(t *testing.T) {
	ctx := setupServiceTest(t)

	Convey("创建来源只做结构校验，不探测、不检查平台支持", t, func() {
		Convey("结构合法的来源被持久化", func() {
			src, err := Create(ctx, SourceInput{Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
			assert.NoError(t, err)
			assert.NotZero(t, src.ID)
			assert.Equal(t, "work", src.Name)
			assert.Equal(t, "environment", src.EndpointType)
		})

		Convey("空名称被拒绝", func() {
			_, err := Create(ctx, SourceInput{Name: "  ", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
			assert.Error(t, err)
		})

		Convey("环境变量名语法非法被拒绝", func() {
			_, err := Create(ctx, SourceInput{Name: "x", EndpointType: "environment", Endpoint: "1BAD NAME"})
			assert.Error(t, err)
		})

		Convey("unix_socket 相对路径被拒绝（不相对工作目录解释）", func() {
			_, err := Create(ctx, SourceInput{Name: "x", EndpointType: "unix_socket", Endpoint: "relative/agent.sock"})
			assert.Error(t, err)
		})

		Convey("unix_socket 绝对路径（含 ~ 展开）被接受，即使 socket 不存在", func() {
			src, err := Create(ctx, SourceInput{Name: "x", EndpointType: "unix_socket", Endpoint: "~/nope/agent.sock"})
			assert.NoError(t, err)
			assert.NotZero(t, src.ID)
		})

		Convey("windows_named_pipe 接受本机 pipe、拒绝远程 UNC", func() {
			src, err := Create(ctx, SourceInput{Name: "x", EndpointType: "windows_named_pipe", Endpoint: `\\.\pipe\openssh-ssh-agent`})
			assert.NoError(t, err)
			assert.NotZero(t, src.ID)

			_, err = Create(ctx, SourceInput{Name: "y", EndpointType: "windows_named_pipe", Endpoint: `\\server\pipe\ssh-agent`})
			assert.Error(t, err)
		})

		Convey("当前平台不支持的端点类型仍可保存（导入保留 unsupported）", func() {
			if runtime.GOOS == "windows" {
				t.Skip("windows 上无不支持的来源类型可测")
			}
			src, err := Create(ctx, SourceInput{Name: "win", EndpointType: "windows_named_pipe", Endpoint: `\\.\pipe\openssh-ssh-agent`})
			assert.NoError(t, err)
			assert.Equal(t, "windows_named_pipe", src.EndpointType)
		})
	})
}

func TestSource_Update_InvalidatesOnEndpointChange(t *testing.T) {
	ctx := setupServiceTest(t)

	var invalidated []int64
	assetconn.RegisterInvalidator("ssh_agent_svc_test", func(_ context.Context, assetID int64) error {
		invalidated = append(invalidated, assetID)
		return nil
	})
	defer assetconn.UnregisterForTest("ssh_agent_svc_test")

	src, err := Create(ctx, SourceInput{Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
	require.NoError(t, err)
	assetID := createAgentAsset(t, ctx, src.ID)

	Convey("修改端点类型或值触发连接失效回调", t, func() {
		Convey("端点值变化使引用资产被失效", func() {
			_, err := Update(ctx, src.ID, SourceInput{Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK_2"})
			assert.NoError(t, err)
			assert.Equal(t, []int64{assetID}, invalidated)
		})

		Convey("只改名称或描述不触发连接失效", func() {
			invalidated = nil
			_, err := Update(ctx, src.ID, SourceInput{Name: "renamed", Description: "new desc", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK_2"})
			assert.NoError(t, err)
			assert.Empty(t, invalidated)
		})
	})
}

func TestSource_Delete_RejectsInUse(t *testing.T) {
	ctx := setupServiceTest(t)

	Convey("删除来源", t, func() {
		Convey("未被引用的来源可删除", func() {
			src, err := Create(ctx, SourceInput{Name: "a", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
			require.NoError(t, err)
			assert.NoError(t, Delete(ctx, src.ID))
		})

		Convey("被活动 Agent 资产引用的来源拒绝删除（ssh_agent_source_in_use）", func() {
			src, err := Create(ctx, SourceInput{Name: "a", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
			require.NoError(t, err)
			assetID := createAgentAsset(t, ctx, src.ID)

			err = Delete(ctx, src.ID)
			assert.Error(t, err)
			code, ok := CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, CodeSourceInUse, code)

			// 引用资产删除（软删除）后，来源不再被引用，可以删除。
			require.NoError(t, asset_repo.Asset().Delete(ctx, assetID))
			assert.NoError(t, Delete(ctx, src.ID))
		})

		Convey("删除不存在的来源返回 ssh_agent_source_not_found", func() {
			err := Delete(ctx, 999)
			assert.Error(t, err)
			code, ok := CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, CodeSourceNotFound, code)
		})
	})
}

func TestSource_Discover(t *testing.T) {
	ctx := setupServiceTest(t)

	Convey("发现候选项", t, func() {
		Convey("SSH_AUTH_SOCK 非空时给出 environment 候选项", func() {
			t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
			cands, err := Discover(ctx)
			assert.NoError(t, err)
			require.Len(t, cands, 1)
			assert.Equal(t, "environment", cands[0].EndpointType)
			assert.Equal(t, "SSH_AUTH_SOCK", cands[0].Endpoint)
		})

		Convey("SSH_AUTH_SOCK 为空时没有候选项", func() {
			t.Setenv("SSH_AUTH_SOCK", "")
			cands, err := Discover(ctx)
			assert.NoError(t, err)
			assert.Empty(t, cands)
		})

		Convey("已保存来源被排除", func() {
			t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
			_, err := Create(ctx, SourceInput{Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
			require.NoError(t, err)

			cands, err := Discover(ctx)
			assert.NoError(t, err)
			assert.Empty(t, cands)
		})
	})
}

func TestSource_GetAndList(t *testing.T) {
	ctx := setupServiceTest(t)

	src, err := Create(ctx, SourceInput{Name: "a", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK", Description: "d"})
	require.NoError(t, err)

	got, err := Get(ctx, src.ID)
	require.NoError(t, err)
	assert.Equal(t, "a", got.Name)
	assert.Equal(t, "d", got.Description)

	list, err := List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, src.ID, list[0].ID)

	_, err = Get(ctx, 999)
	code, ok := CodeOf(err)
	assert.True(t, ok)
	assert.Equal(t, CodeSourceNotFound, code)
}
