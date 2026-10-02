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

// auditResultTool 模拟一个在执行中通过 aictx.RecordAuditResult 写审计摘要的工具（通用资产
// 的 exec 执行器就是这样做的），返回给模型的仍是完整文本。summary 为空表示不写摘要。
type auditResultTool struct {
	name    string
	summary string
	text    string
}

func (r *auditResultTool) Name() string         { return r.name }
func (r *auditResultTool) Description() string  { return "audit-result stub" }
func (r *auditResultTool) Schema() agent.Schema { return agent.Schema{Type: "object"} }
func (r *auditResultTool) Call(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
	if r.summary != "" {
		aictx.RecordAuditResult(ctx, r.summary)
	}
	return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: r.text}}}, nil
}

func dispatchWithAudit(ctx context.Context, tool agent.Tool, input map[string]any) agent.DispatchResult {
	td := &agent.ToolDispatcher{
		Tools:      []agent.Tool{tool},
		Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{{Matcher: ".*", Fn: auditMiddleware}},
	}
	return td.Run(ctx, agent.DispatchInput{ToolName: tool.Name(), ToolUseID: "tu_" + tool.Name(), Input: input})
}

// TestAuditMiddleware_RecordedAuditResultReplacesOutputInAuditOnly locks spec「策略、审批与
// 审计」for the AI path (V16b): a generic HTTP exec whose response body echoes the injected
// Authorization header must store only the status line in audit_logs.result, while the
// model still receives the full body. A tool that records no summary — every other asset
// type's exec — keeps its full output in the audit row.
func TestAuditMiddleware_RecordedAuditResultReplacesOutputInAuditOnly(t *testing.T) {
	// #nosec G101 -- intentional test fixture used to verify that injected values never leak.
	body := "HTTP 200 OK\n\nGET /echo\nAuthorization=Bearer glsa_echoed_must_not_reach_audit"

	Convey("通用资产 exec 记录的摘要替换审计 result，模型输出不变", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "11").Return(nil, nil)
		m.EXPECT().Find(gomock.Any(), int64(11)).Return(
			&asset_entity.Asset{ID: 11, Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric}, nil)

		res := dispatchWithAudit(context.Background(),
			&auditResultTool{name: "exec", summary: "HTTP 200 OK", text: body},
			map[string]any{"asset": "11", "command": "GET /echo"})

		entry := waitForAuditEntry(t, mockRepo, "exec", 11)
		So(entry.Result, ShouldEqual, "HTTP 200 OK")
		got, err := extractAuditResult(res.Output)
		So(err, ShouldBeNil)
		So(got, ShouldEqual, body)
	})

	Convey("未记录摘要的 exec（其他资产类型）审计 result 保持完整输出", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "12").Return(nil, nil)
		m.EXPECT().Find(gomock.Any(), int64(12)).Return(
			&asset_entity.Asset{ID: 12, Name: "web-1", Type: asset_entity.AssetTypeSSH}, nil)

		dispatchWithAudit(context.Background(),
			&auditResultTool{name: "exec", text: "uptime output"},
			map[string]any{"asset": "12", "command": "uptime"})

		entry := waitForAuditEntry(t, mockRepo, "exec", 12)
		So(entry.Result, ShouldEqual, "uptime output")
	})
}
