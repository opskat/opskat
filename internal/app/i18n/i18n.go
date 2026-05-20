// Package i18n 提供 binder 共用的语言上下文工具。
//
// 取代了旧 *App.langCtx() 和 *App.pickMsg()，让任何 binder 持有 lang 字段后
// 都能复用同一套规范，把 cago i18n + ai PolicyLang 一次注入到 context。
package i18n

import (
	"context"
	"strings"

	cagoi18n "github.com/cago-frame/cago/pkg/i18n"
	"github.com/opskat/opskat/internal/ai/aictx"
)

// Ctx 把 lang 同时绑定到 cago i18n 与 ai policy 两套 context key。
// 每个 binder 在调用 service 前调用一次：service.Foo(i18n.Ctx(b.ctx, b.lang.Lang()), ...)
func Ctx(ctx context.Context, lang string) context.Context {
	ctx = cagoi18n.WithLanguage(ctx, lang)
	ctx = aictx.WithPolicyLang(ctx, lang)
	return ctx
}

// Pick 按当前语言挑中英文消息。给那些不走 cago i18n 注册表、
// 但又会展示给用户的小段文案（连接进度、绑定层错误等）用。
func Pick(lang, zh, en string) string {
	if strings.HasPrefix(strings.ToLower(lang), "zh") {
		return zh
	}
	return en
}
