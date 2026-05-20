// Package aictx 提供 internal/ai 各子包共享的 context 键、决策原语与 slot 协议。
//
// 本包没有任何 internal/ai/* 依赖，是 ai 体系的最底层公共类型。其他子包
// (audit/policy/permission/helper/tool/runner) 都向上依赖本包。
package aictx

import "context"

type (
	auditSourceKey    struct{}
	conversationIDKey struct{}
	grantSessionIDKey struct{}
	sessionIDKey      struct{}
	policyLangKey     struct{}
)

// WithAuditSource 注入审计来源
func WithAuditSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, auditSourceKey{}, source)
}

// GetAuditSource 获取审计来源
func GetAuditSource(ctx context.Context) string {
	if v, ok := ctx.Value(auditSourceKey{}).(string); ok {
		return v
	}
	return ""
}

// WithConversationID 注入会话 ID
func WithConversationID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, conversationIDKey{}, id)
}

// GetConversationID 获取会话 ID
func GetConversationID(ctx context.Context) int64 {
	if v, ok := ctx.Value(conversationIDKey{}).(int64); ok {
		return v
	}
	return 0
}

// WithGrantSessionID 注入授权会话 ID
func WithGrantSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, grantSessionIDKey{}, id)
}

// GetGrantSessionID 获取授权会话 ID
func GetGrantSessionID(ctx context.Context) string {
	if v, ok := ctx.Value(grantSessionIDKey{}).(string); ok {
		return v
	}
	return ""
}

// WithSessionID 注入会话 ID（opsctl session 或 AI session）
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, id)
}

// GetSessionID 获取会话 ID
func GetSessionID(ctx context.Context) string {
	if v, ok := ctx.Value(sessionIDKey{}).(string); ok {
		return v
	}
	return ""
}

// WithPolicyLang 设置策略消息的语言（"zh-cn", "en" 等）
func WithPolicyLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, policyLangKey{}, lang)
}

// GetPolicyLang 获取策略消息的语言
func GetPolicyLang(ctx context.Context) string {
	if v, ok := ctx.Value(policyLangKey{}).(string); ok {
		return v
	}
	return ""
}
