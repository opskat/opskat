package credential_resolver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/pkg/sshtuning"
	"github.com/opskat/opskat/internal/repository/asset_repo"
)

// createHopAsset 插入一个密钥文件认证的 SSH 资产。extraConfig 是追加到 config 里的
// JSON 片段（以逗号开头），用来给这台机器配它自己的连接路径；tunnelID 是资产行上的
// SSH 隧道字段。
func createHopAsset(t *testing.T, ctx context.Context, host string, tunnelID int64, extraConfig string) int64 {
	t.Helper()
	return createHopAssetAt(t, ctx, host, 22, tunnelID, extraConfig)
}

func createHopAssetAt(t *testing.T, ctx context.Context, host string, port int, tunnelID int64, extraConfig string) int64 {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600))
	asset := &asset_entity.Asset{
		Name:        host,
		Type:        asset_entity.AssetTypeSSH,
		Status:      asset_entity.StatusActive,
		Createtime:  1,
		SSHTunnelID: tunnelID,
		Config: fmt.Sprintf(`{"host":%q,"port":%d,"username":"root","auth_type":"key","private_keys":[%q]%s}`,
			host, port, keyPath, extraConfig),
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, asset))
	return asset.ID
}

func sshChainJSON(assetIDs ...int64) string {
	layers := ""
	for i, id := range assetIDs {
		if i > 0 {
			layers += ","
		}
		layers += fmt.Sprintf(`{"id":"l%d","type":"ssh","order":%d,"ssh_asset_id":%d}`, i+1, i+1, id)
	}
	return fmt.Sprintf(`,"proxy_chain":{"layers":[%s]}`, layers)
}

func sshChain(assetIDs ...int64) *asset_entity.ProxyChainConfig {
	chain := &asset_entity.ProxyChainConfig{}
	for i, id := range assetIDs {
		chain.Layers = append(chain.Layers, asset_entity.ProxyChainLayer{
			ID: fmt.Sprintf("l%d", i+1), Type: asset_entity.ProxyChainLayerSSH, Order: i + 1, SSHAssetID: id,
		})
	}
	return chain
}

func hopHosts(layers []proxychain.Layer) []string {
	hosts := make([]string, 0, len(layers))
	for _, layer := range layers {
		hosts = append(hosts, layer.Host)
	}
	return hosts
}

func TestResolveProxyChainFirstHopUpstream(t *testing.T) {
	convey.Convey("代理链第一跳从本机拨出，沿用那台 SSH 资产自己的连接路径", t, func() {
		ctx := setupAgentDialTest(t)

		convey.Convey("跳板机自己配了代理链时，先经它的上游再到它", func() {
			relay := createHopAsset(t, ctx, "relay.internal", 0, "")
			bastion := createHopAsset(t, ctx, "bastion.example", 0, sshChainJSON(relay))

			layers, err := Default().ResolveProxyChain(ctx, sshChain(bastion), 5)
			require.NoError(t, err)
			assert.Equal(t, []string{"relay.internal", "bastion.example"}, hopHosts(layers))
		})

		convey.Convey("跳板机用资产上的 SSH 隧道字段指向上游时同样沿用", func() {
			relay := createHopAsset(t, ctx, "relay.internal", 0, "")
			bastion := createHopAsset(t, ctx, "bastion.example", relay, "")

			layers, err := Default().ResolveProxyChain(ctx, sshChain(bastion), 5)
			require.NoError(t, err)
			assert.Equal(t, []string{"relay.internal", "bastion.example"}, hopHosts(layers))
		})

		convey.Convey("上游的上游逐级展开", func() {
			edge := createHopAsset(t, ctx, "edge.internal", 0, "")
			relay := createHopAsset(t, ctx, "relay.internal", 0, sshChainJSON(edge))
			bastion := createHopAsset(t, ctx, "bastion.example", 0, sshChainJSON(relay))

			layers, err := Default().ResolveProxyChain(ctx, sshChain(bastion), 5)
			require.NoError(t, err)
			assert.Equal(t, []string{"edge.internal", "relay.internal", "bastion.example"}, hopHosts(layers))
		})

		convey.Convey("展开到深度上限那一层、且它没有上游时不算过深", func() {
			a := createHopAsset(t, ctx, "a.internal", 0, "")
			b := createHopAsset(t, ctx, "b.internal", 0, sshChainJSON(a))
			c := createHopAsset(t, ctx, "c.internal", 0, sshChainJSON(b))
			d := createHopAsset(t, ctx, "d.internal", 0, sshChainJSON(c))
			e := createHopAsset(t, ctx, "e.internal", 0, sshChainJSON(d))

			layers, err := Default().ResolveProxyChain(ctx, sshChain(e), 5)
			require.NoError(t, err)
			assert.Equal(t, []string{"a.internal", "b.internal", "c.internal", "d.internal", "e.internal"}, hopHosts(layers))
		})

		convey.Convey("第二跳起由前一跳到达，不再套用它自己的连接路径", func() {
			relay := createHopAsset(t, ctx, "relay.internal", 0, "")
			other := createHopAsset(t, ctx, "other.internal", 0, "")
			bastion := createHopAsset(t, ctx, "bastion.example", 0, sshChainJSON(relay))

			layers, err := Default().ResolveProxyChain(ctx, sshChain(other, bastion), 5)
			require.NoError(t, err)
			assert.Equal(t, []string{"other.internal", "bastion.example"}, hopHosts(layers))
		})

		convey.Convey("互相引用的连接路径报错而不是无限展开", func() {
			a := createHopAsset(t, ctx, "a.internal", 0, "")
			b := createHopAsset(t, ctx, "b.internal", 0, sshChainJSON(a))
			stored, err := asset_repo.Asset().Find(ctx, a)
			require.NoError(t, err)
			stored.SSHTunnelID = b
			require.NoError(t, asset_repo.Asset().Update(ctx, stored))

			_, err = Default().ResolveProxyChain(ctx, sshChain(a), 5)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "循环引用")
		})
	})
}

func TestResolveSSHConnectConfigJumpHostUpstream(t *testing.T) {
	convey.Convey("SSH 资产经跳板机连接时，跳板机自己的上游也在路径里", t, func() {
		ctx := setupAgentDialTest(t)
		relay := createHopAsset(t, ctx, "relay.internal", 0, "")
		bastion := createHopAsset(t, ctx, "bastion.example", 0, sshChainJSON(relay))
		target := createHopAsset(t, ctx, "target.internal", bastion, "")

		_, _, _, _, _, layers, err := Default().ResolveSSHConnectConfig(ctx, target)
		require.NoError(t, err)
		assert.Equal(t, []string{"relay.internal", "bastion.example"}, hopHosts(layers))
	})
}

// silentHop 接受连接但从不应答：能连上、握手却没有下文的一跳。
func silentHop(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func TestResolvedProxyChainGivesUpOnStalledHop(t *testing.T) {
	convey.Convey("解析出的代理链按连接超时设置放弃没有应答的一跳，调用方 ctx 没有期限也一样", t, func() {
		ctx := setupAgentDialTest(t)
		previous := sshtuning.Get()
		t.Cleanup(func() { sshtuning.Set(previous) })
		tuned := previous
		tuned.DialTimeout = 150 * time.Millisecond
		sshtuning.Set(tuned)

		dial := func(chain *asset_entity.ProxyChainConfig) error {
			layers, err := Default().ResolveProxyChain(ctx, chain, 5)
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() {
				conn, err := proxychain.Chain{Layers: layers}.Dial(context.Background(), "10.0.0.1:9200")
				if conn != nil {
					_ = conn.Close()
				}
				done <- err
			}()
			select {
			case err := <-done:
				return err
			case <-time.After(3 * time.Second):
				t.Fatal("拨号在 3s 内没有返回")
				return nil
			}
		}

		convey.Convey("SSH 层", func() {
			host, port := silentHop(t)
			hop := createHopAssetAt(t, ctx, host, port, 0, "")
			assert.ErrorIs(t, dial(sshChain(hop)), context.DeadlineExceeded)
		})

		convey.Convey("SOCKS5 层", func() {
			host, port := silentHop(t)
			chain := &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{
				{ID: "l1", Type: asset_entity.ProxyChainLayerSOCKS5, Order: 1, Host: host, Port: port},
			}}
			assert.ErrorIs(t, dial(chain), context.DeadlineExceeded)
		})
	})
}
