package credential_resolver

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/service/ssh_agent_svc"
	"github.com/opskat/opskat/internal/sshagent"
)

func setupAgentResolverTest(t *testing.T) context.Context {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&ssh_agent_source_entity.SSHAgentSource{}))
	db.SetDefault(gdb)
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	return context.Background()
}

func TestResolveAgentAuthConfig(t *testing.T) {
	convey.Convey("从 SSHConfig 解析 Agent 认证配置", t, func() {
		convey.Convey("非 Agent 认证返回 nil", func() {
			r := Default()
			cfg, err := r.ResolveAgentAuthConfig(&asset_entity.SSHConfig{
				Host: "h", Port: 22, Username: "u", AuthType: "password",
			})
			assert.NoError(t, err)
			assert.Nil(t, cfg)
		})

		convey.Convey("Agent 认证缺少来源 ID 报错", func() {
			r := Default()
			_, err := r.ResolveAgentAuthConfig(&asset_entity.SSHConfig{
				Host: "h", Port: 22, Username: "u", AuthType: "agent",
				AgentKeyFingerprint: "SHA256:abc",
			})
			assert.Error(t, err)
		})

		convey.Convey("来源在握手发生时解析（懒闭包，不提前打开传输）", func() {
			ctx := setupAgentResolverTest(t)
			r := Default()

			created := &ssh_agent_source_entity.SSHAgentSource{
				Name:         "work",
				EndpointType: "unix_socket",
				Endpoint:     "/tmp/agent.sock",
			}
			require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(ctx, created))
			require.NotZero(t, created.ID)

			cfg, err := r.ResolveAgentAuthConfig(&asset_entity.SSHConfig{
				Host: "h", Port: 22, Username: "u", AuthType: "agent",
				AgentSourceID:       created.ID,
				AgentKeyFingerprint: "SHA256:aaaa",
			})
			assert.NoError(t, err)
			assert.NotNil(t, cfg)
			assert.Equal(t, "SHA256:aaaa", cfg.Fingerprint)

			// 真正拨号时才调用 Source：返回保存的端点定义。
			src, err := cfg.Source(ctx)
			assert.NoError(t, err)
			assert.Equal(t, sshagent.EndpointTypeUnixSocket, src.Type)
			assert.Equal(t, "/tmp/agent.sock", src.Value)
		})

		convey.Convey("来源缺失时懒解析返回错误（在握手时暴露，而非配置解析时）", func() {
			ctx := setupAgentResolverTest(t)
			r := Default()
			cfg, err := r.ResolveAgentAuthConfig(&asset_entity.SSHConfig{
				Host: "h", Port: 22, Username: "u", AuthType: "agent",
				AgentSourceID:       999,
				AgentKeyFingerprint: "SHA256:aaaa",
			})
			assert.NoError(t, err)
			assert.NotNil(t, cfg)

			_, err = cfg.Source(ctx)
			assert.Error(t, err)
			code, ok := ssh_agent_svc.CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, "ssh_agent_source_not_found", code)
		})
	})
}
