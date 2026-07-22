package tool

import (
	"testing"

	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/extension"
)

// mockExtToolExecutor implements ExtensionToolExecutor for testing.
type mockExtToolExecutor struct {
	ext   *extension.Extension
	def   extension.ToolDef
	defOK bool
}

func (m *mockExtToolExecutor) FindExtensionByTool(extName, toolName string) *extension.Extension {
	return m.ext
}

func (m *mockExtToolExecutor) FindToolDef(extName, toolName string) (extension.ToolDef, bool) {
	return m.def, m.defOK
}

func (m *mockExtToolExecutor) GetExtensionPolicyGroups(extName, assetType string, assetID int64) []string {
	return nil
}

// Compile-time interface check.
var _ ExtensionToolExecutor = (*mockExtToolExecutor)(nil)

// newNoopPlugin loads the same minimal, export-less WASM module pkg/extension's own
// TestLoadPlugin uses (magic + version, no exported functions). Any call into it
// (check_policy, execute_tool) errors — exactly the shape the characterization tests
// below need to drive "the policy/execution stage was actually reached" without a real
// compiled extension.
func newNoopPlugin(t *testing.T) *extension.Plugin {
	t.Helper()
	host := extension.NewDefaultHostProvider(extension.DefaultHostConfig{Logger: zap.NewNop()})
	t.Cleanup(host.CloseAll)

	minimalWasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	p, err := extension.LoadPlugin(t.Context(), &extension.Manifest{Name: "oss", Version: "1.0.0"}, minimalWasm, host, nil)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	t.Cleanup(func() { _ = p.Close(t.Context()) })
	return p
}

func TestExecToolHandler(t *testing.T) {
	Convey("handleExecTool", t, func() {
		origExecutor := execToolExecutor
		t.Cleanup(func() { execToolExecutor = origExecutor })

		Convey("should return error when no executor configured", func() {
			execToolExecutor = nil
			_, err := handleExecTool(t.Context(), map[string]any{
				"command": "oss some_tool",
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not found")
		})

		Convey("should return error when command is missing", func() {
			_, err := handleExecTool(t.Context(), map[string]any{})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "command")
		})

		Convey("should return error when command names no tool", func() {
			execToolExecutor = &mockExtToolExecutor{}
			_, err := handleExecTool(t.Context(), map[string]any{
				"command": "oss",
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "tool")
		})

		Convey("should return error when tool not found in extension", func() {
			execToolExecutor = &mockExtToolExecutor{ext: nil}
			_, err := handleExecTool(t.Context(), map[string]any{
				"command": "oss nonexistent",
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not found")
			So(err.Error(), ShouldContainSubstring, "nonexistent")
		})

		// 下面两条是策略路径的首个覆盖（此前零测试，见 recon G.1）。它们只表征现状，
		// 不改行为：真要驱动 Deny / NeedConfirm 决策需要一个真的会返回 action 的
		// check_policy 导出，而仓库里没有任何编译好的测试用 WASM 扩展（唯一的 WASM
		// fixture 是 pkg/extension 自己 TestLoadPlugin 用的、没有导出函数的最小模块）；
		// 伪造一个真实决策需要引入 wat2wasm/TinyGo 工具链，超出本 task 范围。noop 插件
		// 没有任何导出——wazero 实例化一个没有 _start 的空模块不会报错，只是什么都不
		// 执行，call() 因此拿到空输出、nil error；CallTool 直接把这段空输出原样返回
		// （它不解码），所以两条路径最终都"成功"（err == nil）。这两条测试转而锁定
		// recon E 节记录的两处已知 fail-open：调用没有被策略拦下，为将来真正修复它们
		// （Task 12 的收尾 issue）留下抓手，未来行为一变这里就会变红。
		Convey("characterization: Policies.Type 为空时跳过策略检查、不要求 asset（recon E.1，现状未变）", func() {
			def := extension.ToolDef{Name: "noop", Parameters: map[string]any{
				"type": "object", "properties": map[string]any{},
			}}
			ext := &extension.Extension{
				Name:     "oss",
				Manifest: &extension.Manifest{Name: "oss", Policies: extension.PoliciesDef{Type: ""}},
				Plugin:   newNoopPlugin(t),
			}
			execToolExecutor = &mockExtToolExecutor{ext: ext, def: def, defOK: true}

			out, err := handleExecTool(t.Context(), map[string]any{"command": "oss noop"})
			// 没有资产也没有被拦下（没有 "requires asset" 之类的错误），调用直接
			// 放行到执行——空 Policies.Type 确实完全跳过了策略检查。
			So(err, ShouldBeNil)
			So(out, ShouldEqual, "")
		})

		Convey("characterization: CheckPolicy 报错时策略检查被跳过，调用照常放行（recon E.2 fail-open，本 task 不修）", func() {
			m := setupUnified(t)
			m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil).AnyTimes()
			m.EXPECT().Find(gomock.Any(), int64(7)).Return(
				&asset_entity.Asset{ID: 7, Name: "srv-1", Type: "oss"}, nil).AnyTimes()

			def := extension.ToolDef{Name: "noop", Parameters: map[string]any{
				"type": "object", "properties": map[string]any{},
			}}
			ext := &extension.Extension{
				Name:     "oss",
				Manifest: &extension.Manifest{Name: "oss", Policies: extension.PoliciesDef{Type: "oss"}},
				Plugin:   newNoopPlugin(t),
			}
			execToolExecutor = &mockExtToolExecutor{ext: ext, def: def, defOK: true}

			out, err := handleExecTool(t.Context(), map[string]any{"asset": "7", "command": "oss noop"})
			// noop 插件没有真正的 check_policy 导出，CheckPolicy 因解不出合法的决策
			// JSON 而报错；handler 把"CheckPolicy 出错"等同于"没有 action"，直接放行
			// 到执行，而不是拒绝——一次本该被门禁挡下的调用完全放行且不留痕迹，
			// 这正是 recon E.2 记录的 fail-open。
			So(err, ShouldBeNil)
			So(out, ShouldEqual, "")
		})
	})
}
