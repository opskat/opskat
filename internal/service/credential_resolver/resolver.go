package credential_resolver

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/pkg/sshtuning"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/ssh_agent_svc"
	"github.com/opskat/opskat/internal/service/ssh_svc"
	"github.com/opskat/opskat/internal/sshagent"
)

// Resolver 统一凭据解析服务
type Resolver struct{}

var defaultResolver = &Resolver{}

// Default 获取默认 Resolver 实例
func Default() *Resolver {
	return defaultResolver
}

// ResolveProxyChain 把资产配置里的代理链解析成可拨号的各层（凭据已解密），顺序是离本机
// 最近的一跳在前。
//
// 第一跳如果是 SSH 资产，要从本机拨出去，所以先展开那台资产自己的连接路径（它的代理链，
// 或 SSH 隧道 / SOCKS5 字段）再接上它本身：一台机器怎么到达只在它自己身上配一次，引用它
// 的资产都跟着走。第二跳起由前一跳到达，链上写的就是完整路径，不再套用它们自己的路径。
// maxDepth 限制展开的层数，连接路径互相引用时报错。
func (r *Resolver) ResolveProxyChain(ctx context.Context, chain *asset_entity.ProxyChainConfig, maxDepth int) ([]proxychain.Layer, error) {
	if maxDepth <= 0 {
		return nil, fmt.Errorf("代理链过深，可能存在循环引用")
	}
	chain = asset_entity.NormalizeProxyChain(chain)
	if chain == nil {
		return nil, nil
	}
	if err := asset_entity.ValidateProxyChain(chain); err != nil {
		return nil, err
	}
	layers := append([]asset_entity.ProxyChainLayer(nil), chain.Layers...)
	sort.SliceStable(layers, func(i, j int) bool {
		if layers[i].Order == layers[j].Order {
			return i < j
		}
		return layers[i].Order < layers[j].Order
	})
	// 链上的 SSH 握手、通道打开和 SOCKS5 握手本身都没有时限；经 http.Transport 拨号时
	// ctx 也不带期限，卡住的一跳会一直占着连接。
	stepTimeout := sshtuning.Get().DialTimeoutOrDefault()
	resolved := make([]proxychain.Layer, 0, len(layers))
	for i, layer := range layers {
		switch layer.Type {
		case asset_entity.ProxyChainLayerSSH:
			asset, err := asset_svc.Asset().Get(ctx, layer.SSHAssetID)
			if err != nil {
				return nil, fmt.Errorf("代理链 SSH 资产不存在(ID=%d): %w", layer.SSHAssetID, err)
			}
			cfg, err := asset.GetSSHConfig()
			if err != nil {
				return nil, err
			}
			if own := sshAssetChain(asset, cfg); i == 0 && own != nil {
				upstream, err := r.ResolveProxyChain(ctx, own, maxDepth-1)
				if err != nil {
					return nil, fmt.Errorf("解析 SSH 资产 %q 自身的连接路径失败: %w", asset.Name, err)
				}
				logger.Ctx(ctx).Debug("proxy chain first hop reached through its own connection path",
					zap.Int64("assetID", asset.ID), zap.Int("upstreamHops", len(upstream)))
				resolved = append(resolved, upstream...)
			}
			hop := ssh_svc.ConnectConfig{
				Host:              cfg.Host,
				Port:              cfg.Port,
				Username:          cfg.Username,
				AuthType:          cfg.AuthType,
				HostKeyVerifyFunc: ssh_svc.AutoTrustFirstRejectChangeVerifyFunc(),
			}
			if cfg.AuthType == asset_entity.AuthTypeAgent {
				// Agent 层不解析密码/密钥材料，经同一认证工厂在真正拨号时完成握手。
				if hop.Agent, err = r.ResolveAgentAuthConfig(cfg); err != nil {
					return nil, err
				}
			} else {
				password, key, passphrase, err := r.ResolveSSHCredentials(ctx, cfg)
				if err != nil {
					return nil, fmt.Errorf("解析代理链 SSH 凭据失败: %w", err)
				}
				hop.Password, hop.Key, hop.KeyPassphrase, hop.PrivateKeys = password, key, passphrase, cfg.PrivateKeys
			}
			// 跳板要第二因子时与目标一样交给 ctx 里的应答方，挑战以跳板资产为对象。
			var newMFA func() sshagent.InteractiveCaller
			if newCaller := mfaCallerFrom(ctx); newCaller != nil {
				hopID := asset.ID
				newMFA = func() sshagent.InteractiveCaller { return newCaller(hopID) }
			}
			handshake, err := ssh_svc.ProxyChainHandshake(hop, newMFA)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, proxychain.Layer{
				Type:             proxychain.LayerSSH,
				Name:             layer.Name,
				Host:             cfg.Host,
				Port:             cfg.Port,
				Username:         cfg.Username,
				Handshake:        handshake,
				EstablishTimeout: stepTimeout,
			})
		case asset_entity.ProxyChainLayerSOCKS5:
			cp := asset_entity.ProxyConfig{
				Type: "socks5", Host: layer.Host, Port: layer.Port, Username: layer.Username, Password: layer.Password,
			}
			proxy := r.DecryptProxyPassword(&cp)
			resolved = append(resolved, proxychain.Layer{
				Type: proxychain.LayerSOCKS5, Name: layer.Name, Host: proxy.Host, Port: proxy.Port,
				Username: proxy.Username, Password: proxy.Password,
				EstablishTimeout: stepTimeout,
			})
		case asset_entity.ProxyChainLayerHTTPTunnel:
			resolved = append(resolved, proxychain.Layer{
				Type: proxychain.LayerHTTPTunnel, Name: layer.Name, URL: layer.URL,
				Token: r.decryptInlineSecret(layer.Token), TimeoutSeconds: layer.TimeoutSeconds,
			})
		}
	}
	return resolved, nil
}

// sshAssetChain 是到达一台 SSH 资产要走的连接路径：它自己的代理链，或还没迁成代理链的
// SSH 隧道 / SOCKS5 代理字段。
func sshAssetChain(asset *asset_entity.Asset, cfg *asset_entity.SSHConfig) *asset_entity.ProxyChainConfig {
	jumpHostID := asset.SSHTunnelID
	if jumpHostID == 0 {
		jumpHostID = cfg.JumpHostID // backward compat
	}
	return asset_entity.EffectiveProxyChain(cfg.ProxyChain, jumpHostID, cfg.Proxy)
}

// ResolveSSHCredentials 从 SSHConfig 解析明文密码/密钥/passphrase
// 优先使用统一凭证，向后兼容内联密码和本地密钥文件
func (r *Resolver) ResolveSSHCredentials(ctx context.Context, cfg *asset_entity.SSHConfig) (password, key, passphrase string, err error) {
	// 优先使用统一凭证
	if cfg.CredentialID > 0 {
		cred, err := credential_mgr_svc.Get(ctx, cfg.CredentialID)
		if err != nil {
			return "", "", "", fmt.Errorf("获取凭证失败: %w", err)
		}
		switch cred.Type {
		case credential_entity.TypePassword:
			decrypted, err := credential_svc.Default().Decrypt(cred.Password)
			if err != nil {
				return "", "", "", fmt.Errorf("解密密码失败: %w", err)
			}
			return decrypted, "", "", nil
		case credential_entity.TypeSSHKey:
			privKey, err := credential_mgr_svc.GetDecryptedPrivateKey(ctx, cfg.CredentialID)
			if err != nil {
				return "", "", "", fmt.Errorf("获取密钥失败: %w", err)
			}
			passphrase, err := credential_mgr_svc.GetDecryptedPassphrase(ctx, cfg.CredentialID)
			if err != nil {
				return "", "", "", fmt.Errorf("获取 passphrase 失败: %w", err)
			}
			return "", privKey, passphrase, nil
		}
	}
	// 向后兼容：内联密码
	if cfg.AuthType == "password" && cfg.Password != "" {
		decrypted, err := credential_svc.Default().Decrypt(cfg.Password)
		if err != nil {
			return "", "", "", fmt.Errorf("解密密码失败: %w", err)
		}
		return decrypted, "", "", nil
	}
	// 向后兼容：本地密钥文件
	if cfg.AuthType == "key" && len(cfg.PrivateKeys) > 0 {
		data, err := os.ReadFile(cfg.PrivateKeys[0])
		if err != nil {
			return "", "", "", fmt.Errorf("读取私钥文件失败: %w", err)
		}
		// 解密本地密钥的 passphrase
		var passphrase string
		if cfg.PrivateKeyPassphrase != "" {
			passphrase, err = credential_svc.Default().Decrypt(cfg.PrivateKeyPassphrase)
			if err != nil {
				return "", "", "", fmt.Errorf("解密私钥密码失败: %w", err)
			}
		}
		return "", string(data), passphrase, nil
	}
	return "", "", "", nil
}

// ResolveAgentAuthConfig 从 SSHConfig 构建 Agent 认证配置（auth_type=agent 时）。
// 来源（sourceID → 端点）在真正拨号 / 握手发生时解析，绝不提前打开 Agent 传输；
// 也绝不解析出密码/密钥材料。非 Agent 认证返回 nil。MFA 由调用方按交互性设置。
func (r *Resolver) ResolveAgentAuthConfig(sshCfg *asset_entity.SSHConfig) (*ssh_svc.AgentConfig, error) {
	if sshCfg == nil || sshCfg.AuthType != asset_entity.AuthTypeAgent {
		return nil, nil
	}
	if sshCfg.AgentSourceID <= 0 {
		return nil, fmt.Errorf("SSH Agent 认证缺少来源 ID")
	}
	sourceID := sshCfg.AgentSourceID
	return &ssh_svc.AgentConfig{
		Source:      r.agentSource(sourceID),
		Fingerprint: sshCfg.AgentKeyFingerprint,
	}, nil
}

// ResolveAgentForwardConfig builds the lazy source resolver for SSH Agent
// forwarding. Forwarding deliberately has no fingerprint: the remote host uses
// the selected local Agent as an agent protocol endpoint.
func (r *Resolver) ResolveAgentForwardConfig(sshCfg *asset_entity.SSHConfig) (*ssh_svc.AgentForwardConfig, error) {
	if sshCfg == nil || !sshCfg.AgentForwarding {
		return nil, nil
	}
	if sshCfg.AgentForwardSourceID <= 0 {
		return nil, fmt.Errorf("SSH Agent 转发缺少来源 ID")
	}
	return &ssh_svc.AgentForwardConfig{Source: r.agentSource(sshCfg.AgentForwardSourceID)}, nil
}

func (r *Resolver) agentSource(sourceID int64) func(context.Context) (sshagent.Source, error) {
	return func(ctx context.Context) (sshagent.Source, error) {
		src, err := ssh_agent_svc.Get(ctx, sourceID)
		if err != nil {
			return sshagent.Source{}, err
		}
		return sshagent.Source{
			Type:  sshagent.EndpointType(src.EndpointType),
			Value: src.Endpoint,
		}, nil
	}
}

// ResolveJumpHosts 递归解析跳板机链（含凭据解密），返回从第一跳到最后一跳的顺序
func (r *Resolver) ResolveJumpHosts(ctx context.Context, jumpHostID int64, maxDepth int) ([]ssh_svc.JumpHostEntry, error) {
	if maxDepth <= 0 {
		return nil, fmt.Errorf("跳板机链过深，可能存在循环引用")
	}

	jumpAsset, err := asset_svc.Asset().Get(ctx, jumpHostID)
	if err != nil {
		return nil, fmt.Errorf("跳板机资产不存在(ID=%d): %w", jumpHostID, err)
	}
	jumpCfg, err := jumpAsset.GetSSHConfig()
	if err != nil {
		return nil, err
	}

	// 解析跳板机凭证
	password, key, passphrase, err := r.ResolveSSHCredentials(ctx, jumpCfg)
	if err != nil {
		return nil, fmt.Errorf("解析跳板机凭据失败: %w", err)
	}

	entry := ssh_svc.JumpHostEntry{
		Host:       jumpCfg.Host,
		Port:       jumpCfg.Port,
		Username:   jumpCfg.Username,
		AuthType:   jumpCfg.AuthType,
		Password:   password,
		Key:        key,
		Passphrase: passphrase,
	}
	if jumpCfg.AuthType == asset_entity.AuthTypeAgent {
		agentCfg, err := r.ResolveAgentAuthConfig(jumpCfg)
		if err != nil {
			return nil, fmt.Errorf("解析跳板机 Agent 认证配置失败: %w", err)
		}
		entry.Agent = agentCfg
	}

	nextJumpID := jumpAsset.SSHTunnelID
	if nextJumpID == 0 {
		nextJumpID = jumpCfg.JumpHostID // backward compat
	}
	if nextJumpID > 0 {
		parentHosts, err := r.ResolveJumpHosts(ctx, nextJumpID, maxDepth-1)
		if err != nil {
			return nil, err
		}
		return append(parentHosts, entry), nil
	}

	return []ssh_svc.JumpHostEntry{entry}, nil
}

// PasswordSource 密码来源接口。
type PasswordSource interface {
	GetCredentialID() int64
	GetPassword() string
}

// ResolvePasswordGeneric 为 assettype handler 解析托管或内联密码。
func (r *Resolver) ResolvePasswordGeneric(ctx context.Context, src PasswordSource) (string, error) {
	if src.GetCredentialID() > 0 {
		password, err := credential_mgr_svc.GetDecryptedPassword(ctx, src.GetCredentialID())
		if err != nil {
			return "", fmt.Errorf("获取凭证失败: %w", err)
		}
		return password, nil
	}
	if src.GetPassword() == "" {
		return "", nil
	}
	decrypted, err := credential_svc.Default().Decrypt(src.GetPassword())
	if err != nil {
		return "", fmt.Errorf("解密密码失败: %w", err)
	}
	return decrypted, nil
}

// ResolveDatabasePassword 解密 DatabaseConfig 中的密码
// 优先使用统一凭证，向后兼容内联密码
func (r *Resolver) ResolveDatabasePassword(ctx context.Context, cfg *asset_entity.DatabaseConfig) (string, error) {
	if cfg.CredentialID > 0 {
		password, err := credential_mgr_svc.GetDecryptedPassword(ctx, cfg.CredentialID)
		if err != nil {
			return "", fmt.Errorf("获取数据库凭证失败: %w", err)
		}
		return password, nil
	}
	if cfg.Password == "" {
		return "", nil
	}
	decrypted, err := credential_svc.Default().Decrypt(cfg.Password)
	if err != nil {
		return "", fmt.Errorf("解密数据库密码失败: %w", err)
	}
	return decrypted, nil
}

// ResolveRedisPassword 解密 RedisConfig 中的密码
// 优先使用统一凭证，向后兼容内联密码
func (r *Resolver) ResolveRedisPassword(ctx context.Context, cfg *asset_entity.RedisConfig) (string, error) {
	if cfg.CredentialID > 0 {
		password, err := credential_mgr_svc.GetDecryptedPassword(ctx, cfg.CredentialID)
		if err != nil {
			return "", fmt.Errorf("获取 Redis 凭证失败: %w", err)
		}
		return password, nil
	}
	if cfg.Password == "" {
		return "", nil
	}
	decrypted, err := credential_svc.Default().Decrypt(cfg.Password)
	if err != nil {
		return "", fmt.Errorf("解密 Redis 密码失败: %w", err)
	}
	return decrypted, nil
}

// ResolveRedisSentinelPassword 解密哨兵认证密码。哨兵密码只支持直接输入(加密保存),
// 与数据节点的密码/托管凭据相互独立。
func (r *Resolver) ResolveRedisSentinelPassword(cfg *asset_entity.RedisConfig) (string, error) {
	if cfg.SentinelPassword == "" {
		return "", nil
	}
	decrypted, err := credential_svc.Default().Decrypt(cfg.SentinelPassword)
	if err != nil {
		return "", fmt.Errorf("解密 Redis 哨兵密码失败: %w", err)
	}
	return decrypted, nil
}

// ResolveMongoDBPassword 解密 MongoDBConfig 中的密码
// 优先使用统一凭证，向后兼容内联密码
func (r *Resolver) ResolveMongoDBPassword(ctx context.Context, cfg *asset_entity.MongoDBConfig) (string, error) {
	if cfg.CredentialID > 0 {
		password, err := credential_mgr_svc.GetDecryptedPassword(ctx, cfg.CredentialID)
		if err != nil {
			return "", fmt.Errorf("获取 MongoDB 凭证失败: %w", err)
		}
		return password, nil
	}
	if cfg.Password == "" {
		return "", nil
	}
	decrypted, err := credential_svc.Default().Decrypt(cfg.Password)
	if err != nil {
		return "", fmt.Errorf("解密 MongoDB 密码失败: %w", err)
	}
	return decrypted, nil
}

// DecryptProxyPassword 解密代理配置中的密码，返回新的 ProxyConfig（不修改原始对象）
func (r *Resolver) DecryptProxyPassword(proxy *asset_entity.ProxyConfig) *asset_entity.ProxyConfig {
	if proxy == nil || proxy.Password == "" {
		return proxy
	}
	decrypted, err := credential_svc.Default().Decrypt(proxy.Password)
	if err != nil {
		// 解密失败，可能是明文（测试连接场景），原样返回
		return proxy
	}
	cp := *proxy
	cp.Password = decrypted
	return &cp
}

func (r *Resolver) decryptInlineSecret(value string) string {
	if value == "" {
		return ""
	}
	decrypted, err := credential_svc.Default().Decrypt(value)
	if err != nil {
		return value
	}
	return decrypted
}

// ResolveSSHConnectConfig 从资产 ID 解析完整的 SSH 连接信息
// 返回 SSHConfig、明文密码、明文密钥、passphrase、跳板机链
func (r *Resolver) ResolveSSHConnectConfig(ctx context.Context, assetID int64) (*asset_entity.SSHConfig, string, string, string, []ssh_svc.JumpHostEntry, []proxychain.Layer, error) {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil, "", "", "", nil, nil, fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsSSH() {
		return nil, "", "", "", nil, nil, fmt.Errorf("资产不是SSH类型")
	}
	sshCfg, err := asset.GetSSHConfig()
	if err != nil {
		return nil, "", "", "", nil, nil, fmt.Errorf("获取SSH配置失败: %w", err)
	}
	password, key, passphrase, err := r.ResolveSSHCredentials(ctx, sshCfg)
	if err != nil {
		return nil, "", "", "", nil, nil, fmt.Errorf("解析凭据失败: %w", err)
	}

	var jumpHosts []ssh_svc.JumpHostEntry
	jumpHostID := asset.SSHTunnelID
	if jumpHostID == 0 {
		jumpHostID = sshCfg.JumpHostID // backward compat
	}
	effectiveChain := asset_entity.EffectiveProxyChain(sshCfg.ProxyChain, jumpHostID, sshCfg.Proxy)
	proxyChain, err := r.ResolveProxyChain(ctx, effectiveChain, 5)
	if err != nil {
		return nil, "", "", "", nil, nil, fmt.Errorf("解析代理链失败: %w", err)
	}
	if len(proxyChain) == 0 && jumpHostID > 0 {
		jumpHosts, err = r.ResolveJumpHosts(ctx, jumpHostID, 5)
		if err != nil {
			return nil, "", "", "", nil, nil, fmt.Errorf("解析跳板机失败: %w", err)
		}
	}

	return sshCfg, password, key, passphrase, jumpHosts, proxyChain, nil
}

// DialAssetSSH 一站式解析资产并建立 SSH 连接，自动处理代理密码解密、跳板机链。
// 除返回的 *ssh.Client 外，调用方还须负责关闭返回的所有额外 closer（跳板机链的
// 中间连接等）；否则会泄漏连接。失败时返回的 closer 列表为 nil。ctx 经 WithMFA 携带
// 应答方时，keyboard-interactive / Agent MFA 挑战交给它；否则保持非交互。
func (r *Resolver) DialAssetSSH(ctx context.Context, assetID int64) (*ssh.Client, []io.Closer, error) {
	sshCfg, password, key, passphrase, jumpHosts, proxyChain, err := r.ResolveSSHConnectConfig(ctx, assetID)
	if err != nil {
		return nil, nil, err
	}
	agentCfg, err := r.ResolveAgentAuthConfig(sshCfg)
	if err != nil {
		return nil, nil, err
	}
	var mfa sshagent.InteractiveCaller
	if newCaller := mfaCallerFrom(ctx); newCaller != nil {
		mfa = newCaller(assetID)
		if agentCfg != nil {
			agentCfg.MFA = mfa
		}
	}
	cfg := ssh_svc.ConnectConfig{
		Host:                     sshCfg.Host,
		Port:                     sshCfg.Port,
		Username:                 sshCfg.Username,
		AuthType:                 sshCfg.AuthType,
		Password:                 password,
		Key:                      key,
		KeyPassphrase:            passphrase,
		PrivateKeys:              sshCfg.PrivateKeys,
		AssetID:                  assetID,
		Ctx:                      ctx,
		Agent:                    agentCfg,
		MFA:                      mfa,
		Proxy:                    r.DecryptProxyPassword(sshCfg.Proxy),
		JumpHosts:                jumpHosts,
		ProxyChain:               proxyChain,
		HostKeyVerifyFunc:        ssh_svc.AutoTrustFirstRejectChangeVerifyFunc(),
		KeepAliveIntervalSeconds: sshCfg.KeepAliveIntervalSeconds,
	}
	return ssh_svc.NewManager().Dial(cfg)
}

type mfaKeyType struct{}

// WithMFA 让经 ctx 发起的 DialAssetSSH 与 ResolveProxyChain 把 keyboard-interactive /
// Agent MFA 挑战交给应答方（opsctl）：目标资产的挑战，以及连接路径上每台 SSH 跳板的挑战。
// newCaller 每次新建连接（每台主机的每次握手）调用一次，传入被挑战的那台资产（目标或
// 跳板），使应答方可以持有「本连接内」的状态。
func WithMFA(ctx context.Context, newCaller func(assetID int64) sshagent.InteractiveCaller) context.Context {
	return context.WithValue(ctx, mfaKeyType{}, newCaller)
}

// mfaCallerFrom 取 WithMFA 登记的应答方；没有登记时返回 nil（非交互）。
func mfaCallerFrom(ctx context.Context) func(assetID int64) sshagent.InteractiveCaller {
	newCaller, _ := ctx.Value(mfaKeyType{}).(func(assetID int64) sshagent.InteractiveCaller)
	return newCaller
}
