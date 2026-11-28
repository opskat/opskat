package system

import (
	"fmt"
	"strconv"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/wailsapp/wails/v2/pkg/options"
)

// SetWindowBackground 由前端在主题解析结果变化时调用，传入当前页面背景色（#rrggbb）。
// 立即应用到窗口，并存进配置供下次启动在网页绘制前就用上：Wails 未设底色时是不透明白色，
// 窗口出现到首帧之间的 1~2 秒会是一块白屏，暗色主题下尤其刺眼。
func (s *System) SetWindowBackground(hex string) error {
	c, ok := parseHexColor(hex)
	if !ok {
		return fmt.Errorf("%s: %q", i18n.Pick(s.Lang(), "窗口背景色格式无效，应为 #rrggbb", "Invalid window background color, expected #rrggbb"), hex)
	}
	if s.ctx != nil {
		windowOps.SetBackgroundColour(s.ctx, c.R, c.G, c.B, c.A)
	}
	cfg := bootstrap.GetConfig()
	if cfg.WindowBackground == hex {
		return nil
	}
	cfg.WindowBackground = hex
	return bootstrap.SaveConfig(cfg)
}

// WindowBackgroundColor 返回配置里记下的窗口底色；没记过时返回 nil，由 Wails 用默认底色。
func WindowBackgroundColor(cfg *bootstrap.AppConfig) *options.RGBA {
	if cfg == nil {
		return nil
	}
	c, ok := parseHexColor(cfg.WindowBackground)
	if !ok {
		return nil
	}
	return c
}

func parseHexColor(hex string) (*options.RGBA, bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return nil, false
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return nil, false
	}
	return &options.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, true
}
