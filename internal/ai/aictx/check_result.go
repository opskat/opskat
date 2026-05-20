package aictx

import "context"

// checkResultKey 是 audit middleware 挂的 *CheckResult slot 的 key。
// 跨包共享：tool handler 通过 RecordDecision(ctx, r) 写决策；audit middleware
// 在 c.Next() 返回后用 GetCheckResult(ctx) 读取该槽落审计。
// 声明为带名空结构体（按 Go 推荐做法）以避免 string key 冲突。
type checkResultKey struct{}

// WithCheckResultSlot 把 slot 挂到 ctx 上，供后续 RecordDecision 写入。
// 通常由 audit middleware 在 c.Next() 之前调用。
func WithCheckResultSlot(ctx context.Context, slot *CheckResult) context.Context {
	return context.WithValue(ctx, checkResultKey{}, slot)
}

// RecordDecision 在工具 handler 中写入决策结果，供 audit middleware 读取。
// 没有 slot（如 opsctl 直调 handler 路径）时为 no-op。
func RecordDecision(ctx context.Context, result CheckResult) {
	if slot, ok := ctx.Value(checkResultKey{}).(*CheckResult); ok && slot != nil {
		*slot = result
	}
}

// GetCheckResult 读取当前 ctx 上挂的 *CheckResult slot 值。
// 返回 nil 表示未挂 slot（opsctl 直调路径）。
func GetCheckResult(ctx context.Context) *CheckResult {
	if slot, ok := ctx.Value(checkResultKey{}).(*CheckResult); ok {
		return slot
	}
	return nil
}
