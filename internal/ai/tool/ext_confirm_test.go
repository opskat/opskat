package tool

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
)

// 扩展审批没有定义可安全复用的参数/grant pattern，因此只接受 allow/deny。
// allowAll、空值、未知值和大小写变体都必须拒绝，且 kind 必须让前端隐藏 remember。
func TestConfirmExtensionTool_DecisionMapping(t *testing.T) {
	cases := []struct {
		decision   string
		wantErr    bool
		wantResult aictx.Decision
		wantSource string
	}{
		{"allow", false, aictx.Allow, aictx.SourceUserAllow},
		{"allowAll", true, aictx.Deny, aictx.SourceUserDeny},
		{"deny", true, aictx.Deny, aictx.SourceUserDeny},
		{"", true, aictx.Deny, aictx.SourceUserDeny},
		{"bogus", true, aictx.Deny, aictx.SourceUserDeny},
		{"ALLOW", true, aictx.Deny, aictx.SourceUserDeny},
	}
	for _, tc := range cases {
		name := tc.decision
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			var gotKind string
			checker := permission.NewCommandPolicyChecker(
				func(_ context.Context, kind string, _ []permission.ApprovalItem) permission.ApprovalResponse {
					gotKind = kind
					return permission.ApprovalResponse{Decision: tc.decision}
				})
			slot := &aictx.CheckResult{}
			ctx := aictx.WithCheckResultSlot(permission.WithPolicyChecker(context.Background(), checker), slot)

			err := confirmExtensionTool(ctx, 1, "oss", "list_objects", "oss.list_objects {}", "test reason")
			if (err != nil) != tc.wantErr {
				t.Fatalf("decision %q: err = %v, wantErr = %v", tc.decision, err, tc.wantErr)
			}
			if gotKind != "extension" {
				t.Errorf("confirm kind = %q, want extension", gotKind)
			}
			if slot.Decision != tc.wantResult || slot.DecisionSource != tc.wantSource {
				t.Errorf("recorded decision = %v/%q, want %v/%q",
					slot.Decision, slot.DecisionSource, tc.wantResult, tc.wantSource)
			}
		})
	}
}
