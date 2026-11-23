package command

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

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

func (*listAuditRepoStub) Facets(context.Context, int64, int64) (*audit_repo.Facets, error) {
	return &audit_repo.Facets{}, nil
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

// runListAuditStderr 捕获 stderr 后跑 cmdList audit，返回退出码与错误输出。
func runListAuditStderr(ctx context.Context, args ...string) (int, string) {
	r, w, err := os.Pipe()
	So(err, ShouldBeNil)
	origStderr := os.Stderr
	os.Stderr = w

	code := cmdList(ctx, nil, args)

	So(w.Close(), ShouldBeNil)
	os.Stderr = origStderr
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
			So(stub.opts[0].AssetIDs, ShouldResemble, []int64{7})
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
			So(stub.opts[0].AssetIDs, ShouldResemble, []int64{5})
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

		Convey("--asset can be repeated and the assets are unioned", func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
			mockAsset.EXPECT().Find(gomock.Any(), int64(7)).Return(&asset_entity.Asset{ID: 7, Name: "web-01"}, nil).AnyTimes()
			mockAsset.EXPECT().Find(gomock.Any(), int64(8)).Return(&asset_entity.Asset{ID: 8, Name: "web-02"}, nil).AnyTimes()
			origAsset := asset_repo.Asset()
			asset_repo.RegisterAsset(mockAsset)
			defer asset_repo.RegisterAsset(origAsset)

			code, _ := runListAudit(context.Background(), "audit", "--asset", "7", "--asset", "8")

			So(code, ShouldEqual, 0)
			So(stub.opts[0].AssetIDs, ShouldResemble, []int64{7, 8})
		})

		Convey("filter flags map to the same query the desktop audit page uses", func() {
			code, _ := runListAudit(context.Background(), "audit",
				"--source", "ai", "--tool", "exec", "--result", "failed", "--decision", "deny",
				"--decider", "exec_gate", "--session", "s1", "--grep", "nginx")

			So(code, ShouldEqual, 0)
			opts := stub.opts[0]
			So(opts.Source, ShouldEqual, "ai")
			So(opts.ToolName, ShouldEqual, "exec")
			So(opts.Success, ShouldNotBeNil)
			So(*opts.Success, ShouldBeFalse)
			So(opts.Decision, ShouldEqual, "deny")
			So(opts.DecisionSources, ShouldContain, aictx.SourceExecGateBlocked)
			So(opts.DecisionSources, ShouldContain, aictx.SourceExecInvalidInput)
			So(opts.SessionID, ShouldEqual, "s1")
			So(opts.Keyword, ShouldEqual, "nginx")
		})

		Convey("--since limits rows to the recent window", func() {
			for _, c := range []struct {
				arg  string
				span int64
			}{{"30m", 1800}, {"6h", 6 * 3600}, {"7d", 7 * 86400}} {
				stub.opts = nil
				before := time.Now().Unix()
				code, _ := runListAudit(context.Background(), "audit", "--since", c.arg)
				after := time.Now().Unix()

				So(code, ShouldEqual, 0)
				So(stub.opts[0].StartTime, ShouldBeGreaterThanOrEqualTo, before-c.span)
				So(stub.opts[0].StartTime, ShouldBeLessThanOrEqualTo, after-c.span)
			}
		})

		Convey("invalid enum values fail before querying and list the allowed values", func() {
			for _, c := range []struct {
				args    []string
				allowed string
			}{
				{[]string{"--result", "maybe"}, "success, failed"},
				{[]string{"--decision", "perhaps"}, "allow, deny"},
				{[]string{"--decider", "robot"}, "policy, user, grant"},
				{[]string{"--source", "cron"}, "ai, opsctl, desktop"},
			} {
				stub.opts = nil
				code, stderr := runListAuditStderr(context.Background(), append([]string{"audit"}, c.args...)...)

				So(code, ShouldEqual, 1)
				So(stub.opts, ShouldBeEmpty)
				So(stderr, ShouldContainSubstring, c.args[0])
				So(stderr, ShouldContainSubstring, c.allowed)
			}
		})

		Convey("an unparsable --since fails before querying", func() {
			code, stderr := runListAuditStderr(context.Background(), "audit", "--since", "7x")

			So(code, ShouldEqual, 1)
			So(stub.opts, ShouldBeEmpty)
			So(stderr, ShouldContainSubstring, "--since")
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
