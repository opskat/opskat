package bootstrap

import (
	"testing"

	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
)

// TestRegisterRepositoriesAgentSourceRepo 回归：registerRepositories() 必须注册
// ssh_agent_source_repo。此前漏注册导致生产进程 SSHAgentSource() 为 nil，任何来源
// 操作（创建/列表/检查/删除/复制公钥）在运行时 nil-pointer panic；单测因各自显式
// 注入而没暴露。
func TestRegisterRepositoriesAgentSourceRepo(t *testing.T) {
	// 清掉可能残留的实例，确保测的是 registerRepositories 自己注册的。
	ssh_agent_source_repo.RegisterSSHAgentSource(nil)

	registerRepositories()

	if ssh_agent_source_repo.SSHAgentSource() == nil {
		t.Fatal("registerRepositories 未注册 ssh_agent_source_repo：生产进程来源操作会 nil panic")
	}
}
