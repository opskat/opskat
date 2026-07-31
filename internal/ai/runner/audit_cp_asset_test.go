package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/agents/agent"

	. "github.com/smartystreets/goconvey/convey"

	aitool "github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
)

// multiAssetRepo resolves several fixed assets by id and by name, standing in for
// assetref.Resolve's FindByName-then-Get fallback. cp needs more than staticAssetRepo's
// single asset: its whole point is two endpoints, and the interesting case
// (server → object storage) has an asset on each one.
type multiAssetRepo struct {
	asset_repo.AssetRepo
	assets []*asset_entity.Asset
}

func (r *multiAssetRepo) Find(_ context.Context, id int64) (*asset_entity.Asset, error) {
	for _, a := range r.assets {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, errors.New("asset not found")
}

func (r *multiAssetRepo) FindByName(_ context.Context, name string) ([]*asset_entity.Asset, error) {
	for _, a := range r.assets {
		if a.Name == name {
			return []*asset_entity.Asset{a}, nil
		}
	}
	return nil, nil
}

func registerAssets(t *testing.T, assets ...*asset_entity.Asset) {
	t.Helper()
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(&multiAssetRepo{assets: assets})
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })
}

// runCpToolChain drives the **real** cp tool (aitool.Tools(), same registry production
// wires into the agent) through the real auditMiddleware.
//
// The handler is expected to fail: ctx carries no policy checker, so
// permission.RequireCheckerOrPreapproved rejects the call before anything connects.
// That is exactly the case worth auditing — a transfer that was refused still has to say
// which asset it was aimed at — and it keeps the test off the network while still running
// the production argument parsing.
func runCpToolChain(t *testing.T, toolUseID string, input map[string]any) {
	t.Helper()
	var cpTool agent.Tool
	for _, tl := range aitool.Tools() {
		if tl.Name() == "cp" {
			cpTool = tl
		}
	}
	if cpTool == nil {
		t.Fatal(`tool "cp" not found in aitool.Tools()`)
	}
	td := &agent.ToolDispatcher{
		Tools:      []agent.Tool{cpTool},
		Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{{Matcher: ".*", Fn: auditMiddleware}},
	}
	result := td.Run(context.Background(), agent.DispatchInput{
		ToolName: "cp", ToolUseID: toolUseID, Input: input,
	})
	if result.Output == nil || !result.Output.IsError {
		t.Fatal("expected the cp handler to fail without a policy checker")
	}
}

// TestAuditMiddleware_CpAttributesAssetEndpoint is the regression lock for the
// attribution hole the transfer-face convergence opened: upload_file / download_file
// carried args["asset_id"], which WriteToolCall's fallback chain reads, while cp names
// only endpoints (src / dst) and hides the asset inside one of them. Every AI-initiated
// transfer therefore landed with asset_id=0 / asset_name="" and disappeared from
// audit_repo's AssetID filter — for both the name and the numeric endpoint form.
//
// It drives the real cp tool rather than a stub so the arguments under test are the ones
// the model actually sends.
func TestAuditMiddleware_CpAttributesAssetEndpoint(t *testing.T) {
	Convey("下载形态（源端是资产）：审计归属到源端资产", t, func() {
		mockRepo := registerMockAuditRepo(t)
		registerAssets(t, &asset_entity.Asset{ID: 21, Name: "web-01", Type: asset_entity.AssetTypeSSH})

		runCpToolChain(t, "tu_cp_download",
			map[string]any{"src": "web-01:/var/log/app.log", "dst": "/tmp/app.log"})

		entry := waitForAuditEntry(t, mockRepo, "cp", 21)
		So(entry.AssetName, ShouldEqual, "web-01")
	})
}

// TestAuditMiddleware_CpAttributesNumericEndpointRef covers the other reference form the
// model may write. It is not redundant with the name form: an endpoint's "<asset>:" part
// goes through assetref.Resolve, not through package audit's numericAssetRef fallback,
// which only reads args["asset"] and so never sees a number buried in an endpoint string.
func TestAuditMiddleware_CpAttributesNumericEndpointRef(t *testing.T) {
	Convey("上传形态且端点用数字 id 引用：审计归属到目的端资产", t, func() {
		mockRepo := registerMockAuditRepo(t)
		registerAssets(t, &asset_entity.Asset{ID: 21, Name: "web-01", Type: asset_entity.AssetTypeSSH})

		runCpToolChain(t, "tu_cp_upload_numeric",
			map[string]any{"src": "/tmp/app.log", "dst": "21:/srv/app.log"})

		entry := waitForAuditEntry(t, mockRepo, "cp", 21)
		So(entry.AssetName, ShouldEqual, "web-01")
	})
}

// TestAuditMiddleware_CpAttributesDestinationWhenBothEndsAreAssets pins the choice made
// for the case cp added and upload_file / download_file never had: a transfer whose two
// ends are both assets (server ↔ object storage). audit_logs holds one
// (asset_id, asset_name) pair and one tool call must stay one row, so the row names the
// destination — the mutated side, and the same end opsctl's cp already records
// (cmd/opsctl/command/cp.go's primaryAssetID). The source end stays readable in the
// command summary, which is asserted here too: unindexed is not the same as erased, and
// that is the whole reason this trade-off is acceptable.
func TestAuditMiddleware_CpAttributesDestinationWhenBothEndsAreAssets(t *testing.T) {
	Convey("两端都是资产时：审计归属到目的端，源端仍留在命令摘要里", t, func() {
		mockRepo := registerMockAuditRepo(t)
		registerAssets(t,
			&asset_entity.Asset{ID: 21, Name: "web-01", Type: asset_entity.AssetTypeSSH},
			&asset_entity.Asset{ID: 22, Name: "s3-prod", Type: asset_entity.AssetTypeOSS})

		runCpToolChain(t, "tu_cp_ssh_to_oss",
			map[string]any{"src": "web-01:/srv/app.log", "dst": "s3-prod:/backups/app.log"})

		entry := waitForAuditEntry(t, mockRepo, "cp", 22)
		So(entry.AssetName, ShouldEqual, "s3-prod")
		So(entry.Command, ShouldEqual, "cp web-01:/srv/app.log → s3-prod:/backups/app.log")
	})
}

// TestCpToolSchemaCarriesTheAuditedEndpointArgs cross-checks two independent sources of
// truth that nothing else ties together: the argument names the cp tool advertises to the
// model (tools_exec.go) and the argument names audit attribution reads
// (transferEndpointArgs). They are edited in different packages for different reasons,
// and a rename on the schema side is silent — the handler would follow the new name, the
// audit resolver would keep reading the old one and quietly go back to attributing
// nothing. The behavior tests above cannot see it: they supply the argument map
// themselves, so they would keep passing with both halves renamed under them.
func TestCpToolSchemaCarriesTheAuditedEndpointArgs(t *testing.T) {
	Convey("cp 工具 schema 里必须有审计归属读的那两个端点参数", t, func() {
		var schema agent.Schema
		for _, tl := range aitool.Tools() {
			if tl.Name() == "cp" {
				schema = tl.Schema()
			}
		}
		So(schema.Properties, ShouldNotBeEmpty)
		for _, key := range transferEndpointArgs {
			So(schema.Properties, ShouldContainKey, key)
		}
	})
}
