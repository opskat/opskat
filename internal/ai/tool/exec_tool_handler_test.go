package tool

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/extension"
)

// mockExtToolExecutor implements ExtensionToolExecutor for testing.
type mockExtToolExecutor struct {
	ext            *extension.Extension
	def            extension.ToolDef
	defOK          bool
	policyArgsSeen []byte
	callArgsSeen   []byte
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

func (m *mockExtToolExecutor) CheckToolPolicy(ctx context.Context, _, toolName string, argsJSON []byte) (string, string, error) {
	m.policyArgsSeen = append([]byte(nil), argsJSON...)
	return m.ext.Plugin.CheckPolicy(ctx, toolName, argsJSON)
}

func (m *mockExtToolExecutor) CallTool(ctx context.Context, _, toolName string, argsJSON []byte) ([]byte, error) {
	m.callArgsSeen = append([]byte(nil), argsJSON...)
	return m.ext.Plugin.CallTool(ctx, toolName, argsJSON)
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

		// 最小 WASM 没有 check_policy/execute_tool 导出：它既能证明策略缺失或失败时
		// 调用被挡在执行前，也能在显式批准场景证明调用确实越过了确认闸。
		Convey("Policies.Type 为空时必须 fail-closed，不能执行工具", func() {
			def := extension.ToolDef{Name: "noop", Parameters: map[string]any{
				"type": "object", "properties": map[string]any{},
			}}
			ext := &extension.Extension{
				Name:     "oss",
				Manifest: &extension.Manifest{Name: "oss", Policies: extension.PoliciesDef{Type: ""}},
				Plugin:   newNoopPlugin(t),
			}
			execToolExecutor = &mockExtToolExecutor{ext: ext, def: def, defOK: true}

			_, err := handleExecTool(t.Context(), map[string]any{"command": "oss noop"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "no policy type")
		})

		Convey("Policies.Type 为空时经显式确认后才执行", func() {
			def := extension.ToolDef{Name: "noop", Parameters: map[string]any{
				"type": "object", "properties": map[string]any{
					"bucket": map[string]any{"type": "string"},
					"token":  map[string]any{"type": "string"},
				},
			}}
			ext := &extension.Extension{
				Name: "oss", Manifest: &extension.Manifest{Name: "oss"}, Plugin: newNoopPlugin(t),
			}
			executor := &mockExtToolExecutor{ext: ext, def: def, defOK: true}
			execToolExecutor = executor
			confirmed := false
			var approvedCommand, approvedKind string
			checker := permission.NewCommandPolicyChecker(func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
				confirmed = true
				approvedKind = kind
				approvedCommand = items[0].Command
				So(items[0].Type, ShouldEqual, "ext_tool")
				return permission.ApprovalResponse{Decision: "allow"}
			})

			out, err := handleExecTool(permission.WithPolicyChecker(t.Context(), checker), map[string]any{
				"command": "oss noop --bucket=production-target --token=review-secret",
			})
			So(err, ShouldBeNil)
			So(out, ShouldEqual, "")
			So(confirmed, ShouldBeTrue)
			So(approvedKind, ShouldEqual, "extension")
			So(approvedCommand, ShouldContainSubstring, "production-target")
			So(approvedCommand, ShouldNotContainSubstring, "review-secret")
			So(string(executor.callArgsSeen), ShouldContainSubstring, "production-target")
			So(string(executor.callArgsSeen), ShouldContainSubstring, "review-secret")
		})

		Convey("required 参数缺失在 policy approval plugin 之前拒绝", func() {
			def := extension.ToolDef{Name: "noop", Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"bucket": map[string]any{"type": "string"}},
				"required":   []any{"bucket"},
			}}
			ext := &extension.Extension{Name: "oss", Manifest: &extension.Manifest{Name: "oss"}, Plugin: newNoopPlugin(t)}
			execToolExecutor = &mockExtToolExecutor{ext: ext, def: def, defOK: true}
			confirmCalls := 0
			checker := permission.NewCommandPolicyChecker(func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
				confirmCalls++
				return permission.ApprovalResponse{Decision: "allow"}
			})

			_, err := handleExecTool(permission.WithPolicyChecker(t.Context(), checker), map[string]any{"command": "oss noop"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "bucket")
			So(confirmCalls, ShouldEqual, 0)
		})

		Convey("CheckPolicy 报错时必须 fail-closed，不能执行工具", func() {
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

			_, err := handleExecTool(t.Context(), map[string]any{"asset": "7", "command": "oss noop"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policy check failed")
		})
	})
}

func TestExecuteExtensionToolValidatesDelegatedArgsBeforeApprovalAndPlugin(t *testing.T) {
	def := extension.ToolDef{Name: "delete_objects", Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bucket": map[string]any{"type": "string"},
			"keys":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []any{"bucket", "keys"},
	}}
	executor := &mockExtToolExecutor{
		ext: &extension.Extension{
			Name: "oss", Manifest: &extension.Manifest{Name: "oss"}, Plugin: newNoopPlugin(t),
		},
		def: def, defOK: true,
	}
	confirmCalls := 0
	checker := permission.NewCommandPolicyChecker(func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
		confirmCalls++
		return permission.ApprovalResponse{Decision: "allow"}
	})

	_, err := ExecuteExtensionTool(permission.WithPolicyChecker(t.Context(), checker), executor, 0,
		"oss", "delete_objects", []byte(`{"bucket":"production-target"}`))
	if err == nil || !strings.Contains(err.Error(), "keys") || !strings.Contains(err.Error(), "required") {
		t.Fatalf("delegated missing required args error = %v", err)
	}
	if confirmCalls != 0 {
		t.Fatalf("invalid delegated args reached approval %d times", confirmCalls)
	}
	if len(executor.callArgsSeen) != 0 || len(executor.policyArgsSeen) != 0 {
		t.Fatalf("invalid delegated args reached extension runtime: policy=%s call=%s",
			executor.policyArgsSeen, executor.callArgsSeen)
	}
}

func TestExecuteExtensionToolInvalidApprovalDoesNotCallPlugin(t *testing.T) {
	responses := []permission.ApprovalResponse{
		{},
		{Decision: "bogus"},
		{Decision: "ALLOW"},
		{Decision: "allowAll"},
		{Decision: "allow", EditedItems: []permission.ApprovalItem{{Type: "ext_tool", Command: "changed"}}},
	}
	for _, resp := range responses {
		name := resp.Decision
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			executor := &mockExtToolExecutor{
				ext: &extension.Extension{
					Name: "oss", Manifest: &extension.Manifest{Name: "oss"}, Plugin: newNoopPlugin(t),
				},
				def: extension.ToolDef{Name: "noop", Parameters: map[string]any{
					"type": "object", "properties": map[string]any{},
				}},
				defOK: true,
			}
			checker := permission.NewCommandPolicyChecker(func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
				return resp
			})

			_, err := ExecuteExtensionTool(permission.WithPolicyChecker(t.Context(), checker), executor, 0,
				"oss", "noop", []byte(`{}`))
			if err == nil {
				t.Fatalf("invalid approval response %#v unexpectedly executed", resp)
			}
			if len(executor.callArgsSeen) != 0 {
				t.Fatalf("invalid approval response %#v reached plugin with %s", resp, executor.callArgsSeen)
			}
		})
	}
}
