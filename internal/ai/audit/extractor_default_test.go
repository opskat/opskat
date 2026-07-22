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

// TestExtractor_DeleteAsset 锁住 delete_asset 的摘要提取：直接读 args["asset"]。
func TestExtractor_DeleteAsset(t *testing.T) {
	got := ExtractCommandForAudit("delete_asset", map[string]any{"asset": "web-9"})
	assert.Equal(t, "delete asset web-9", got)
}

// TestGroupScopedTools_Exhaustive 锁住 groupScopedTools 的**完整**成员，而不只是"这三个
// 名字都在表里"。isGroupScopedTool 式的单点检查能抓住"该注册而漏注册"，但抓不住反过来
// 的漂移：extractor_default.go 的 init() 里少了一行 RegisterGroupScopedTool 调用（比如
// 重构时手滑删掉），既不会编译失败，也不会让除了该工具自己那条锁定测试之外的任何测试
// 变红——TestWriteToolCall_GetGroupDoesNotMisattributeToAsset 只锁 get_group，
// ...PutGroupDoesNotMisattributeToAsset 只锁 put_group，两者互不覆盖对方。把整张表与一份
// 显式清单做集合相等比较，任何增删都会在这里产生一处显眼的 diff，供评审注意——
// 与 internal/ai/tool/tools_test.go 的注册表穷尽性断言是同一种解法（该文件的注释解释了
// 为什么 ShouldContainKey 抓不住"多注册了一个没人知道的"）。
func TestGroupScopedTools_Exhaustive(t *testing.T) {
	expected := map[string]bool{
		"get_group":    true,
		"put_group":    true,
		"delete_group": true,
	}

	groupScopedMu.RLock()
	got := make(map[string]bool, len(groupScopedTools))
	for name, v := range groupScopedTools {
		got[name] = v
	}
	groupScopedMu.RUnlock()

	assert.Equal(t, expected, got)
}

// TestExtractor_DeleteGroup 锁住 delete_group 的摘要提取，覆盖 delete_assets 为
// true/false 两种情况。args["id"] 在真实调用里是 JSON number（解码成 float64），不是
// ArgString 认得的字符串——实施者写这个提取器时就踩过这个坑：用 ArgString(a, "id")
// 会静默拿到空串，摘要永远是 "delete group "，id 部分整个消失，而且不报错，测试也
// 不会因为编译失败而拦住它。这条测试就是钉住那个具体回归的锁。
func TestExtractor_DeleteGroup(t *testing.T) {
	got := ExtractCommandForAudit("delete_group", map[string]any{"id": float64(3)})
	assert.Equal(t, "delete group 3", got)

	gotWithAssets := ExtractCommandForAudit("delete_group",
		map[string]any{"id": float64(3), "delete_assets": true})
	assert.Equal(t, "delete group 3 (with assets)", gotWithAssets)
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
