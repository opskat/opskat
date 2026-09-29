package runner

import (
	"context"
	"testing"

	"github.com/cago-frame/agents/agent"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"

	// Blank-imported for its init() side effect only: internal/ai/tool registers the
	// get_asset_secret result projector there (audit.RegisterResultProjector). This test
	// exercises the real end-to-end path — auditMiddleware -> DefaultAuditWriter -> the
	// registered projector — not a fake stand-in for it.
	_ "github.com/opskat/opskat/internal/ai/tool"

	. "github.com/smartystreets/goconvey/convey"
)

// TestAuditMiddleware_GetAssetSecretResultNeverReachesAuditLog locks
// docs/specs/2026-09-28-generic-asset.md「取值」"审计：只记录资产、字段名和判定结果，
// 不记录值" through the real AI dispatch path: auditMiddleware captures whatever the tool
// returns as c.Output's text, and get_asset_secret's own text on an allowed read is the
// plaintext value itself (internal/ai/tool/tool_handlers_secret.go). Without the
// registered result projector, that plaintext would land verbatim in audit_logs.result —
// this is the same middleware every AI tool call actually goes through, not a
// package-internal stand-in for it.
func TestAuditMiddleware_GetAssetSecretResultNeverReachesAuditLog(t *testing.T) {
	Convey("get_asset_secret 的明文绝不落进 audit_logs.result", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "1").Return(nil, nil)
		m.EXPECT().Find(gomock.Any(), int64(1)).Return(
			&asset_entity.Asset{ID: 1, Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric}, nil)

		// #nosec G101 -- intentional test fixture used to verify that secret field values never leak.
		secret := "glsa_must_not_reach_audit_log"
		decision := &aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow}
		runAuditChain(t, context.Background(), "get_asset_secret", "tu_secret_1",
			map[string]any{"asset": "1", "field": "token"},
			decision,
			func() (*agent.ToolResultBlock, error) {
				return &agent.ToolResultBlock{
					Content: []agent.ContentBlock{agent.TextBlock{Text: secret}},
				}, nil
			},
		)

		entry := waitForAuditEntry(t, mockRepo, "get_asset_secret", 1)
		So(entry.Result, ShouldNotContainSubstring, secret)
		So(entry.Result, ShouldContainSubstring, "field=token")
		So(entry.Result, ShouldContainSubstring, "decision=allow")
	})
}

// TestAuditMiddleware_GetAssetSecretDeniedResultAlsoProjected proves the projection is
// unconditional, not only applied on the allow path where the raw text happens to be the
// value: a denial's own message text (also never the plaintext, but exercised here for
// symmetry with the allow case) is projected the same way.
func TestAuditMiddleware_GetAssetSecretDeniedResultAlsoProjected(t *testing.T) {
	Convey("拒绝路径的 Result 同样经过投影", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "2").Return(nil, nil)
		m.EXPECT().Find(gomock.Any(), int64(2)).Return(
			&asset_entity.Asset{ID: 2, Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric}, nil)

		decision := &aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}
		runAuditChain(t, context.Background(), "get_asset_secret", "tu_secret_2",
			map[string]any{"asset": "2", "field": "token"},
			decision,
			func() (*agent.ToolResultBlock, error) {
				return &agent.ToolResultBlock{
					Content: []agent.ContentBlock{agent.TextBlock{Text: "USER DENIED: ..."}},
				}, nil
			},
		)

		entry := waitForAuditEntry(t, mockRepo, "get_asset_secret", 2)
		So(entry.Result, ShouldContainSubstring, "field=token")
		So(entry.Result, ShouldContainSubstring, "decision=deny")
	})
}
