package aictx

import "context"

// auditResultKey 是一次工具调用（或批量中的一个条目）的审计 result 摘要槽的 key。
type auditResultKey struct{}

// AuditResultSlot 是一次执行可写的审计 result 摘要槽，与 AuditRequestSlot 同一套路。
//
// 审计 result 默认是返回给调用方的完整输出（raw-by-default）。只有输出可能含注入值、
// 而 spec 规定审计只记摘要的执行器（通用资产：HTTP 记状态行、命令记 exit N）才写这个槽；
// 安装槽的一方（runner 的 auditMiddleware、AI batch / opsctl batch 的每个条目）在执行后
// 读取它，用摘要替换审计 result。摘要只影响审计落库，绝不改变返回给模型或调用方的输出。
type AuditResultSlot struct {
	result string
	set    bool
}

// NewAuditResultSlot 创建摘要槽。由审计写入方在执行之前安装。
func NewAuditResultSlot() *AuditResultSlot {
	return &AuditResultSlot{}
}

// WithAuditResultSlot 把摘要槽挂到 ctx 上，供执行器经 RecordAuditResult 写入。
func WithAuditResultSlot(ctx context.Context, slot *AuditResultSlot) context.Context {
	return context.WithValue(ctx, auditResultKey{}, slot)
}

// RecordAuditResult 写入当前执行的审计 result 摘要。没有安装槽时为 no-op。
func RecordAuditResult(ctx context.Context, result string) {
	if slot, ok := ctx.Value(auditResultKey{}).(*AuditResultSlot); ok && slot != nil {
		slot.result = result
		slot.set = true
	}
}

// GetAuditResult 读取当前执行记录的审计 result 摘要；ok=false 表示没有摘要，调用方沿用完整输出。
func GetAuditResult(ctx context.Context) (result string, ok bool) {
	if slot, isSlot := ctx.Value(auditResultKey{}).(*AuditResultSlot); isSlot && slot != nil {
		return slot.result, slot.set
	}
	return "", false
}
