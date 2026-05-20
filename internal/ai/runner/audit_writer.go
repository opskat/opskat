package runner

import "github.com/opskat/opskat/internal/ai/audit"

// auditWriter 由 auditMiddleware 在每次工具调用后异步写审计日志。
// 包级变量便于测试时替换。
var auditWriter audit.AuditWriter = audit.NewDefaultAuditWriter()
