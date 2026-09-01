package ssh_svc

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"

	"github.com/opskat/opskat/internal/service/host_key_svc"

	"golang.org/x/crypto/ssh"
)

// HostKeyAction 主机密钥校验操作
type HostKeyAction int

const (
	HostKeyAcceptAndSave HostKeyAction = iota // 接受并记住
	HostKeyAcceptOnce                         // 仅本次接受
	HostKeyReject                             // 取消/拒绝
)

// HostKeyEvent 主机密钥校验事件
type HostKeyEvent struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	KeyType        string `json:"keyType"`
	Fingerprint    string `json:"fingerprint"`
	IsChanged      bool   `json:"isChanged"`      // true=密钥已变更（危险）
	OldFingerprint string `json:"oldFingerprint"` // 变更时的旧指纹
}

// HostKeyVerifyFunc 主机密钥校验回调，由调用方实现不同的交互方式
type HostKeyVerifyFunc func(event HostKeyEvent) HostKeyAction

// MakeHostKeyCallback 创建 SSH HostKeyCallback
func MakeHostKeyCallback(host string, port int, verifyFn HostKeyVerifyFunc) ssh.HostKeyCallback {
	if verifyFn == nil {
		return ssh.InsecureIgnoreHostKey()
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		presented := host_key_svc.PresentedKey{
			Host:        host,
			Port:        port,
			KeyType:     key.Type(),
			PublicKey:   base64.StdEncoding.EncodeToString(key.Marshal()),
			Fingerprint: ssh.FingerprintSHA256(key),
		}
		ctx := context.Background()
		check, err := host_key_svc.HostKey().Check(ctx, presented)
		if err != nil {
			return fmt.Errorf("校验 SSH 主机密钥失败: %w", err)
		}
		if check.State == host_key_svc.CheckMatch {
			return nil
		}

		changed := check.State == host_key_svc.CheckChanged
		action := verifyFn(HostKeyEvent{
			Host:           host,
			Port:           port,
			KeyType:        presented.KeyType,
			Fingerprint:    presented.Fingerprint,
			IsChanged:      changed,
			OldFingerprint: check.OldFingerprint,
		})

		switch action {
		case HostKeyAcceptAndSave:
			if err := host_key_svc.HostKey().Trust(ctx, presented, changed); err != nil {
				return fmt.Errorf("保存 SSH 主机密钥失败: %w", err)
			}
			return nil
		case HostKeyAcceptOnce:
			return nil
		default:
			if changed {
				return fmt.Errorf("主机密钥已变更，连接被用户拒绝 (host=%s:%d)", host, port)
			}
			return fmt.Errorf("首次连接被用户拒绝 (host=%s:%d)", host, port)
		}
	}
}

// AutoTrustFirstRejectChangeVerifyFunc AI agent 使用：首次自动信任，变更拒绝
func AutoTrustFirstRejectChangeVerifyFunc() HostKeyVerifyFunc {
	return func(event HostKeyEvent) HostKeyAction {
		if event.IsChanged {
			return HostKeyReject
		}
		return HostKeyAcceptAndSave
	}
}
