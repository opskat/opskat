package system

import (
	"encoding/base64"
	"strconv"
	"testing"

	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo/mock_ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/service/ssh_agent_svc"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agentFingerprint() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(raw)
}

// registerAgentSourceMock 注册来源仓库 mock；返回后调用方设置 Find 期望。
func registerAgentSourceMock(t *testing.T) *mock_ssh_agent_source_repo.MockSSHAgentSourceRepo {
	t.Helper()
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
	return mock
}

func rawSSHAsset(t *testing.T, config string) *asset_entity.Asset {
	t.Helper()
	a := &asset_entity.Asset{Name: "x", Type: asset_entity.AssetTypeSSH}
	a.Config = config
	return a
}

// TestCreateAsset_SSHAgentWriteBoundary 钉住桌面 IPC 写入边界：SSH Agent 判别/来源
// 字段的重复 JSON key 与非规范拼写必须被稳定错误 token 拒绝，即使调用方绕过前端。
func TestCreateAsset_SSHAgentWriteBoundary(t *testing.T) {
	Convey("SSH Agent 字段 JSON 写入边界（IPC）", t, func() {
		s, assetMock, auditMem := setupAssetAudit(t)

		Convey("重复 auth_type key 被稳定 token 拒绝且不落库", func() {
			asset := rawSSHAsset(t, `{"host":"h","port":22,"username":"u","auth_type":"password","auth_type":"agent"}`)
			err := s.CreateAsset(asset)
			assert.Error(t, err)
			code, ok := asset_entity.AgentConfigCodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, asset_entity.CodeAgentConfigDuplicateKey, code)
			require.Len(t, auditMem.logs, 1)
			assert.Equal(t, 0, auditMem.logs[0].Success)
		})

		Convey("非规范拼写 authType 被拒绝", func() {
			asset := rawSSHAsset(t, `{"host":"h","port":22,"username":"u","authType":"agent"}`)
			err := s.CreateAsset(asset)
			assert.Error(t, err)
			code, ok := asset_entity.AgentConfigCodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, asset_entity.CodeAgentConfigNoncanonicalKey, code)
		})

		Convey("非 SSH 资产不检查 Agent 写入边界", func() {
			asset := &asset_entity.Asset{Name: "x", Type: asset_entity.AssetTypeRedis}
			asset.Config = `{"host":"h","port":6379,"auth_type":"password","auth_type":"agent"}`
			assetMock.EXPECT().Create(gomock.Any(), asset).Return(nil)
			assert.NoError(t, s.CreateAsset(asset))
		})
	})
}

// TestCreateAsset_SSHAgentReferenceGuard 钉住引用完整性：Agent 资产不能引用不存在的
// 来源（ssh_agent_source_not_found）。
func TestCreateAsset_SSHAgentReferenceGuard(t *testing.T) {
	Convey("Agent 资产引用完整性（IPC）", t, func() {
		s, assetMock, _ := setupAssetAudit(t)
		mock := registerAgentSourceMock(t)
		fp := agentFingerprint()
		agentConfig := func(sourceID int64) string {
			return `{"host":"h","port":22,"username":"u","auth_type":"agent","agent_source_id":` +
				strconv.FormatInt(sourceID, 10) + `,"agent_key_fingerprint":"` + fp + `"}`
		}

		Convey("来源存在时保存通过", func() {
			mock.EXPECT().Find(gomock.Any(), int64(7)).Return(&ssh_agent_source_entity.SSHAgentSource{ID: 7}, nil)
			asset := rawSSHAsset(t, agentConfig(7))
			assetMock.EXPECT().Create(gomock.Any(), asset).Return(nil)
			assert.NoError(t, s.CreateAsset(asset))
		})

		Convey("来源不存在时拒绝（ssh_agent_source_not_found）", func() {
			mock.EXPECT().Find(gomock.Any(), int64(42)).Return(nil, gorm.ErrRecordNotFound)
			asset := rawSSHAsset(t, agentConfig(42))
			err := s.CreateAsset(asset)
			assert.Error(t, err)
			code, ok := ssh_agent_svc.CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, ssh_agent_svc.CodeSourceNotFound, code)
		})
	})
}
