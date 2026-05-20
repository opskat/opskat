package tool

import "github.com/opskat/opskat/internal/ai/audit"

// 注册 K8s 协议的命令摘要提取器到 audit 包。
// k8sAuditCommandFromArgs 助手在本包内,因此从本包 init() 完成注册。
func init() {
	audit.RegisterExtractor("exec_k8s", k8sAuditCommandFromArgs)
}
