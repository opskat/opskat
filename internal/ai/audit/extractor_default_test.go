package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExtractor_ExecHasItsOwnRegistration 锁住 exec 的提取器存在且读 command 参数。
// 注意它**单独不够**：别名还在的年代这个测试同样会过（别名把 exec 转成 run_command）。
// 真正的守卫是下面的 TestExtractor_ExecSurvivesRunCommandRemoval。
func TestExtractor_ExecHasItsOwnRegistration(t *testing.T) {
	got := ExtractCommandForAudit("exec", map[string]any{"asset": "web-1", "command": "uptime"})
	assert.Equal(t, "uptime", got)
}

// TestExtractor_ExecSurvivesRunCommandRemoval 直接模拟旧工具下线：摘掉 run_command
// 的注册后，exec 的提取必须仍然工作。别名存在时这里会拿到空串——审计日志静默丢失
// 命令摘要，不报错、不记日志。
func TestExtractor_ExecSurvivesRunCommandRemoval(t *testing.T) {
	restore := unregisterExtractorForTest("run_command")
	t.Cleanup(restore)

	got := ExtractCommandForAudit("exec", map[string]any{"asset": "web-1", "command": "uptime"})
	assert.Equal(t, "uptime", got, "exec must not depend on run_command's extractor")
}

// TestUnregisterExtractorForTest_Restores 锁住辅助函数自身：它若还原不干净，
// 上面那个测试就会污染同包其他用例，而污染的表现是别处莫名其妙地失败。
// The subject is "exec" — a name that is actually registered. It used to be
// "run_command", which is now gone: against an unregistered name every assertion below
// holds vacuously (empty before, empty after), so the helper could stop restoring
// anything and this test would still pass.
func TestUnregisterExtractorForTest_Restores(t *testing.T) {
	before := ExtractCommandForAudit("exec", map[string]any{"command": "uptime"})
	assert.NotEmpty(t, before, "subject must be a registered extractor, else this test is vacuous")
	restore := unregisterExtractorForTest("exec")
	assert.Empty(t, ExtractCommandForAudit("exec", map[string]any{"command": "uptime"}))
	restore()
	assert.Equal(t, before, ExtractCommandForAudit("exec", map[string]any{"command": "uptime"}))

	// 未注册的名字：还原函数必须不留下一个凭空冒出来的条目。
	restoreAbsent := unregisterExtractorForTest("no-such-tool")
	restoreAbsent()
	assert.Empty(t, ExtractCommandForAudit("no-such-tool", map[string]any{"command": "uptime"}))
}
