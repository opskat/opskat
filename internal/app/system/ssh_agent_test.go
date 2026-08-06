package system

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/service/ssh_agent_svc"
	"github.com/opskat/opskat/internal/sshagent"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// fakeAgentSrv serves a well-behaved in-memory agent over a real unix socket so
// the transport's peer-credential checks see a real same-UID peer. Mirrors the
// fixture in internal/service/ssh_agent_svc.
type fakeAgentSrv struct {
	path string
	ln   net.Listener
}

func startFakeAgent(t *testing.T, keys ...agent.AddedKey) *fakeAgentSrv {
	t.Helper()
	kr := agent.NewKeyring()
	for _, k := range keys {
		require.NoError(t, kr.Add(k))
	}
	// Keep the socket path short: macOS limits unix socket paths to 104 bytes.
	dir, err := os.MkdirTemp("", "ag")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "a")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = agent.ServeAgent(kr, conn)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return &fakeAgentSrv{path: path, ln: ln}
}

func agentTestKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	return priv, ssh.FingerprintSHA256(signer.PublicKey())
}

// setupAgentBinderTest 装好真实来源仓库 + 资产仓库 + 内存 SQLite，返回 ctx 已就绪的
// System（真实服务路径，不经 Wails runtime）。
func setupAgentBinderTest(t *testing.T) *System {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix socket fixtures are not used on windows")
	}
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&ssh_agent_source_entity.SSHAgentSource{}, &asset_entity.Asset{}))
	db.SetDefault(gdb)
	origSrc := ssh_agent_source_repo.SSHAgentSource()
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	t.Cleanup(func() {
		if origSrc != nil {
			ssh_agent_source_repo.RegisterSSHAgentSource(origSrc)
		}
		if origAsset != nil {
			asset_repo.RegisterAsset(origAsset)
		}
	})
	return &System{ctx: context.Background(), lang: "zh-cn"}
}

func createAgentRefAsset(t *testing.T, ctx context.Context, sourceID int64) int64 {
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

func TestListAgentSources_SummaryOmitsEndpoint(t *testing.T) {
	s := setupAgentBinderTest(t)
	ctx := context.Background()

	_, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
	require.NoError(t, err)

	Convey("列出来源只返回摘要，不暴露端点", t, func() {
		list, err := s.ListAgentSources()
		assert.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "work", list[0].Name)
		assert.Equal(t, "environment", list[0].EndpointType)
		raw, err := json.Marshal(list)
		assert.NoError(t, err)
		assert.NotContains(t, string(raw), "SSH_AUTH_SOCK", "摘要 JSON 不得携带端点值")
		_ = ctx
	})
}

func TestGetAgentSource_FullDefinition(t *testing.T) {
	s := setupAgentBinderTest(t)

	src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: "/tmp/foo"})
	require.NoError(t, err)

	Convey("读单来源定义返回完整端点（来源编辑/探测界面）", t, func() {
		got, err := s.GetAgentSource(src.ID)
		assert.NoError(t, err)
		assert.Equal(t, "/tmp/foo", got.Endpoint)
		assert.Equal(t, "work", got.Name)
	})
}

func TestCreateAgentSource_ValidationAndPersist(t *testing.T) {
	s := setupAgentBinderTest(t)

	Convey("创建来源只做结构校验并持久化", t, func() {
		Convey("空名称被拒绝", func() {
			_, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "  ", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
			assert.Error(t, err)
		})

		Convey("合法来源落库且可再次读到", func() {
			src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: "/tmp/foo"})
			assert.NoError(t, err)
			assert.NotZero(t, src.ID)
			got, err := s.GetAgentSource(src.ID)
			assert.NoError(t, err)
			assert.Equal(t, "work", got.Name)
		})
	})
}

func TestUpdateAgentSource(t *testing.T) {
	s := setupAgentBinderTest(t)
	src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
	require.NoError(t, err)

	Convey("更新来源只改名称也生效", t, func() {
		got, err := s.UpdateAgentSource(src.ID, ssh_agent_svc.SourceInput{Name: "renamed", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK"})
		assert.NoError(t, err)
		assert.Equal(t, "renamed", got.Name)
	})
}

func TestDeleteAgentSource_RejectsInUse(t *testing.T) {
	s := setupAgentBinderTest(t)
	ctx := context.Background()

	src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: "/tmp/foo"})
	require.NoError(t, err)

	Convey("删除被引用的来源被拒绝", t, func() {
		assetID := createAgentRefAsset(t, ctx, src.ID)
		defer func() { _ = asset_repo.Asset().Delete(ctx, assetID) }()
		err := s.DeleteAgentSource(src.ID)
		assert.Error(t, err)
		code, ok := ssh_agent_svc.CodeOf(err)
		assert.True(t, ok)
		assert.Equal(t, "ssh_agent_source_in_use", code)
	})

	Convey("删除未引用的来源成功", t, func() {
		free, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "free", EndpointType: "unix_socket", Endpoint: "/tmp/bar"})
		require.NoError(t, err)
		assert.NoError(t, s.DeleteAgentSource(free.ID))
		_, err = s.GetAgentSource(free.ID)
		assert.Error(t, err)
	})
}

func TestProbeAgentSource(t *testing.T) {
	s := setupAgentBinderTest(t)
	priv, fp := agentTestKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "work key"})
	_ = fp

	Convey("探测候选项", t, func() {
		Convey("可用端点返回 ok 与身份数", func() {
			res, err := s.ProbeAgentSource("unix_socket", srv.path)
			assert.NoError(t, err)
			assert.Equal(t, ssh_agent_svc.ProbeOK, res.Status)
			assert.Equal(t, 1, res.IdentityCount)
		})

		Convey("不可用端点返回 unavailable", func() {
			res, err := s.ProbeAgentSource("unix_socket", "/tmp/does-not-exist-xyz")
			assert.NoError(t, err)
			assert.Equal(t, ssh_agent_svc.ProbeUnavailable, res.Status)
		})
	})
}

func TestProbeSavedAgentSource(t *testing.T) {
	s := setupAgentBinderTest(t)
	priv, _ := agentTestKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "work key"})

	src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: srv.path})
	require.NoError(t, err)

	Convey("探测已保存来源按 ID 返回运行状态", t, func() {
		res, err := s.ProbeSavedAgentSource(src.ID)
		assert.NoError(t, err)
		assert.Equal(t, ssh_agent_svc.ProbeOK, res.Status)
	})
}

func TestInspectAgentSource(t *testing.T) {
	s := setupAgentBinderTest(t)
	ctx := context.Background()
	priv, fp := agentTestKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "work key"})

	src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: srv.path})
	require.NoError(t, err)

	Convey("检查身份返回有界摘要与使用数", t, func() {
		assetID := createAgentRefAsset(t, ctx, src.ID)
		defer func() { _ = asset_repo.Asset().Delete(ctx, assetID) }()

		res, err := s.InspectAgentSource(src.ID)
		assert.NoError(t, err)
		require.Len(t, res.Identities, 1)
		assert.Equal(t, fp, res.Identities[0].Fingerprint)
		assert.Equal(t, "ssh-ed25519", res.Identities[0].Type)
		assert.Equal(t, "work key", res.Identities[0].Comment)
		assert.Equal(t, int64(1), res.Usages)
	})
}

func TestTestAgentSourceFingerprint(t *testing.T) {
	s := setupAgentBinderTest(t)
	priv, fp := agentTestKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "work key"})

	src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: srv.path})
	require.NoError(t, err)

	Convey("测试指定来源+指纹组合", t, func() {
		Convey("精确指纹能选中签名器并真实签名", func() {
			assert.NoError(t, s.TestAgentSourceFingerprint(src.ID, fp))
		})

		Convey("不存在的指纹返回 identity_missing", func() {
			err := s.TestAgentSourceFingerprint(src.ID, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
			assert.Error(t, err)
			code, ok := ssh_agent_svc.CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, sshagent.CodeIdentityMissing, code)
		})

		Convey("来源不存在返回 source_not_found", func() {
			err := s.TestAgentSourceFingerprint(99999, fp)
			assert.Error(t, err)
			code, ok := ssh_agent_svc.CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, "ssh_agent_source_not_found", code)
		})
	})
}

func TestCopyAgentSourcePublicKey(t *testing.T) {
	s := setupAgentBinderTest(t)
	priv, fp := agentTestKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "do not include"})

	src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: srv.path})
	require.NoError(t, err)

	Convey("显式复制公钥不含 Agent 备注", t, func() {
		line, err := s.CopyAgentSourcePublicKey(src.ID, fp)
		assert.NoError(t, err)
		assert.True(t, strings.HasPrefix(line, "ssh-ed25519 "), "authorized_keys 行以密钥类型开头")
		assert.False(t, strings.Contains(line, "do not include"), "复制出的公钥不得携带 Agent 备注")
	})
}

func TestGetAgentSourceUsage(t *testing.T) {
	s := setupAgentBinderTest(t)
	ctx := context.Background()
	priv, _ := agentTestKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "work key"})

	src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: srv.path})
	require.NoError(t, err)

	Convey("读取来源使用数", t, func() {
		Convey("未被引用为 0", func() {
			n, err := s.GetAgentSourceUsage(src.ID)
			assert.NoError(t, err)
			assert.Equal(t, int64(0), n)
		})

		Convey("被一个活动资产引用为 1", func() {
			assetID := createAgentRefAsset(t, ctx, src.ID)
			defer func() { _ = asset_repo.Asset().Delete(ctx, assetID) }()
			n, err := s.GetAgentSourceUsage(src.ID)
			assert.NoError(t, err)
			assert.Equal(t, int64(1), n)
		})
	})
}

func TestGetAgentAssetDetail(t *testing.T) {
	s := setupAgentBinderTest(t)
	priv, fp := agentTestKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "work key"})

	src, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: srv.path})
	require.NoError(t, err)

	Convey("为资产详情读取所选 Agent 信息", t, func() {
		Convey("身份可用时返回类型/备注且不暴露端点与公钥", func() {
			detail, err := s.GetAgentAssetDetail(src.ID, fp)
			assert.NoError(t, err)
			assert.Equal(t, "work", detail.SourceName)
			assert.Equal(t, fp, detail.Fingerprint)
			assert.Equal(t, "ok", detail.Availability)
			assert.Equal(t, "ssh-ed25519", detail.Type)
			assert.Equal(t, "work key", detail.Comment)
			raw, err := json.Marshal(detail)
			assert.NoError(t, err)
			assert.NotContains(t, string(raw), srv.path, "资产详情不暴露端点")
			assert.NotContains(t, string(raw), "public_key", "资产详情不暴露公钥字段")
		})

		Convey("已存指纹当前缺失时标记 missing", func() {
			detail, err := s.GetAgentAssetDetail(src.ID, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
			assert.NoError(t, err)
			assert.Equal(t, "missing", detail.Availability)
			assert.Empty(t, detail.Type)
			assert.Empty(t, detail.Comment)
		})

		Convey("来源可达但未装载任何身份时标记 empty（不与 missing 混淆）", func() {
			emptySrv := startFakeAgent(t)
			emptySrc, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "empty", EndpointType: "unix_socket", Endpoint: emptySrv.path})
			require.NoError(t, err)
			detail, err := s.GetAgentAssetDetail(emptySrc.ID, fp)
			assert.NoError(t, err)
			assert.Equal(t, "empty", detail.Availability)
			assert.Empty(t, detail.Type)
			assert.Empty(t, detail.Comment)
		})

		Convey("来源不可用时标记 unavailable", func() {
			dead, err := s.CreateAgentSource(ssh_agent_svc.SourceInput{Name: "dead", EndpointType: "unix_socket", Endpoint: "/tmp/does-not-exist-xyz"})
			require.NoError(t, err)
			detail, err := s.GetAgentAssetDetail(dead.ID, fp)
			assert.NoError(t, err)
			assert.Equal(t, "unavailable", detail.Availability)
		})
	})
}
