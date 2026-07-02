package system

import (
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/sshtuning"
)

// 保活间隔的允许范围（秒）。范围校验在 IPC 边界做，避免把无意义的值（如 1 秒一次
// 的心跳风暴、或长到失去保活意义的间隔）写进配置。
const (
	sshKeepAliveIntervalMinSeconds = 5
	sshKeepAliveIntervalMaxSeconds = 3600
)

// SSHConnectionSettings 是前端可读写的 SSH 空闲保活全局默认。TCP_NODELAY /
// SO_KEEPALIVE / 连接超时不再暴露给用户（恒采用内置默认），故此处只有保活间隔。
// 返回值已填充默认，所以 UI 永远拿到真实数值（30）而非代表"未设置"的 0。
type SSHConnectionSettings struct {
	KeepAliveIntervalSeconds int `json:"keepAliveIntervalSeconds"` // SSH 空闲保活心跳间隔（秒）
}

// sshTuningFromConfig 把持久化配置翻译成运行期 sshtuning.Settings。只有保活间隔
// 可配；其余项恒取内置默认。
func sshTuningFromConfig(cfg *bootstrap.AppConfig) sshtuning.Settings {
	s := sshtuning.Default()
	if cfg != nil && cfg.SSHKeepAliveIntervalSeconds > 0 {
		s.KeepAliveInterval = time.Duration(cfg.SSHKeepAliveIntervalSeconds) * time.Second
	}
	return s
}

// ApplySSHTuning 在启动时由 main.go 调用，把持久化配置注入全局连接调优，
// 使首个连接即采用用户配置。
func ApplySSHTuning(cfg *bootstrap.AppConfig) {
	sshtuning.Set(sshTuningFromConfig(cfg))
}

// GetSSHConnectionSettings 返回当前生效的保活全局默认（默认已填充）。
func (s *System) GetSSHConnectionSettings() SSHConnectionSettings {
	t := sshTuningFromConfig(bootstrap.GetConfig())
	return SSHConnectionSettings{
		KeepAliveIntervalSeconds: int(t.KeepAliveInterval / time.Second),
	}
}

// SetSSHConnectionSettings 校验、持久化并立即应用保活全局默认。已建立的连接保持
// 其建立时的参数，新连接采用新值。
func (s *System) SetSSHConnectionSettings(in SSHConnectionSettings) error {
	if in.KeepAliveIntervalSeconds < sshKeepAliveIntervalMinSeconds || in.KeepAliveIntervalSeconds > sshKeepAliveIntervalMaxSeconds {
		return fmt.Errorf("SSH 保活间隔须在 %d-%d 秒之间", sshKeepAliveIntervalMinSeconds, sshKeepAliveIntervalMaxSeconds)
	}

	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	cfg.SSHKeepAliveIntervalSeconds = in.KeepAliveIntervalSeconds

	if err := bootstrap.SaveConfig(cfg); err != nil {
		return err
	}
	ApplySSHTuning(cfg)
	return nil
}
