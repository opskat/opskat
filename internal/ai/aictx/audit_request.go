package aictx

import "context"

// auditRequestKey 是 audit middleware 挂的审计 request 投影 slot 的 key。
// 跨包共享：producer（put_asset）通过 RecordAuditRequest 写投影；runner 的
// auditMiddleware 在 c.Next() 返回后读取该槽，用投影覆盖原始参数落审计。
// 声明为带名空结构体（按 Go 推荐做法）以避免 string key 冲突。
type auditRequestKey struct{}

// AuditRequestSlot 是一次工具调用可写的审计 request 投影槽。
//
// 默认 Audit writer 是 raw-by-default（Task 7）：没有 override 的工具，审计记录的就是
// writer 收到的原始 command/request/result/error。只有明确拥有 write-only 字段契约的
// producer（put_asset）设置投影——把字段白名单（复用 asset_put_svc.Prepared 的
// SafeAuditArgs/SafeAuditArgsForResult 与 assettype.AutomationContract.ApprovalFields）
// 交给 runner，让 write-only 字段整体缺席而不是被 `<redacted>` 占位。
//
// 该 override 只影响 Audit 落库，绝不成为执行、审批、ToolBlock 或会话输入：handler
// 仍把原始 config 交给 asset_put_svc，投影只写入这个独立槽。
type AuditRequestSlot struct {
	args map[string]any
}

// NewAuditRequestSlot 创建投影槽。由 runner 的 auditMiddleware 在 c.Next() 之前安装。
func NewAuditRequestSlot() *AuditRequestSlot {
	return &AuditRequestSlot{}
}

// WithAuditRequestSlot 把投影槽挂到 ctx 上，供后续 RecordAuditRequest 写入。
func WithAuditRequestSlot(ctx context.Context, slot *AuditRequestSlot) context.Context {
	return context.WithValue(ctx, auditRequestKey{}, slot)
}

// RecordAuditRequest 在工具 handler 中写入审计 request 投影，供 audit middleware 读取。
// 没有 slot（如 opsctl 直调 handler 路径——它自己直接写投影）时为 no-op。
func RecordAuditRequest(ctx context.Context, args map[string]any) {
	if slot, ok := ctx.Value(auditRequestKey{}).(*AuditRequestSlot); ok && slot != nil {
		slot.args = args
	}
}

// GetAuditRequest 读取当前工具调用记录的审计 request 投影；未记录（无 override）返回 nil。
// 返回 nil 时 runner 继续使用原始参数（Task 7 raw-by-default）。
func GetAuditRequest(ctx context.Context) map[string]any {
	if slot, ok := ctx.Value(auditRequestKey{}).(*AuditRequestSlot); ok && slot != nil {
		return slot.args
	}
	return nil
}
