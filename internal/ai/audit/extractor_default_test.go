package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractor_ExecHasItsOwnRegistration 锁住 exec 的提取器存在且读 command 参数。
// 注意它**单独不够**：别名还在的年代这个测试同样会过（别名把 exec 转成 run_command）。
// 真正的守卫是下面的 TestExtractor_ExecSurvivesRunCommandRemoval。
func TestExtractor_ExecHasItsOwnRegistration(t *testing.T) {
	got := ExtractCommandForAudit("exec", map[string]any{"asset": "web-1", "command": "uptime"})
	assert.Equal(t, "uptime", got)
}

// TestExtractor_ExecDoesNotBorrowAnotherToolsExtractor 锁住 exec 有自己的注册，
// 而不是从别处借的——历史上它靠 extractor.go 里一句 toolName == "exec" → "run_command"
// 的别名借用旧工具的提取器，一旦那个旧工具下线，审计日志就会**静默**丢失命令摘要。
//
// 它取代了早先的 ...SurvivesRunCommandRemoval：那个测试摘掉 run_command 的注册再验
// exec，但 run_command 已随旧工具一起删除，摘一个根本没注册的名字是空操作，于是它
// 退化成与 ExecHasItsOwnRegistration 一模一样的断言，却顶着一个更强的名字。
//
// 这里改为摘掉**除 exec 之外的全部**提取器：只要 exec 的提取再次变成借来的，
// 无论借的是哪一个工具，这里都会拿到空串而失败。
func TestExtractor_ExecDoesNotBorrowAnotherToolsExtractor(t *testing.T) {
	extractorsMu.RLock()
	others := make([]string, 0, len(extractors))
	for name := range extractors {
		if name != "exec" {
			others = append(others, name)
		}
	}
	extractorsMu.RUnlock()
	// 注册表若缩到只剩 exec，下面的循环什么也摘不掉，断言就成了空转。
	require.NotEmpty(t, others, "no other extractor registered — this test would be vacuous")

	for _, name := range others {
		t.Cleanup(unregisterExtractorForTest(name))
	}

	got := ExtractCommandForAudit("exec", map[string]any{"asset": "web-1", "command": "uptime"})
	assert.Equal(t, "uptime", got, "exec must have its own registration, not borrow another tool's")
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
