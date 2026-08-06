package ssh_agent_svc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
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

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// fakeAgentSrv serves a well-behaved in-memory agent over a real unix socket so
// the transport's peer-credential checks see a real same-UID peer.
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
	// Keep the socket path short: macOS limits unix socket paths to 104 bytes,
	// and t.TempDir() names include the long test function name.
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

func testKey(t *testing.T) (ed25519.PrivateKey, ssh.PublicKey, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	pub := signer.PublicKey()
	return priv, pub, ssh.FingerprintSHA256(pub)
}

func setupInspectTest(t *testing.T) context.Context {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix socket fixtures are not used on windows")
	}
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&ssh_agent_source_entity.SSHAgentSource{}, &asset_entity.Asset{}))
	db.SetDefault(gdb)
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	return context.Background()
}

func TestInspect_ReturnsBoundedIdentitySummary(t *testing.T) {
	ctx := setupInspectTest(t)

	priv, _, fp := testKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "line1\n\x01line2\x7f"})

	src, err := Create(ctx, SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: srv.path})
	require.NoError(t, err)
	assetID := createAgentAsset(t, ctx, src.ID)
	defer func() { _ = asset_repo.Asset().Delete(ctx, assetID) }()

	Convey("检查来源返回有界身份摘要", t, func() {
		Convey("指纹/密钥类型/清理备注与使用数", func() {
			res, err := Inspect(ctx, src.ID)
			assert.NoError(t, err)
			require.Len(t, res.Identities, 1)
			assert.Equal(t, fp, res.Identities[0].Fingerprint)
			assert.Equal(t, "ssh-ed25519", res.Identities[0].Type)
			assert.Equal(t, "line1line2", res.Identities[0].Comment, "备注控制字符被清理")
			assert.Equal(t, int64(1), res.Usages, "引用该来源的活动资产数")
			assert.Equal(t, int64(1), res.Identities[0].Usages)
		})
	})
}

func TestInspect_MissingSource(t *testing.T) {
	ctx := setupInspectTest(t)

	Convey("检查不存在的来源返回 ssh_agent_source_not_found", t, func() {
		_, err := Inspect(ctx, 999)
		code, ok := CodeOf(err)
		assert.True(t, ok)
		assert.Equal(t, CodeSourceNotFound, code)
	})
}

func TestInspect_EndpointUnavailable(t *testing.T) {
	ctx := setupInspectTest(t)

	src, err := Create(ctx, SourceInput{Name: "dead", EndpointType: "unix_socket", Endpoint: "/tmp/definitely-not-a-socket-opskat"})
	require.NoError(t, err)

	Convey("端点不可达时检查返回类型化错误", t, func() {
		_, err := Inspect(ctx, src.ID)
		code, ok := CodeOf(err)
		assert.True(t, ok)
		assert.Equal(t, "ssh_agent_endpoint_unavailable", code)
	})
}

func TestProbe_Statuses(t *testing.T) {
	ctx := setupInspectTest(t)

	Convey("探测端点返回运行状态而不持久化", t, func() {
		Convey("可达且有身份 → ok", func() {
			priv, _, _ := testKey(t)
			srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			res, err := Probe(ctx, "unix_socket", srv.path)
			assert.NoError(t, err)
			assert.Equal(t, ProbeOK, res.Status)
			assert.Equal(t, 1, res.IdentityCount)
		})

		Convey("可达但无身份 → empty", func() {
			srv := startFakeAgent(t)
			res, err := Probe(ctx, "unix_socket", srv.path)
			assert.NoError(t, err)
			assert.Equal(t, ProbeEmpty, res.Status)
		})

		Convey("端点不可达 → unavailable", func() {
			res, err := Probe(ctx, "unix_socket", "/tmp/definitely-not-a-socket-opskat")
			assert.NoError(t, err)
			assert.Equal(t, ProbeUnavailable, res.Status)
		})

		Convey("环境变量未设置 → unavailable", func() {
			t.Setenv("MY_AGENT_SOCK", "")
			res, err := Probe(ctx, "environment", "MY_AGENT_SOCK")
			assert.NoError(t, err)
			assert.Equal(t, ProbeUnavailable, res.Status)
		})

		Convey("平台不支持的端点类型 → unsupported", func() {
			if runtime.GOOS == "windows" {
				t.Skip("windows 上无'不支持'类型可测")
			}
			res, err := Probe(ctx, "windows_named_pipe", `\\.\pipe\openssh-ssh-agent`)
			assert.NoError(t, err)
			assert.Equal(t, ProbeUnsupported, res.Status)
		})

		Convey("探测不写入 ssh_agent_sources", func() {
			priv, _, _ := testKey(t)
			srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			_, err := Probe(ctx, "unix_socket", srv.path)
			assert.NoError(t, err)
			list, err := List(ctx)
			assert.NoError(t, err)
			assert.Empty(t, list, "探测不得持久化任何来源")
		})
	})
}

func TestCopyPublicKey_ExplicitCopy(t *testing.T) {
	ctx := setupInspectTest(t)

	priv, _, fp := testKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "secret-agent-comment"})

	src, err := Create(ctx, SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: srv.path})
	require.NoError(t, err)

	Convey("显式复制公钥", t, func() {
		Convey("按来源+指纹重读，返回不含 Agent 备注的 authorized key 行", func() {
			line, err := CopyPublicKey(ctx, src.ID, fp)
			assert.NoError(t, err)
			assert.NotContains(t, line, "secret-agent-comment", "不得携带 Agent 备注")
			assert.Contains(t, line, "ssh-ed25519")

			// 返回行可重新解析为同一公钥。
			got, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
			assert.NoError(t, err)
			assert.Equal(t, ssh.FingerprintSHA256(got), fp)
		})

		Convey("指纹不匹配任何身份 → ssh_agent_identity_missing", func() {
			_, err := CopyPublicKey(ctx, src.ID, "SHA256:"+strings.Repeat("A", 43))
			code, ok := CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, "ssh_agent_identity_missing", code)
		})
	})
}

func TestLifecycle_NoImplicitPersistence(t *testing.T) {
	ctx := setupInspectTest(t)

	priv, _, _ := testKey(t)
	srv := startFakeAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})

	Convey("探测/检查都不会写入 ssh_agent_sources（仅显式保存）", t, func() {
		Convey("探测候选不落库", func() {
			_, err := Probe(ctx, "unix_socket", srv.path)
			assert.NoError(t, err)
			assert.Empty(t, mustListSources(t, ctx), "探测不得持久化任何来源")
		})

		Convey("检查已保存来源不新增来源行", func() {
			src, err := Create(ctx, SourceInput{Name: "work", EndpointType: "unix_socket", Endpoint: srv.path})
			require.NoError(t, err)
			_, err = Inspect(ctx, src.ID)
			assert.NoError(t, err)

			list, err := List(ctx)
			assert.NoError(t, err)
			require.Len(t, list, 1, "只有显式保存的那一个来源")
			assert.Equal(t, src.ID, list[0].ID)
		})
	})
}

func mustListSources(t *testing.T, ctx context.Context) []*ssh_agent_source_entity.SSHAgentSource {
	t.Helper()
	list, err := List(ctx)
	require.NoError(t, err)
	return list
}
