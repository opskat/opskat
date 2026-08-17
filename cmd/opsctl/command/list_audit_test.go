package command

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
)

// listAuditRepoStub 记录收到的查询选项并返回预置行。它只为下面两个行为边界服务：
// --asset 的解析结果与 --limit 的默认值 —— 断言的是 CLI 输入到查询选项的映射，
// 不是审计行内容本身。
type listAuditRepoStub struct {
	opts []audit_repo.ListOptions
	rows []*audit_entity.AuditLog
}

func (s *listAuditRepoStub) Create(context.Context, *audit_entity.AuditLog) error {
	return nil
}

func (s *listAuditRepoStub) List(_ context.Context, opts audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	s.opts = append(s.opts, opts)
	return s.rows, int64(len(s.rows)), nil
}

func (s *listAuditRepoStub) ListSessions(context.Context, int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

// runListAudit 捕获 stdout 后跑 cmdList audit，返回退出码与输出。
func runListAudit(ctx context.Context, args ...string) (int, string) {
	r, w, err := os.Pipe()
	So(err, ShouldBeNil)
	origStdout := os.Stdout
	os.Stdout = w

	code := cmdList(ctx, nil, args)

	So(w.Close(), ShouldBeNil)
	os.Stdout = origStdout
	data, readErr := io.ReadAll(r)
	So(readErr, ShouldBeNil)
	return code, string(data)
}

func TestCmdListAudit(t *testing.T) {
	Convey("cmdList audit", t, func() {
		origAudit := audit_repo.Audit()
		stub := &listAuditRepoStub{}
		audit_repo.RegisterAudit(stub)
		defer audit_repo.RegisterAudit(origAudit)

		Convey("--asset accepts a numeric ID", func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
			mockAsset.EXPECT().Find(gomock.Any(), int64(7)).
				Return(&asset_entity.Asset{ID: 7, Name: "web-01"}, nil).AnyTimes()
			origAsset := asset_repo.Asset()
			asset_repo.RegisterAsset(mockAsset)
			defer asset_repo.RegisterAsset(origAsset)

			code, _ := runListAudit(context.Background(), "audit", "--asset", "7")

			So(code, ShouldEqual, 0)
			So(stub.opts, ShouldHaveLength, 1)
			So(stub.opts[0].AssetID, ShouldEqual, 7)
		})

		Convey("--asset resolves an asset name to its ID", func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
			mockAsset.EXPECT().List(gomock.Any(), gomock.Any()).
				Return([]*asset_entity.Asset{{ID: 5, Name: "web-01"}, {ID: 6, Name: "db-01"}}, nil).AnyTimes()
			origAsset := asset_repo.Asset()
			asset_repo.RegisterAsset(mockAsset)
			defer asset_repo.RegisterAsset(origAsset)

			code, _ := runListAudit(context.Background(), "audit", "--asset", "web-01")

			So(code, ShouldEqual, 0)
			So(stub.opts, ShouldHaveLength, 1)
			So(stub.opts[0].AssetID, ShouldEqual, 5)
		})

		Convey("--asset that matches nothing fails before querying audit rows", func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
			mockAsset.EXPECT().List(gomock.Any(), gomock.Any()).
				Return([]*asset_entity.Asset{{ID: 5, Name: "web-01"}}, nil).AnyTimes()
			origAsset := asset_repo.Asset()
			asset_repo.RegisterAsset(mockAsset)
			defer asset_repo.RegisterAsset(origAsset)

			code, _ := runListAudit(context.Background(), "audit", "--asset", "ghost")

			So(code, ShouldEqual, 1)
			So(stub.opts, ShouldBeEmpty)
		})

		Convey("--limit falls back to the default when omitted or non-positive", func() {
			code, _ := runListAudit(context.Background(), "audit")
			So(code, ShouldEqual, 0)
			So(stub.opts[0].Limit, ShouldEqual, defaultAuditListLimit)

			code, _ = runListAudit(context.Background(), "audit", "--limit", "5")
			So(code, ShouldEqual, 0)
			So(stub.opts[1].Limit, ShouldEqual, 5)

			code, _ = runListAudit(context.Background(), "audit", "--limit", "0")
			So(code, ShouldEqual, 0)
			So(stub.opts[2].Limit, ShouldEqual, defaultAuditListLimit)
		})

		Convey("table header follows the policy lang in ctx", func() {
			stub.rows = []*audit_entity.AuditLog{{
				ID: 1, Source: "opsctl", ToolName: "exec", AssetID: 7, AssetName: "web-01",
				Command: "uptime", Decision: "allow", DecisionSource: "user_allow", Createtime: 1755400000,
			}}

			_, out := runListAudit(aictx.WithPolicyLang(context.Background(), "en"), "audit")
			So(out, ShouldContainSubstring, "TIME")
			So(out, ShouldContainSubstring, "DECISION SOURCE")

			_, out = runListAudit(aictx.WithPolicyLang(context.Background(), "zh-cn"), "audit")
			So(out, ShouldContainSubstring, "时间")
			So(out, ShouldContainSubstring, "决策来源")
		})

		Convey("long commands are summarized, stored values are presented verbatim", func() {
			stub.rows = []*audit_entity.AuditLog{
				{
					ID: 1, Source: "opsctl", ToolName: "exec", AssetID: 7, AssetName: "web-01",
					Command: strings.Repeat("x", 100), Decision: "allow", DecisionSource: "user_allow",
					Createtime: 1755400000,
				},
				{
					ID: 2, Source: "ai", ToolName: "list_assets",
					Command: "", Createtime: 1755400100,
				},
			}

			code, out := runListAudit(context.Background(), "audit")

			So(code, ShouldEqual, 0)
			// 摘要：超长命令被截断，不会整段照抄到表格里
			So(out, ShouldContainSubstring, "...")
			So(out, ShouldNotContainSubstring, strings.Repeat("x", 70))
			// 原样：存量字段不二次改写、不脱敏、不补占位
			So(out, ShouldContainSubstring, "user_allow")
			So(out, ShouldContainSubstring, "web-01")
			So(out, ShouldContainSubstring, "list_assets")
			So(out, ShouldNotContainSubstring, "***")
		})
	})
}
