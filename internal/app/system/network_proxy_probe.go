package system

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/service/testreg"
	"go.uber.org/zap"
)

// NetworkProxyTestResult 是「测试连接」的结果。
type NetworkProxyTestResult struct {
	// OK 表示收到了目标的 HTTP 响应（任何状态码都算连通）。
	OK bool `json:"ok"`
	// Via 为所经代理的 scheme://host:port（不含凭据），直连时为 "direct"。
	Via string `json:"via"`
	// Host 为目标的 host[:port]。
	Host string `json:"host"`
	// Status 为 HTTP 状态码；没有收到响应时为 0。
	Status    int   `json:"status"`
	ElapsedMs int64 `json:"elapsedMs"`
	// Reason 为失败原因，OK 时为空：proxy_unreachable | proxy_auth | dns | tls | timeout | other。
	Reason string `json:"reason"`
	// Detail 为失败时的原始错误文本。
	Detail string `json:"detail"`
}

// TestNetworkProxy 用前端表单里当前（可能未保存）的设置 in 访问 targetURL，测试能否连通；不保存、不应用设置。
// 密码留空时沿用已保存的密码。testID 配合 CancelTest 中断，被取消时返回 context.Canceled。
// 设置不合法或 targetURL 不是 http(s) URL 时返回中英文错误，不发请求。
func (s *System) TestNetworkProxy(testID string, in NetworkProxyInput, targetURL string) (*NetworkProxyTestResult, error) {
	settings, err := networkProxySettingsFromInput(in, bootstrap.GetConfig())
	if err != nil {
		return nil, err
	}
	if err := s.validateNetworkProxy(settings); err != nil {
		return nil, err
	}
	target, err := url.Parse(targetURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, fmt.Errorf("%s", i18n.Pick(s.Lang(), "测试地址须为 http:// 或 https:// 开头的完整 URL",
			"The test URL must be a full URL starting with http:// or https://"))
	}

	ctx, release := testreg.Begin(i18n.Ctx(s.ctx, s.Lang()), testID)
	defer release()
	log := logger.Ctx(ctx).With(zap.String("testID", testID), zap.String("mode", string(settings.Mode)), zap.String("targetHost", target.Host))
	log.Info("network proxy test started")
	r := netproxy.Probe(ctx, settings, target)
	if errors.Is(ctx.Err(), context.Canceled) {
		log.Info("network proxy test canceled")
		return nil, ctx.Err()
	}
	log.Info("network proxy test finished",
		zap.Bool("ok", r.OK), zap.String("via", r.Via), zap.Int("status", r.Status),
		zap.Duration("elapsed", r.Elapsed), zap.String("reason", string(r.Reason)), zap.String("detail", r.Detail))
	return &NetworkProxyTestResult{
		OK:        r.OK,
		Via:       r.Via,
		Host:      r.Host,
		Status:    r.Status,
		ElapsedMs: r.Elapsed.Milliseconds(),
		Reason:    string(r.Reason),
		Detail:    r.Detail,
	}, nil
}
