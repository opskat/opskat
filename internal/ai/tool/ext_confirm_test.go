package tool

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
)

// confirmExtensionTool 把 ConfirmFunc 的响应映射成放行/拒绝，映射规则必须与
// decisionFromApproval（delete）、HandleConfirm、local_tool_gate 一致：只有
// "deny" 是拒绝，"allow"/"allowAll" 都是放行。
//
// ext_tool 审批走 kind="single"，前端 ApprovalBlock 对 single 会渲染「记住并允许」
// 按钮（respond("allowAll")），所以 allowAll 必然可达。把 allowAll 当成拒绝会让用户
// 明确批准过的扩展工具静默不执行，并在 audit_logs 里把一次真实允许记成 user denied。
func TestConfirmExtensionTool_DecisionMapping(t *testing.T) {
	cases := []struct {
		decision   string
		wantErr    bool
		wantResult aictx.Decision
		wantSource string
	}{
		{"allow", false, aictx.Allow, aictx.SourceUserAllow},
		{"allowAll", false, aictx.Allow, aictx.SourceUserAllow},
		{"deny", true, aictx.Deny, aictx.SourceUserDeny},
	}
	for _, tc := range cases {
		t.Run(tc.decision, func(t *testing.T) {
			var gotKind string
			checker := permission.NewCommandPolicyChecker(
				func(_ context.Context, kind string, _ []permission.ApprovalItem) permission.ApprovalResponse {
					gotKind = kind
					return permission.ApprovalResponse{Decision: tc.decision}
				})
			slot := &aictx.CheckResult{}
			ctx := aictx.WithCheckResultSlot(permission.WithPolicyChecker(context.Background(), checker), slot)

			err := confirmExtensionTool(ctx, 1, "oss", "list_objects", "test reason")
			if (err != nil) != tc.wantErr {
				t.Fatalf("decision %q: err = %v, wantErr = %v", tc.decision, err, tc.wantErr)
			}
			if gotKind != "single" {
				t.Errorf("confirm kind = %q, want single", gotKind)
			}
			if slot.Decision != tc.wantResult || slot.DecisionSource != tc.wantSource {
				t.Errorf("recorded decision = %v/%q, want %v/%q",
					slot.Decision, slot.DecisionSource, tc.wantResult, tc.wantSource)
			}
		})
	}
}
