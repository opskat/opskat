package policy

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
)

// IsZh 判断 context 中的语言是否为中文，默认英文
func IsZh(ctx context.Context) bool {
	lang := aictx.GetPolicyLang(ctx)
	if lang == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(lang), "zh")
}

// PolicyMsg 根据 context 语言选择消息
func PolicyMsg(ctx context.Context, en, zh string) string {
	if IsZh(ctx) {
		return zh
	}
	return en
}

// PolicyFmt 根据 context 语言选择格式化消息
func PolicyFmt(ctx context.Context, enFmt, zhFmt string, args ...any) string {
	if IsZh(ctx) {
		return fmt.Sprintf(zhFmt, args...)
	}
	return fmt.Sprintf(enFmt, args...)
}
