package command

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/host_key_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/host_key_repo"
	"github.com/opskat/opskat/internal/service/credential_resolver"
)

// otpExecServer 是一台「公钥部分成功后要求单提示 OTP」的 SSH 服务器，能执行 exec
// 请求（回显 "ok:<command>"）。challenges 统计服务器发出的 OTP 挑战次数。
type otpExecServer struct {
	addr       string
	challenges atomic.Int32
}

func newOTPExecServer(t *testing.T, otp string) *otpExecServer {
	t.Helper()
	s := &otpExecServer{}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
				KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
					s.challenges.Add(1)
					answers, err := ch("Verification", "Enter code", []string{"OTP: "}, []bool{false})
					if err != nil {
						return nil, err
					}
					if len(answers) != 1 || answers[0] != otp {
						return nil, fmt.Errorf("bad otp")
					}
					return nil, nil
				},
			}}
		},
	}
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	require.NoError(t, err)
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s.addr = ln.Addr().String()
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveExecConn(conn, cfg)
		}
	}()
	return s
}

func serveExecConn(conn net.Conn, cfg *ssh.ServerConfig) {
	defer func() { _ = conn.Close() }()
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer func() { _ = ch.Close() }()
			for req := range chReqs {
				if req.Type != "exec" {
					_ = req.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &payload)
				_ = req.Reply(true, nil)
				_, _ = fmt.Fprintf(ch, "ok:%s", payload.Command)
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			}
		}()
	}
}

// setupBatchMFAAsset 用临时文件 SQLite 注册资产与主机密钥仓库（并发拨号会开多条
// 数据库连接，in-memory 库不跨连接共享），为每台 srv 插入一个私钥文件认证 SSH 资产。
func setupBatchMFAAssets(t *testing.T, srvs ...*otpExecServer) []*asset_entity.Asset {
	t.Helper()
	// withMFA 的生产桌面应答方会拨真实数据目录下的 approval.sock；测试绝不能碰到
	// 正在运行的桌面端，因此一律视为不可达。
	origDial := dialApprovalSocket
	dialApprovalSocket = func(string) error { return errors.New("desktop unreachable in tests") }
	t.Cleanup(func() { dialApprovalSocket = origDial })
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "opskat.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&asset_entity.Asset{}, &host_key_entity.HostKey{}))
	db.SetDefault(gdb)
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	host_key_repo.RegisterHostKey(host_key_repo.NewHostKey())
	t.Cleanup(func() {
		if origAsset != nil {
			asset_repo.RegisterAsset(origAsset)
		}
	})

	assets := make([]*asset_entity.Asset, 0, len(srvs))
	for i, srv := range srvs {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		block, err := ssh.MarshalPrivateKey(priv, "")
		require.NoError(t, err)
		keyPath := filepath.Join(t.TempDir(), "id_ed25519")
		require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600))

		host, port, err := net.SplitHostPort(srv.addr)
		require.NoError(t, err)
		asset := &asset_entity.Asset{
			Name: fmt.Sprintf("bastion-%d", i), Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusActive, Createtime: 1,
			Config: fmt.Sprintf(`{"host":%q,"port":%s,"username":"alice","auth_type":"key","private_keys":[%q]}`, host, port, keyPath),
		}
		require.NoError(t, asset_repo.Asset().Create(context.Background(), asset))
		assets = append(assets, asset)
	}
	return assets
}

// runBatchExecConcurrently 以 cmdBatch 同样的方式准备 ctx，并发执行 n 条针对同一资产的 exec 条目。
func runBatchExecConcurrently(t *testing.T, code string, n int, assets ...*asset_entity.Asset) []batchResult {
	t.Helper()
	ctx, closeBatch := withBatchSSH(withMFACode(context.Background(), code))
	defer closeBatch()
	results := make([]batchResult, n*len(assets))
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = executeBatchExec(ctx, resolvedBatchCmd{asset: assets[i/n], command: fmt.Sprintf("cmd-%d", i)})
		}(i)
	}
	wg.Wait()
	return results
}

func TestBatchExec_SameAssetSharesOneVerifiedConnection(t *testing.T) {
	srv := newOTPExecServer(t, "123456")
	asset := setupBatchMFAAssets(t, srv)[0]

	results := runBatchExecConcurrently(t, "123456", 4, asset)
	for i, r := range results {
		assert.Equal(t, 0, r.ExitCode, "item %d: %+v", i, r)
		assert.Equal(t, fmt.Sprintf("ok:cmd-%d", i), r.Stdout)
	}
	assert.Equal(t, int32(1), srv.challenges.Load(), "one batch run verifies each asset once")
}

func TestBatchExec_FailedVerificationFailsOnlyThatAssetsItems(t *testing.T) {
	rejecting := newOTPExecServer(t, "123456")
	accepting := newOTPExecServer(t, "000000")
	assets := setupBatchMFAAssets(t, rejecting, accepting)

	results := runBatchExecConcurrently(t, "000000", 4, assets...)
	for i, r := range results[:4] {
		assert.NotEmpty(t, r.Error, "item %d on the rejecting asset must fail", i)
		assert.Equal(t, -1, r.ExitCode)
	}
	for i, r := range results[4:] {
		assert.Equal(t, 0, r.ExitCode, "item %d on the other asset must be unaffected: %+v", i, r)
	}
	assert.Equal(t, int32(1), rejecting.challenges.Load(), "a failed verification is not retried by later items")
	assert.Equal(t, int32(1), accepting.challenges.Load())
}

func TestBatchExec_NoMFASourceEndsBatchWithNeedsMFA(t *testing.T) {
	srv := newOTPExecServer(t, "123456")
	asset := setupBatchMFAAssets(t, srv)[0]
	origIn, origErr := stdinIsTerminal, stderrIsTerminal
	stdinIsTerminal = func() bool { return false }
	stderrIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal, stderrIsTerminal = origIn, origErr })

	results := runBatchExecConcurrently(t, "", 2, asset)

	var stderr bytes.Buffer
	code := batchExitCode(&stderr, results, false)
	assert.Equal(t, refusalExitCode, code)
	firstLine, _, _ := strings.Cut(stderr.String(), "\n")
	assert.Equal(t, needsMFAMarker, firstLine)
	assert.Equal(t, int32(1), srv.challenges.Load())
}

// SSH 隧道上的 MFA 不在本轮范围内（spec Out of scope）：batch 的数据类条目经 SSH 隧道
// 拨号时不得拿到 MFA 应答方——--mfa-code 只属于 SSH exec 条目，隧道行为保持不变。
func TestBatchDataItem_TunnelDialGetsNoMFAResponder(t *testing.T) {
	srv := newOTPExecServer(t, "123456")
	sshAsset := setupBatchMFAAssets(t, srv)[0]

	var dialErr error
	handlers := map[string]tool.ToolHandlerFunc{
		batchAuditTool: func(ctx context.Context, _ map[string]any) (string, error) {
			client, closers, err := credential_resolver.Default().DialAssetSSH(ctx, sshAsset.ID)
			dialErr = err
			if err == nil {
				_ = client.Close()
				for _, c := range closers {
					_ = c.Close()
				}
			}
			return "", err
		},
	}
	ctx, closeBatch := withBatchSSH(withMFACode(context.Background(), "123456"))
	defer closeBatch()
	db := &asset_entity.Asset{ID: 999, Name: "mysql", Type: asset_entity.AssetTypeDatabase}
	executeBatchHandler(ctx, handlers, batchAuditTool, resolvedBatchCmd{asset: db, command: "select 1"}, map[string]any{})

	assert.Error(t, dialErr, "a tunnel dial must not answer MFA with the batch's --mfa-code")
}
