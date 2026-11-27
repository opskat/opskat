package tool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 工具列表（Tools 里的定义）与副作用声明表是两份独立维护的来源：新增工具时忘了声明，
// opsctl mcp 会把它当成「未知副作用」下发，客户端无法在调用前提示用户。
func TestEveryToolDeclaresBehavior(t *testing.T) {
	names := make(map[string]bool)
	for _, tl := range Tools() {
		names[tl.Name()] = true
		_, ok := BehaviorOf(tl.Name())
		require.Truef(t, ok, "tool %q has no Behavior declared in toolBehaviors", tl.Name())
	}
	for name := range toolBehaviors {
		require.Truef(t, names[name], "toolBehaviors declares %q, which is not in Tools()", name)
	}
}
