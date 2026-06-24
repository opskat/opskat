package system

import (
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/sshtuning"
)

// 连接超时 / 保活间隔的允许范围（秒）。范围校验在 IPC 边界做，避免把无意义的
// 值（如 0 秒超时、1 秒一次的心跳风暴）写进配置。
const (
	sshKeepAliveIntervalMinSeconds = 5
	sshKeepAliveIntervalMaxSeconds = 3600
	sshConnectTimeoutMinSeconds    = 5
	sshConnectTimeoutMaxSeconds    = 600
)

// SSHConnectionSettings 是前端可读写的 SSH/TCP 连接调优。返回值已填充默认，
// 所以 UI 永远拿到真实数值（60 / 30）而非代表"未设置"的 0。
type SSHConnectionSettings struct {
	TCPNoDelay               bool `json:"tcpNoDelay"`               // 禁用 Nagle (TCP_NODELAY)
	TCPKeepAlive             bool `json:"tcpKeepAlive"`             // 启用 SO_KEEPALIVE
	KeepAliveIntervalSeconds int  `json:"keepAliveIntervalSeconds"` // SSH 保活心跳间隔（秒）
	ConnectTimeoutSeconds    int  `json:"connectTimeoutSeconds"`    // 最大连接超时（秒）
}

// sshTuningFromConfig 把持久化配置（含三态/默认）翻译成运行期 sshtuning.Settings。
func sshTuningFromConfig(cfg *bootstrap.AppConfig) sshtuning.Settings {
	s := sshtuning.Default()
	if cfg == nil {
		return s
	}
	if cfg.SSHTCPNoDelay != nil {
		s.TCPNoDelay = *cfg.SSHTCPNoDelay
	}
	if cfg.SSHTCPKeepAlive != nil {
		s.TCPKeepAlive = *cfg.SSHTCPKeepAlive
	}
	if cfg.SSHKeepAliveIntervalSeconds > 0 {
		s.KeepAliveInterval = time.Duration(cfg.SSHKeepAliveIntervalSeconds) * time.Second
	}
	if cfg.SSHConnectTimeoutSeconds > 0 {
		s.DialTimeout = time.Duration(cfg.SSHConnectTimeoutSeconds) * time.Second
	}
	return s
}

// ApplySSHTuning 在启动时由 main.go 调用，把持久化配置注入全局连接调优，
// 使首个连接即采用用户配置。
func ApplySSHTuning(cfg *bootstrap.AppConfig) {
	sshtuning.Set(sshTuningFromConfig(cfg))
}

// GetSSHConnectionSettings 返回当前生效的连接调优（默认已填充）。
func (s *System) GetSSHConnectionSettings() SSHConnectionSettings {
	t := sshTuningFromConfig(bootstrap.GetConfig())
	return SSHConnectionSettings{
		TCPNoDelay:               t.TCPNoDelay,
		TCPKeepAlive:             t.TCPKeepAlive,
		KeepAliveIntervalSeconds: int(t.KeepAliveInterval / time.Second),
		ConnectTimeoutSeconds:    int(t.DialTimeout / time.Second),
	}
}

// SetSSHConnectionSettings 校验、持久化并立即应用连接调优。已建立的连接保持
// 其建立时的参数，新连接采用新值。
func (s *System) SetSSHConnectionSettings(in SSHConnectionSettings) error {
	if in.KeepAliveIntervalSeconds < sshKeepAliveIntervalMinSeconds || in.KeepAliveIntervalSeconds > sshKeepAliveIntervalMaxSeconds {
		return fmt.Errorf("SSH 保活间隔须在 %d-%d 秒之间", sshKeepAliveIntervalMinSeconds, sshKeepAliveIntervalMaxSeconds)
	}
	if in.ConnectTimeoutSeconds < sshConnectTimeoutMinSeconds || in.ConnectTimeoutSeconds > sshConnectTimeoutMaxSeconds {
		return fmt.Errorf("连接超时须在 %d-%d 秒之间", sshConnectTimeoutMinSeconds, sshConnectTimeoutMaxSeconds)
	}

	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}

	noDelay := in.TCPNoDelay
	keepAlive := in.TCPKeepAlive
	cfg.SSHTCPNoDelay = &noDelay
	cfg.SSHTCPKeepAlive = &keepAlive
	cfg.SSHKeepAliveIntervalSeconds = in.KeepAliveIntervalSeconds
	cfg.SSHConnectTimeoutSeconds = in.ConnectTimeoutSeconds

	if err := bootstrap.SaveConfig(cfg); err != nil {
		return err
	}
	ApplySSHTuning(cfg)
	return nil
}
