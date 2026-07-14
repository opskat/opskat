package vnc_svc

import (
	"context"
	"crypto/des" //nolint:gosec // VNC RFB authentication requires the protocol-mandated DES primitive.
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/proxychain"
)

// TestConfig dials the VNC target through the resolved proxy chain and runs the
// RFB authentication handshake to verify the host/port/password are reachable and
// correct. It never opens a session; callers use it purely for connectivity tests.
func (m *Manager) TestConfig(ctx context.Context, cfg *asset_entity.VNCConfig, password string) error {
	if strings.TrimSpace(cfg.Host) == "" {
		return fmt.Errorf("主机地址不能为空")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return fmt.Errorf("端口无效")
	}
	layers, err := m.resolver.ResolveProxyChain(ctx, cfg.ProxyChain, 5)
	if err != nil {
		return err
	}
	conn, err := (proxychain.Chain{Layers: layers}).Dial(ctx, net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
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
		if password != "" {
			return fmt.Errorf("VNC 服务端未启用密码认证，无法验证当前密码")
		}
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
	hasNone := false
	hasPassword := false
	for _, typ := range types {
		if typ == 2 {
			hasPassword = true
		}
		if typ == 1 {
			hasNone = true
		}
	}
	if password != "" {
		if !hasPassword {
			return fmt.Errorf("VNC 服务端未启用密码认证，无法验证当前密码")
		}
		selected = 2
	} else if hasNone {
		selected = 1
	} else if hasPassword {
		selected = 2
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
