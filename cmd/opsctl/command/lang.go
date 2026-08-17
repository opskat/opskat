package command

import (
	"strings"
)

// resolvePolicyLang 从标准 locale 环境变量解析策略消息语言：
// LC_ALL → LC_MESSAGES → LANG 依次取第一个非空值，取语言前缀映射到
// zh-cn / en；C、POSIX 与无法识别的值落 en。
func resolvePolicyLang(lcAll, lcMessages, lang string) string {
	v := lcAll
	if v == "" {
		v = lcMessages
	}
	if v == "" {
		v = lang
	}
	if v == "" {
		return "en"
	}
	if strings.EqualFold(v, "C") || strings.EqualFold(v, "POSIX") {
		return "en"
	}
	// 取语言前缀：去掉首个 _ / - / . / @ 之后的部分（如 zh_CN.UTF-8 → zh）
	prefix := v
	if i := strings.IndexAny(v, "_-.@"); i >= 0 {
		prefix = v[:i]
	}
	if strings.EqualFold(prefix, "zh") {
		return "zh-cn"
	}
	return "en"
}
