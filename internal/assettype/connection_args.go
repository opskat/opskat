package assettype

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// 连接字段：一个资产"怎么连过去"的那部分自动化参数——SSH 隧道、代理链、TLS。
// 它们在所有类型里同名同义（内置类型各自存进自己的 config，扩展类型存进宿主保留键），
// 所以解析、互斥校验、加密与审批摘要只在这里写一遍，各 handler 调用，不各自再实现。
const (
	argSSHAsset   = "ssh_asset_id"
	argProxyChain = "proxy_chain"
	argTLS        = "tls"
	argTLSInsec   = "tls_insecure"
	argTLSServer  = "tls_server_name"
	argTLSKeyPEM  = "tls_key_pem"
)

// tlsCertArg 是一份证书材料的两种给法：本机路径，或 PEM 内容。三份材料（CA、客户端
// 证书、客户端私钥）共用一个来源——资产表单只有一个来源开关，一个混着两种来源的资产它
// 显示不了，保存时会丢掉没显示的那一半。
type tlsCertArg struct{ file, pem string }

var tlsCertArgs = []tlsCertArg{
	{"tls_ca_file", "tls_ca_pem"},
	{"tls_cert_file", "tls_cert_pem"},
	{"tls_key_file", argTLSKeyPEM},
}

// tlsConfigArgs 返回一个类型接受的全部 TLS 字段；tlsApprovalArgs 是其中可进审批视图的
// 部分——客户端私钥内容是密文字段，write-only。
func tlsConfigArgs() []string {
	out := make([]string, 0, 3+2*len(tlsCertArgs))
	out = append(out, argTLS, argTLSInsec, argTLSServer)
	for _, cert := range tlsCertArgs {
		out = append(out, cert.file, cert.pem)
	}
	return out
}

func tlsApprovalArgs() []string {
	out := make([]string, 0, len(tlsConfigArgs()))
	for _, field := range tlsConfigArgs() {
		if field != argTLSKeyPEM {
			out = append(out, field)
		}
	}
	return out
}

// withFields 返回 base 追加 extra 后的新切片，不改 base。
func withFields(base []string, extra ...string) []string {
	return append(append([]string(nil), base...), extra...)
}

// validateConnectionArgs 校验一次请求里连接字段之间的关系：代理链的形状，代理链与
// 单条 SSH 隧道二选一，同一份证书的路径与内容二选一。字段是否被该类型接受已由
// rejectUnknownFields 判过，这里只管"接受了的字段彼此是否矛盾"。
func validateConnectionArgs(args map[string]any) error {
	chain, _, err := proxyChainArg(args)
	if err != nil {
		return err
	}
	if chain != nil && ArgInt64(args, argSSHAsset) != 0 {
		return fmt.Errorf("%s and %s are mutually exclusive: a proxy chain replaces the single SSH tunnel", argProxyChain, argSSHAsset)
	}
	var files, pems []string
	for _, cert := range tlsCertArgs {
		if ArgString(args, cert.file) != "" {
			files = append(files, cert.file)
		}
		if ArgString(args, cert.pem) != "" {
			pems = append(pems, cert.pem)
		}
	}
	if len(files) > 0 && len(pems) > 0 {
		return fmt.Errorf("TLS certificates are given either all as paths or all as content, not a mix: got %s and %s",
			strings.Join(files, ", "), strings.Join(pems, ", "))
	}
	return nil
}

// proxyChainLayerFields 是每种层接受的字段（type / name 之外）。
var proxyChainLayerFields = map[string][]string{
	asset_entity.ProxyChainLayerSSH:        {argSSHAsset},
	asset_entity.ProxyChainLayerSOCKS5:     {"host", "port", "username", "password"},
	asset_entity.ProxyChainLayerHTTPTunnel: {"url", "token", "timeout_seconds"},
}

var proxyChainLayerNames = map[string]string{
	asset_entity.ProxyChainLayerSSH:        "SSH Layer",
	asset_entity.ProxyChainLayerSOCKS5:     "SOCKS5 Proxy",
	asset_entity.ProxyChainLayerHTTPTunnel: "HTTP Tunnel",
}

// httpTunnelDefaultTimeoutSeconds 与资产表单新建 HTTP 隧道层时写入的值一致。
const httpTunnelDefaultTimeoutSeconds = 10

// proxyChainArg 解析 proxy_chain：一个有序的层数组，第一项离本机最近。
// 返回的链带明文密钥（socks5 password / http_tunnel token），只用于校验、摘要与
// applyProxyChainArg 的加密落库。supplied=false 表示请求没带这个字段；带了但为空数组
// 返回 (nil, true, nil)，意为清空。
func proxyChainArg(args map[string]any) (*asset_entity.ProxyChainConfig, bool, error) {
	raw, ok := args[argProxyChain]
	if !ok {
		return nil, false, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, true, fmt.Errorf("%s must be a list of layers", argProxyChain)
	}
	if len(items) == 0 {
		return nil, true, nil
	}
	enabled := true
	layers := make([]asset_entity.ProxyChainLayer, 0, len(items))
	for i, item := range items {
		fields, ok := item.(map[string]any)
		if !ok {
			return nil, true, fmt.Errorf("%s layer %d must be an object", argProxyChain, i+1)
		}
		layerType := ArgString(fields, "type")
		accepted, ok := proxyChainLayerFields[layerType]
		if !ok {
			return nil, true, fmt.Errorf("%s layer %d: unsupported type %q (ssh, socks5, http_tunnel)", argProxyChain, i+1, layerType)
		}
		if err := rejectUnknownFields(fields, withFields(accepted, "type", "name")); err != nil {
			return nil, true, fmt.Errorf("%s layer %d (%s): %w", argProxyChain, i+1, layerType, err)
		}
		layer := asset_entity.ProxyChainLayer{
			ID:         fmt.Sprintf("layer-%d", i+1),
			Name:       ArgString(fields, "name"),
			Enabled:    &enabled,
			Type:       layerType,
			Order:      i + 1,
			SSHAssetID: ArgInt64(fields, argSSHAsset),
			Host:       strings.TrimSpace(ArgString(fields, "host")),
			Port:       ArgInt(fields, "port"),
			Username:   ArgString(fields, "username"),
			Password:   ArgString(fields, "password"),
			URL:        strings.TrimSpace(ArgString(fields, "url")),
			Token:      ArgString(fields, "token"),
		}
		if layer.Name == "" {
			layer.Name = proxyChainLayerNames[layerType]
		}
		if layerType == asset_entity.ProxyChainLayerHTTPTunnel {
			layer.TimeoutSeconds = ArgInt(fields, "timeout_seconds")
			if layer.TimeoutSeconds <= 0 {
				layer.TimeoutSeconds = httpTunnelDefaultTimeoutSeconds
			}
		}
		layers = append(layers, layer)
	}
	chain := &asset_entity.ProxyChainConfig{Layers: layers}
	if err := asset_entity.ValidateProxyChain(chain); err != nil {
		return nil, true, fmt.Errorf("invalid %s: %w", argProxyChain, err)
	}
	return chain, true, nil
}

// ProxyChainHasSecret 报告 config 的 proxy_chain 里是否带了明文密钥（socks5 password /
// http_tunnel token）。opsctl 用它决定要不要提示"明文出现在命令行/文件里"。
func ProxyChainHasSecret(config map[string]any) bool {
	chain, _, err := proxyChainArg(config)
	if err != nil || chain == nil {
		return false
	}
	for _, layer := range chain.Layers {
		if layer.Password != "" || layer.Token != "" {
			return true
		}
	}
	return false
}

// ProxyChainSummary 把一条链渲染成每层一行的审批摘要：只有类型与目标，不含密码、Token，
// HTTP 隧道 URL 去掉 userinfo / query / fragment（它们可能夹带凭据）。sshName 把 SSH 层的
// 资产 ID 换成可读名称；传 nil 或返回空串时只显示 ID。
func ProxyChainSummary(chain *asset_entity.ProxyChainConfig, sshName func(id int64) string) []string {
	if chain == nil {
		return []string{}
	}
	out := make([]string, 0, len(chain.Layers))
	for _, layer := range chain.Layers {
		var target string
		switch layer.Type {
		case asset_entity.ProxyChainLayerSSH:
			target = fmt.Sprintf("asset #%d", layer.SSHAssetID)
			if sshName != nil {
				if name := sshName(layer.SSHAssetID); name != "" {
					target = fmt.Sprintf("%s (#%d)", name, layer.SSHAssetID)
				}
			}
		case asset_entity.ProxyChainLayerSOCKS5:
			target = fmt.Sprintf("%s:%d", layer.Host, layer.Port)
			if layer.Username != "" {
				target = layer.Username + "@" + target
			}
		case asset_entity.ProxyChainLayerHTTPTunnel:
			target = redactedURL(layer.URL)
		}
		out = append(out, layer.Type+": "+target)
	}
	return out
}

func redactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// ProxyChainSSHAssetIDs 返回链里 SSH 层引用的资产 ID（去重、升序），供写入前核对它们存在。
func ProxyChainSSHAssetIDs(chain *asset_entity.ProxyChainConfig) []int64 {
	if chain == nil {
		return nil
	}
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(chain.Layers))
	for _, layer := range chain.Layers {
		if layer.Type != asset_entity.ProxyChainLayerSSH {
			continue
		}
		if _, ok := seen[layer.SSHAssetID]; ok {
			continue
		}
		seen[layer.SSHAssetID] = struct{}{}
		out = append(out, layer.SSHAssetID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// applyProxyChainArg 把请求里的连接路径落到一个类型自己的存储位上：
//   - 带了 proxy_chain：加密层密钥后整条替换 *chain（空数组即清空），并清掉单条 SSH 隧道
//     ——资产的 SSHTunnelID 列与该类型自己另存的隧道 ID（tunnelIDs）；
//   - 没带 proxy_chain 但带了 ssh_asset_id：清掉 *chain，单条隧道由 handler 自己写入。
//
// 两条路径后写的生效：EffectiveProxyChain 在链非空时无视隧道 ID，留着旧的那个只会让
// 读配置的人误判实际走的是哪条路。
func applyProxyChainArg(a *asset_entity.Asset, args map[string]any, chain **asset_entity.ProxyChainConfig, tunnelIDs ...*int64) error {
	parsed, supplied, err := proxyChainArg(args)
	if err != nil {
		return err
	}
	if !supplied {
		if _, ok := args[argSSHAsset]; ok {
			*chain = nil
		}
		return nil
	}
	if parsed == nil {
		*chain = nil
		return nil
	}
	for i := range parsed.Layers {
		layer := &parsed.Layers[i]
		if layer.Password, err = encryptArg(layer.Password); err != nil {
			return fmt.Errorf("encrypt %s layer %d password: %w", argProxyChain, i+1, err)
		}
		if layer.Token, err = encryptArg(layer.Token); err != nil {
			return fmt.Errorf("encrypt %s layer %d token: %w", argProxyChain, i+1, err)
		}
	}
	*chain = parsed
	a.SSHTunnelID = 0
	for _, id := range tunnelIDs {
		*id = 0
	}
	return nil
}

func encryptArg(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	return credential_svc.Default().Encrypt(plaintext)
}

// tlsCertStore 指向一个类型存 TLS 证书材料的六个位置。
type tlsCertStore struct {
	caFile, certFile, keyFile *string
	caPEM, certPEM, keyPEM    *string
}

// applyTLSCertArgs 把请求里出现的证书字段写进 store。没出现的材料不动；显式给空串即清掉
// 那一份。请求用了哪种来源（路径 / 内容），另一种来源已存的三份材料就整组清掉——来源是
// 三份共用的，切换即替换。客户端私钥内容加密落库。
func applyTLSCertArgs(args map[string]any, store tlsCertStore) error {
	files := []*string{store.caFile, store.certFile, store.keyFile}
	pems := []*string{store.caPEM, store.certPEM, store.keyPEM}
	var fileGiven, pemGiven bool
	for i, cert := range tlsCertArgs {
		if _, ok := args[cert.file]; ok {
			fileGiven = true
			*files[i] = ArgString(args, cert.file)
		}
		if _, ok := args[cert.pem]; ok {
			pemGiven = true
			content := ArgString(args, cert.pem)
			if cert.pem == argTLSKeyPEM {
				encrypted, err := encryptArg(content)
				if err != nil {
					return fmt.Errorf("encrypt %s: %w", argTLSKeyPEM, err)
				}
				content = encrypted
			}
			*pems[i] = content
		}
	}
	// validateConnectionArgs 已拒绝两种来源同时带值的请求，所以这里至多清一组。
	if fileGiven && !pemGiven {
		clearStrings(pems)
	}
	if pemGiven && !fileGiven {
		clearStrings(files)
	}
	return nil
}

func clearStrings(slots []*string) {
	for _, slot := range slots {
		*slot = ""
	}
}

// FieldDoc 是一个 config 字段在 help 文档字段表里的一行。
type FieldDoc struct {
	Name, Type, Notes string
}

// ConnectionFieldDocs 返回声明了 conn 这些连接项的类型所接受的宿主连接字段的文档行。
// 字段名取自本文件解析用的同一批常量，help 因此列不出一个实际不被接受的字段。
func ConnectionFieldDocs(conn ExtensionConnectionSpec) []FieldDoc {
	var docs []FieldDoc
	if conn.SSHTunnel {
		notes := "SSH asset to tunnel through; 0 detaches"
		if conn.ProxyChain {
			notes += ". Replaces a stored `proxy_chain`"
		}
		docs = append(docs, FieldDoc{argSSHAsset, "number", notes})
	}
	if conn.ProxyChain {
		docs = append(docs, FieldDoc{argProxyChain, "array", proxyChainDoc})
	}
	if conn.TLS {
		docs = append(docs,
			FieldDoc{argTLS, "bool", "`true` to enable the TLS settings below; they are ignored while it is off"},
			FieldDoc{argTLSInsec, "bool", "`true` to skip TLS certificate verification"},
			FieldDoc{argTLSServer, "string", "TLS SNI / server name override — the name the server certificate is checked against"},
			FieldDoc{"tls_ca_file", "string", "Path to a CA certificate file on this machine. " + tlsCertSourceDoc},
			FieldDoc{"tls_cert_file", "string", "Path to a client certificate file (mTLS)"},
			FieldDoc{"tls_key_file", "string", "Path to a client key file (mTLS)"},
			FieldDoc{"tls_ca_pem", "string", "CA certificate as PEM content, stored in the asset"},
			FieldDoc{"tls_cert_pem", "string", "Client certificate as PEM content (mTLS)"},
			FieldDoc{argTLSKeyPEM, "string", "**Write-only.** Client key as PEM content (mTLS), encrypted in the asset"},
		)
	}
	return docs
}

const tlsCertSourceDoc = "The three certificates share one source: give them all as paths (`tls_*_file`) or all as content (`tls_*_pem`); switching source drops what was stored in the other"

const proxyChainDoc = "Ordered list of hops, nearest to this machine first; it replaces the whole stored chain and the single SSH tunnel, `[]` clears it. " +
	"Layers: `{\"type\":\"ssh\",\"ssh_asset_id\":N}`, " +
	"`{\"type\":\"socks5\",\"host\":\"...\",\"port\":N,\"username\":\"...\",\"password\":\"...\"}`, " +
	"`{\"type\":\"http_tunnel\",\"url\":\"https://...\",\"token\":\"...\",\"timeout_seconds\":N}` (first layer only). " +
	"`password` and `token` are write-only and encrypted in the asset. Not together with `ssh_asset_id`"
