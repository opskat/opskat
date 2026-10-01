package system

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// commandReviewConfigErrorEvent 在审核遇到配置错误（未配置 / API key 无效）时发给前端，
// 同一种错误只发一次，前端据此提醒用户去设置页检查。
const commandReviewConfigErrorEvent = "command-review:config-error"

// CommandReviewSettings 是设置页读到的模型审核设置（已套上默认值）。API key 只回是否已设置。
// LastFailReason / LastFailAt 是本次启动以来最近一次审核失败，没失败过则为空。
type CommandReviewSettings struct {
	APIKeySet      bool    `json:"apiKeySet"`
	BaseURL        string  `json:"baseUrl"`
	Model          string  `json:"model"`
	TimeoutMs      int     `json:"timeoutMs"`
	Threshold      float64 `json:"threshold"`
	LastFailReason string  `json:"lastFailReason"`
	LastFailAt     int64   `json:"lastFailAt"` // Unix 毫秒，没有失败时为 0
}

// CommandReviewSaveInput 是保存（或测试）模型审核设置的入参。数值填 0、地址和模型填空表示用默认值。
// 模型名由用户自己填，不做限制，是否可用由"测试模型"确认。
type CommandReviewSaveInput struct {
	APIKey      string  `json:"apiKey"` // 非空时替换已保存的 key
	ClearAPIKey bool    `json:"clearApiKey"`
	BaseURL     string  `json:"baseUrl"`
	Model       string  `json:"model"`
	TimeoutMs   int     `json:"timeoutMs"`
	Threshold   float64 `json:"threshold"`
}

// baseURL 是规范化后的服务地址：去掉首尾空白和末尾的 /，客户端再拼上 /v1/systemone。
func (in CommandReviewSaveInput) baseURL() string {
	return strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
}

func (in CommandReviewSaveInput) validate() error {
	if u := in.baseURL(); u != "" {
		parsed, err := url.Parse(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("服务地址（Base URL）需要是 http:// 或 https:// 开头的地址")
		}
	}
	if in.TimeoutMs < 1000 || in.TimeoutMs > 60000 {
		return fmt.Errorf("超时需在 1000~60000 毫秒之间")
	}
	if in.Threshold <= 0 || in.Threshold >= 1 {
		return fmt.Errorf("阈值需大于 0、小于 1")
	}
	return nil
}

// GetCommandReviewSettings 读取模型审核设置。
func (s *System) GetCommandReviewSettings() (*CommandReviewSettings, error) {
	cfg := command_review_svc.NewConfig(bootstrap.CommandReviewSettings())
	out := &CommandReviewSettings{
		// 保存过就算已设置，解不开也一样：审核会报"API key 无法读取"，重新填一遍即可覆盖。
		APIKeySet: bootstrap.GetConfig().CommandReviewAPIKey != "",
		BaseURL:   cfg.BaseURL,
		Model:     cfg.Model,
		TimeoutMs: int(cfg.Timeout.Milliseconds()),
		Threshold: cfg.Threshold,
	}
	if st := command_review_svc.Default().Status(); st.LastFailReason != "" {
		out.LastFailReason = st.LastFailReason
		out.LastFailAt = st.LastFailAt.UnixMilli()
	}
	return out, nil
}

// watchCommandReviewConfigErrors 把审核的配置错误转成前端事件。
func (s *System) watchCommandReviewConfigErrors() {
	command_review_svc.Default().SetConfigErrorListener(func(reason string) {
		logger.Ctx(s.ctx).Warn("command review config error", zap.String("reason", reason))
		wailsRuntime.EventsEmit(s.ctx, commandReviewConfigErrorEvent, reason)
	})
}

// SaveCommandReviewSettings 保存模型审核设置，API key 加密后落盘。
func (s *System) SaveCommandReviewSettings(in CommandReviewSaveInput) error {
	ctx := s.desktopCtx()
	if err := in.validate(); err != nil {
		return err
	}
	cfg := bootstrap.GetConfig()
	switch {
	case in.ClearAPIKey:
		cfg.CommandReviewAPIKey = ""
	case in.APIKey != "":
		encrypted, err := credential_svc.Default().Encrypt(strings.TrimSpace(in.APIKey))
		if err != nil {
			return fmt.Errorf("加密 API key 失败: %w", err)
		}
		cfg.CommandReviewAPIKey = encrypted
	}
	cfg.CommandReviewBaseURL = in.baseURL()
	cfg.CommandReviewModel = strings.TrimSpace(in.Model)
	cfg.CommandReviewTimeoutMs = in.TimeoutMs
	cfg.CommandReviewThreshold = in.Threshold
	if err := bootstrap.SaveConfig(cfg); err != nil {
		logger.Ctx(ctx).Error("save command review settings failed", zap.Error(err))
		return err
	}
	logger.Ctx(ctx).Info("command review settings saved",
		zap.Bool("apiKeySet", cfg.CommandReviewAPIKey != ""), zap.String("baseURL", cfg.CommandReviewBaseURL), zap.String("model", cfg.CommandReviewModel))
	return nil
}

// TestCommandReview 用设置页上填的值（不保存）发一次最小请求，检查地址、API key 和模型是否可用，
// 返回服务端实际作答的模型版本。API key 没重新填时用已保存的。
func (s *System) TestCommandReview(in CommandReviewSaveInput) (string, error) {
	ctx := s.desktopCtx()
	if err := in.validate(); err != nil {
		return "", err
	}
	apiKey := strings.TrimSpace(in.APIKey)
	if apiKey == "" && !in.ClearAPIKey {
		saved, err := bootstrap.CommandReviewAPIKey()
		if err != nil {
			logger.Ctx(ctx).Error("read saved command review api key failed", zap.Error(err))
			return "", fmt.Errorf("已保存的 API key 无法读取，请重新填写: %w", err)
		}
		apiKey = saved
	}
	cfg := command_review_svc.NewConfig(command_review_svc.Settings{
		APIKey:    apiKey,
		BaseURL:   in.baseURL(),
		Model:     strings.TrimSpace(in.Model),
		TimeoutMs: in.TimeoutMs,
		Threshold: in.Threshold,
	})
	logger.Ctx(ctx).Info("test command review model started", zap.String("baseURL", cfg.BaseURL), zap.String("model", cfg.Model))
	model, err := command_review_svc.Default().TestModel(ctx, cfg)
	if err != nil {
		logger.Ctx(ctx).Warn("test command review model failed", zap.String("baseURL", cfg.BaseURL), zap.String("model", cfg.Model), zap.Error(err))
		return "", err
	}
	logger.Ctx(ctx).Info("test command review model succeeded", zap.String("model", model))
	return model, nil
}
