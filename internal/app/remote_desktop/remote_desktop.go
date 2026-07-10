package remote_desktop

import (
	"context"
	"crypto/des" //nolint:gosec // VNC RFB authentication requires the protocol-mandated DES primitive.
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/remote_desktop_svc"
	"golang.org/x/text/encoding/simplifiedchinese"
)

type LangProvider interface {
	Lang() string
}

type RemoteDesktop struct {
	ctx     context.Context
	lang    LangProvider
	manager *remote_desktop_svc.Manager
}

func New(appCtx context.Context, lang LangProvider, manager *remote_desktop_svc.Manager) *RemoteDesktop {
	if manager == nil {
		manager = remote_desktop_svc.NewManager(nil)
	}
	r := &RemoteDesktop{ctx: appCtx, lang: lang, manager: manager}
	conntest.Register("vnc", r.testVNCConnection)
	conntest.Register("rdp", r.testRDPConnection)
	return r
}

func (r *RemoteDesktop) Startup(ctx context.Context) {
	r.ctx = ctx
}

func (r *RemoteDesktop) Cleanup() {
	r.manager.Cleanup()
	conntest.Unregister("vnc")
	conntest.Unregister("rdp")
}

func (r *RemoteDesktop) ConnectRemoteDesktop(assetID int64) (*remote_desktop_svc.Session, error) {
	return r.manager.Connect(r.ctx, assetID, remote_desktop_svc.ConnectOptions{})
}

func (r *RemoteDesktop) DisconnectRemoteDesktop(sessionID string) {
	r.manager.Disconnect(sessionID)
}

func (r *RemoteDesktop) EncodeVNCClipboardText(text string) ([]int, error) {
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("VNC 剪贴板文本无法使用 GBK 编码: %w", err)
	}
	result := make([]int, len(encoded))
	for i, value := range encoded {
		result[i] = int(value)
	}
	return result, nil
}

func (r *RemoteDesktop) TestRemoteDesktopConnection(assetType, configJSON string) error {
	if assetType == "vnc" {
		return r.testVNCConnection(r.ctx, configJSON, "")
	}
	return r.testRDPConnection(r.ctx, configJSON, "")
}

func (r *RemoteDesktop) testRDPConnection(ctx context.Context, configJSON, _ string) error {
	var raw map[string]any
	if err := json.Unmarshal([]byte(configJSON), &raw); err != nil {
		return fmt.Errorf("远程桌面配置无效: %w", err)
	}
	host, _ := raw["host"].(string)
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("主机地址不能为空")
	}
	port := intFromAny(raw["port"])
	if port <= 0 || port > 65535 {
		return fmt.Errorf("端口无效")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func (r *RemoteDesktop) testVNCConnection(ctx context.Context, configJSON, plainPassword string) error {
	var cfg asset_entity.VNCConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("VNC配置无效: %w", err)
	}
	if strings.TrimSpace(cfg.Host) == "" {
		return fmt.Errorf("主机地址不能为空")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return fmt.Errorf("端口无效")
	}
	password := plainPassword
	if password == "" {
		resolved, err := credential_resolver.Default().ResolvePasswordGeneric(ctx, &cfg)
		if err != nil {
			return err
		}
		password = resolved
	}
	layers, err := credential_resolver.Default().ResolveProxyChain(ctx, cfg.ProxyChain, 5)
	if err != nil {
		return err
	}
	conn, err := (proxychain.Chain{Layers: layers}).Dial(ctx, net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port)))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return verifyVNCAuthContext(ctx, conn, password)
}

func verifyVNCAuthContext(ctx context.Context, conn net.Conn, password string) error {
	stopClose := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopClose()

	err := verifyVNCAuth(conn, password)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("VNC 握手超时或已取消: %w", ctxErr)
	}
	return err
}

func verifyVNCAuth(conn net.Conn, password string) error {
	version := make([]byte, 12)
	if _, err := io.ReadFull(conn, version); err != nil {
		return fmt.Errorf("读取 VNC 协议版本失败: %w", err)
	}
	clientVersion, modern, err := negotiateVNCVersion(string(version))
	if err != nil {
		return err
	}
	if _, err := conn.Write([]byte(clientVersion)); err != nil {
		return fmt.Errorf("发送 VNC 协议版本失败: %w", err)
	}
	if !modern {
		return verifyVNCAuth33(conn, password)
	}
	return verifyVNCAuth38(conn, password)
}

func negotiateVNCVersion(serverVersion string) (clientVersion string, modern bool, err error) {
	switch serverVersion {
	case "RFB 003.003\n", "RFB 003.006\n":
		return "RFB 003.003\n", false, nil
	case "RFB 003.007\n":
		return "RFB 003.007\n", true, nil
	case "RFB 003.008\n", "RFB 003.889\n", "RFB 004.000\n", "RFB 004.001\n", "RFB 005.000\n":
		return "RFB 003.008\n", true, nil
	default:
		if !strings.HasPrefix(serverVersion, "RFB ") {
			return "", false, fmt.Errorf("目标不是 VNC/RFB 服务")
		}
		return "", false, fmt.Errorf("不支持的 VNC 协议版本: %q", strings.TrimSpace(serverVersion))
	}
}

func verifyVNCAuth33(conn net.Conn, password string) error {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fmt.Errorf("读取 VNC 安全类型失败: %w", err)
	}
	securityType := binary.BigEndian.Uint32(buf)
	switch securityType {
	case 0:
		return readVNCSecurityFailure(conn)
	case 1:
		return sendVNCClientInit(conn)
	case 2:
		return verifyVNCPasswordAuth(conn, password)
	default:
		return fmt.Errorf("不支持的 VNC 安全类型: %d", securityType)
	}
}

func verifyVNCAuth38(conn net.Conn, password string) error {
	count := []byte{0}
	if _, err := io.ReadFull(conn, count); err != nil {
		return fmt.Errorf("读取 VNC 安全类型失败: %w", err)
	}
	if count[0] == 0 {
		return readVNCSecurityFailure(conn)
	}
	types := make([]byte, int(count[0]))
	if _, err := io.ReadFull(conn, types); err != nil {
		return fmt.Errorf("读取 VNC 安全类型列表失败: %w", err)
	}
	selected := byte(0)
	for _, typ := range types {
		if typ == 2 {
			selected = 2
			break
		}
		if typ == 1 && selected == 0 {
			selected = 1
		}
	}
	if selected == 0 {
		return fmt.Errorf("不支持的 VNC 安全类型: %v", types)
	}
	if _, err := conn.Write([]byte{selected}); err != nil {
		return fmt.Errorf("选择 VNC 安全类型失败: %w", err)
	}
	if selected == 2 {
		return verifyVNCPasswordAuth(conn, password)
	}
	if err := readVNCSecurityResult(conn); err != nil {
		return err
	}
	return sendVNCClientInit(conn)
}

func verifyVNCPasswordAuth(conn net.Conn, password string) error {
	if password == "" {
		return fmt.Errorf("VNC 需要密码，请在资产中配置密码或凭据")
	}
	challenge := make([]byte, 16)
	if _, err := io.ReadFull(conn, challenge); err != nil {
		return fmt.Errorf("读取 VNC 认证挑战失败: %w", err)
	}
	response, err := vncPasswordResponse(password, challenge)
	if err != nil {
		return err
	}
	if _, err := conn.Write(response); err != nil {
		return fmt.Errorf("发送 VNC 认证响应失败: %w", err)
	}
	if err := readVNCSecurityResult(conn); err != nil {
		return err
	}
	return sendVNCClientInit(conn)
}

func readVNCSecurityResult(conn net.Conn) error {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fmt.Errorf("读取 VNC 认证结果失败: %w", err)
	}
	if binary.BigEndian.Uint32(buf) == 0 {
		return nil
	}
	reason, err := readVNCReason(conn)
	if err != nil || reason == "" {
		return fmt.Errorf("VNC 认证失败")
	}
	return fmt.Errorf("VNC 认证失败: %s", reason)
}

func readVNCSecurityFailure(conn net.Conn) error {
	reason, err := readVNCReason(conn)
	if err != nil {
		return fmt.Errorf("VNC 服务拒绝连接")
	}
	return fmt.Errorf("VNC 服务拒绝连接: %s", reason)
}

func readVNCReason(conn net.Conn) (string, error) {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint32(buf)
	if n == 0 || n > 4096 {
		return "", nil
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(conn, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func sendVNCClientInit(conn net.Conn) error {
	if _, err := conn.Write([]byte{1}); err != nil {
		return fmt.Errorf("发送 VNC ClientInit 失败: %w", err)
	}
	return nil
}

func vncPasswordResponse(password string, challenge []byte) ([]byte, error) {
	key := make([]byte, 8)
	copy(key, []byte(password))
	for i := range key {
		key[i] = reverseBits(key[i])
	}
	block, err := des.NewCipher(key) //nolint:gosec // VNC authentication requires DES for RFB compatibility.
	if err != nil {
		return nil, fmt.Errorf("初始化 VNC 认证失败: %w", err)
	}
	response := make([]byte, len(challenge))
	for i := 0; i < len(challenge); i += block.BlockSize() {
		block.Encrypt(response[i:i+block.BlockSize()], challenge[i:i+block.BlockSize()])
	}
	return response, nil
}

func reverseBits(b byte) byte {
	b = (b&0xf0)>>4 | (b&0x0f)<<4
	b = (b&0xcc)>>2 | (b&0x33)<<2
	return (b&0xaa)>>1 | (b&0x55)<<1
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	default:
		return 0
	}
}
