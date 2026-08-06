package assettype

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo/mock_ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/service/ssh_agent_svc"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validAgentFingerprintForType() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(raw)
}

// setupAgentAssetTypeTest 用真实 in-memory SQLite 注册来源/资产仓库，并插入一个
// 真实来源供 RequireSourceExists 命中。
func setupAgentAssetTypeTest(t *testing.T) (context.Context, int64) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&ssh_agent_source_entity.SSHAgentSource{}, &asset_entity.Asset{}))
	db.SetDefault(gdb)
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	asset_repo.RegisterAsset(asset_repo.NewAsset())

	src := &ssh_agent_source_entity.SSHAgentSource{Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK", Createtime: 1, Updatetime: 1}
	require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(context.Background(), src))
	return context.Background(), src.ID
}

func TestSSHHandlerAgentContract(t *testing.T) {
	h := &sshHandler{}
	fp := validAgentFingerprintForType()
	ctx, sourceID := setupAgentAssetTypeTest(t)

	Convey("SSH Agent 契约（AI CRUD 路径）", t, func() {
		Convey("ValidateCreateArgs", func() {
			Convey("合法 Agent 参数通过", func() {
				err := h.ValidateCreateArgs(map[string]any{
					"host": "h", "port": float64(22), "username": "u",
					"auth_type": "agent", "agent_source_id": float64(sourceID), "agent_key_fingerprint": fp,
				})
				assert.NoError(t, err)
			})

			Convey("agent_source_id 非正数被拒绝", func() {
				err := h.ValidateCreateArgs(map[string]any{
					"host": "h", "port": float64(22), "username": "u",
					"auth_type": "agent", "agent_source_id": float64(0), "agent_key_fingerprint": fp,
				})
				assert.Error(t, err)
			})

			Convey("指纹非规范被拒绝", func() {
				err := h.ValidateCreateArgs(map[string]any{
					"host": "h", "port": float64(22), "username": "u",
					"auth_type": "agent", "agent_source_id": float64(sourceID), "agent_key_fingerprint": "sha256:bad",
				})
				assert.Error(t, err)
			})

			Convey("Agent 与密码/私钥互斥被拒绝", func() {
				err := h.ValidateCreateArgs(map[string]any{
					"host": "h", "port": float64(22), "username": "u",
					"auth_type": "agent", "agent_source_id": float64(sourceID), "agent_key_fingerprint": fp,
					"password": "secret",
				})
				assert.Error(t, err)
			})

			Convey("非 Agent 认证携带 Agent 字段被拒绝", func() {
				err := h.ValidateCreateArgs(map[string]any{
					"host": "h", "port": float64(22), "username": "u",
					"auth_type": "password", "agent_source_id": float64(sourceID), "agent_key_fingerprint": fp,
				})
				assert.Error(t, err)
			})
		})

		Convey("ApplyCreateArgs Agent 模式", func() {
			a := &asset_entity.Asset{Name: "box", Type: asset_entity.AssetTypeSSH}
			err := h.ApplyCreateArgs(ctx, a, map[string]any{
				"host": "h", "port": float64(22), "username": "u",
				"auth_type": "agent", "agent_source_id": float64(sourceID), "agent_key_fingerprint": fp,
			})
			assert.NoError(t, err)
			cfg, err := a.GetSSHConfig()
			require.NoError(t, err)
			assert.Equal(t, "agent", cfg.AuthType)
			assert.Equal(t, sourceID, cfg.AgentSourceID)
			assert.Equal(t, fp, cfg.AgentKeyFingerprint)
			assert.Zero(t, cfg.CredentialID)
			assert.Empty(t, cfg.Password)
			assert.Empty(t, cfg.PrivateKeys)
			assert.Empty(t, cfg.PrivateKeyPassphrase)
		})

		Convey("ApplyUpdateArgs 切到 Agent 清除密码与密钥材料", func() {
			a := &asset_entity.Asset{Name: "box", Type: asset_entity.AssetTypeSSH}
			require.NoError(t, a.SetSSHConfig(&asset_entity.SSHConfig{
				Host: "h", Port: 22, Username: "u", AuthType: "password", Password: "encrypted", CredentialID: 3,
			}))
			err := h.ApplyUpdateArgs(ctx, a, map[string]any{
				"auth_type": "agent", "agent_source_id": float64(sourceID), "agent_key_fingerprint": fp,
			})
			assert.NoError(t, err)
			cfg, err := a.GetSSHConfig()
			require.NoError(t, err)
			assert.Equal(t, "agent", cfg.AuthType)
			assert.Equal(t, sourceID, cfg.AgentSourceID)
			assert.Equal(t, fp, cfg.AgentKeyFingerprint)
			assert.Zero(t, cfg.CredentialID)
			assert.Empty(t, cfg.Password)
			assert.Empty(t, cfg.PrivateKeys)
			assert.Empty(t, cfg.PrivateKeyPassphrase)
		})

		Convey("ApplyUpdateArgs 切离 Agent 删除 Agent 字段", func() {
			a := &asset_entity.Asset{Name: "box", Type: asset_entity.AssetTypeSSH}
			require.NoError(t, a.SetSSHConfig(&asset_entity.SSHConfig{
				Host: "h", Port: 22, Username: "u", AuthType: "agent",
				AgentSourceID: sourceID, AgentKeyFingerprint: fp,
			}))
			err := h.ApplyUpdateArgs(ctx, a, map[string]any{"auth_type": "password", "password": "newpass"})
			assert.NoError(t, err)
			cfg, err := a.GetSSHConfig()
			require.NoError(t, err)
			assert.Equal(t, "password", cfg.AuthType)
			assert.Zero(t, cfg.AgentSourceID)
			assert.Empty(t, cfg.AgentKeyFingerprint)
			assert.NotEmpty(t, cfg.Password)
		})

		Convey("ApplyUpdateArgs 只改 host 保留 Agent 模式", func() {
			a := &asset_entity.Asset{Name: "box", Type: asset_entity.AssetTypeSSH}
			require.NoError(t, a.SetSSHConfig(&asset_entity.SSHConfig{
				Host: "h", Port: 22, Username: "u", AuthType: "agent",
				AgentSourceID: sourceID, AgentKeyFingerprint: fp,
			}))
			err := h.ApplyUpdateArgs(ctx, a, map[string]any{"host": "h2"})
			assert.NoError(t, err)
			cfg, err := a.GetSSHConfig()
			require.NoError(t, err)
			assert.Equal(t, "agent", cfg.AuthType)
			assert.Equal(t, sourceID, cfg.AgentSourceID)
		})
	})
}

func TestSSHHandlerAgentContractMissingSource(t *testing.T) {
	// 用 mock 来源仓库覆盖"来源缺失"路径，不依赖真实 SQLite 状态。
	h := &sshHandler{}
	fp := validAgentFingerprintForType()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mock := mock_ssh_agent_source_repo.NewMockSSHAgentSourceRepo(ctrl)
	orig := ssh_agent_source_repo.SSHAgentSource()
	ssh_agent_source_repo.RegisterSSHAgentSource(mock)
	t.Cleanup(func() {
		if orig != nil {
			ssh_agent_source_repo.RegisterSSHAgentSource(orig)
		}
	})

	Convey("ApplyCreateArgs 引用不存在的来源返回 ssh_agent_source_not_found", t, func() {
		mock.EXPECT().Find(gomock.Any(), int64(42)).Return(nil, gorm.ErrRecordNotFound)
		a := &asset_entity.Asset{Name: "box", Type: asset_entity.AssetTypeSSH}
		err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
			"host": "h", "port": float64(22), "username": "u",
			"auth_type": "agent", "agent_source_id": float64(42), "agent_key_fingerprint": fp,
		})
		assert.Error(t, err)
		code, ok := ssh_agent_svc.CodeOf(err)
		assert.True(t, ok)
		assert.Equal(t, "ssh_agent_source_not_found", code)
	})
}
