package system

import (
	"fmt"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/pkg/extwire"
)

// commandReviewConfigErrorEvent 在审核遇到设置问题（没有审核扩展、扩展没配置好等）时发给前端，
// 同一种问题只发一次，前端据此提醒用户去设置页检查。
const commandReviewConfigErrorEvent = "command-review:config-error"

// commandReviewStatusEvent 在审核状态变化（出现失败或失败已恢复）时发给前端，载荷是 CommandReviewStatus，
// 设置页打开着时据此实时更新「最近一次未恢复的失败」。
const commandReviewStatusEvent = "command-review:status"

// 超时的取值范围，与设置页的校验一致（frontend/src/components/settings/CommandReviewSection.tsx）。
// 上限就是审核器工具声明的时限（extwire.MaxReviewTimeout）：再长的设置会被工具时限截断。
const (
	commandReviewTimeoutMin = 1000
	commandReviewTimeoutMax = int(extwire.MaxReviewTimeout / time.Millisecond)
)

// CommandReviewStatus 是本次启动以来最近一次还没恢复的审核失败。
type CommandReviewStatus struct {
	LastFailReason string `json:"lastFailReason"`
	LastFailAt     int64  `json:"lastFailAt"` // Unix 毫秒，没有失败时为 0
}

func newCommandReviewStatus(st command_review_svc.Status) CommandReviewStatus {
	if st.LastFailReason == "" {
		return CommandReviewStatus{}
	}
	return CommandReviewStatus{LastFailReason: st.LastFailReason, LastFailAt: st.LastFailAt.UnixMilli()}
}

// CommandReviewSettings 是设置页读到的、宿主侧的审核设置。具体审核服务的设置（API key、模型等）
// 属于审核扩展，在扩展设置里。LastFailReason / LastFailAt 是本次启动以来最近一次还没恢复的审核失败。
type CommandReviewSettings struct {
	TimeoutMs      int    `json:"timeoutMs"`
	LastFailReason string `json:"lastFailReason"`
	LastFailAt     int64  `json:"lastFailAt"` // Unix 毫秒，没有失败时为 0
}

// GetCommandReviewSettings 读取宿主侧的审核设置。
func (s *System) GetCommandReviewSettings() (*CommandReviewSettings, error) {
	st := newCommandReviewStatus(command_review_svc.Default().Status())
	return &CommandReviewSettings{
		TimeoutMs:      int(bootstrap.CommandReviewTimeout().Milliseconds()),
		LastFailReason: st.LastFailReason,
		LastFailAt:     st.LastFailAt,
	}, nil
}

// validateCommandReviewTimeout 检查超时设置，单位毫秒。
func validateCommandReviewTimeout(ms int) error {
	if ms < commandReviewTimeoutMin || ms > commandReviewTimeoutMax {
		return fmt.Errorf("超时需在 %d~%d 毫秒之间", commandReviewTimeoutMin, commandReviewTimeoutMax)
	}
	return nil
}

// SaveCommandReviewTimeout 保存每次调用审核扩展的超时。
func (s *System) SaveCommandReviewTimeout(timeoutMs int) error {
	ctx := s.desktopCtx()
	if err := validateCommandReviewTimeout(timeoutMs); err != nil {
		return err
	}
	cfg := bootstrap.GetConfig()
	cfg.CommandReviewTimeoutMs = timeoutMs
	if err := bootstrap.SaveConfig(cfg); err != nil {
		logger.Ctx(ctx).Error("save command review timeout failed", zap.Error(err))
		return err
	}
	logger.Ctx(ctx).Info("command review timeout saved", zap.Int("timeoutMs", timeoutMs))
	return nil
}

// watchCommandReviewConfigErrors 把审核的设置问题和状态变化转成前端事件。
func (s *System) watchCommandReviewConfigErrors() {
	command_review_svc.Default().SetStatusListener(func(st command_review_svc.Status) {
		wailsRuntime.EventsEmit(s.ctx, commandReviewStatusEvent, newCommandReviewStatus(st))
	})
	command_review_svc.Default().SetConfigErrorListener(func(reason string) {
		logger.Ctx(s.ctx).Warn("command review config error", zap.String("reason", reason))
		wailsRuntime.EventsEmit(s.ctx, commandReviewConfigErrorEvent, reason)
	})
}
