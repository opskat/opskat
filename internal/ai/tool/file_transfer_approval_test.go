package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// cpTestAssetType 是一个只在测试二进制里存在的传输端点类型。
//
// 九种两端组合里只有本地端能在单测里真的读写，SSH 与 OSS 端都要一个服务端。**被拒**的
// 那一半用真适配器测得了——审批排在建连之前，连不上也到不了；**传输真的发生了**的那一半
// 需要一个能观察的远端，于是有了这个内存端点：它记下 handleCp 要求它读写的路径与字节，
// 目的路径的拼装（dst 基点 + RelPath，拼错就是把文件落到别处）与汇总输出因此可被断言。
const cpTestAssetType = "cptest"

type cpTestAdapter struct {
	entries   []helper.Entry    // List 交出的展开结果
	contents  map[string][]byte // OpenRead 的内容
	written   map[string][]byte // Write 收到的内容，按目的路径
	listed    []string          // 被要求展开的 pattern，按调用序
	validated []string          // ValidateDestination 收到的路径，按调用序
	rejectDst string            // 等于它的目的路径被判为形态错误
}

var cpFake = &cpTestAdapter{}

func init() { helper.RegisterTransferAdapter(cpTestAssetType, cpFake) }

func (a *cpTestAdapter) reset() {
	a.entries = nil
	a.contents = map[string][]byte{}
	a.written = map[string][]byte{}
	a.listed = nil
	a.validated = nil
	a.rejectDst = ""
}

func (a *cpTestAdapter) List(
	_ context.Context, _ *asset_entity.Asset, pattern string, _ bool,
) (*helper.ListResult, error) {
	a.listed = append(a.listed, pattern)
	return &helper.ListResult{Entries: a.entries}, nil
}

func (a *cpTestAdapter) OpenRead(
	_ context.Context, _ *asset_entity.Asset, p string,
) (io.ReadCloser, int64, error) {
	content, ok := a.contents[p]
	if !ok {
		return nil, 0, fmt.Errorf("cptest: no such object %q", p)
	}
	return io.NopCloser(strings.NewReader(string(content))), int64(len(content)), nil
}

func (a *cpTestAdapter) Write(
	_ context.Context, _ *asset_entity.Asset, p string, r io.Reader, _ int64,
) error {
	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	a.written[p] = content
	return nil
}

func (a *cpTestAdapter) ValidateDestination(p string) error {
	a.validated = append(a.validated, p)
	if p == a.rejectDst {
		return fmt.Errorf("cptest: %q is not a shape this endpoint can write", p)
	}
	return nil
}

func (a *cpTestAdapter) ApprovalSubject(p string, _ helper.Direction) (string, string) {
	return cpTestAssetType, p
}

// cpTestAssets 是三个测试资产：一台 SSH、一个对象存储、一个上面那个内存端点。
// OSS 的策略是**收窄过的**：默认只读组把 `object.read *` 放行，读方向就不会弹框，
// 于是"读的主体是什么"这条断言会变成一句永远成立的空话。把 allow 收到另一个桶上，
// 两个方向才都走到审批面前。
func cpTestAssets() []*asset_entity.Asset {
	return []*asset_entity.Asset{
		{ID: 1, Name: "web-01", Type: asset_entity.AssetTypeSSH},
		{ID: 2, Name: "s3-prod", Type: asset_entity.AssetTypeOSS,
			CmdPolicy: `{"allow_list":["object.read otherbucket/*"]}`},
		{ID: 3, Name: "sink-01", Type: cpTestAssetType},
		// 两个"审批被自动放行"的端点（见 cpAutoAllowedAssetType）：auto-01 用的是产品自带
		// 的默认策略（只读，读方向零弹框），auto-rw-01 是把写也放开之后的样子（两端零弹框）。
		{ID: 4, Name: "auto-01", Type: cpAutoAllowedAssetType, CmdPolicy: defaultOSSPolicyJSON()},
		{ID: 5, Name: "auto-rw-01", Type: cpAutoAllowedAssetType,
			CmdPolicy: `{"allow_list":["object.read *","object.write *"]}`},
	}
}

// setupCp 注册 mock asset repo 并返回一个记录审批项的 checker。
// decision 是审批回调的固定答复（"deny" / "allow"）。
func setupCp(t *testing.T, decision string) (context.Context, *[]permission.ApprovalItem) {
	t.Helper()
	cpFake.reset()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_asset_repo.NewMockAssetRepo(ctrl)
	for _, a := range cpTestAssets() {
		m.EXPECT().Find(gomock.Any(), a.ID).Return(a, nil).AnyTimes()
		m.EXPECT().FindByName(gomock.Any(), a.Name).Return([]*asset_entity.Asset{a}, nil).AnyTimes()
	}
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(m)
	t.Cleanup(func() {
		if orig != nil {
			asset_repo.RegisterAsset(orig)
		}
	})

	seen := &[]permission.ApprovalItem{}
	checker := permission.NewCommandPolicyChecker(
		func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
			*seen = append(*seen, items...)
			return permission.ApprovalResponse{Decision: decision}
		})
	return permission.WithPolicyChecker(context.Background(), checker), seen
}

func TestCpUploadRequiresApproval(t *testing.T) {
	Convey("本地 → SSH：审批在传输之前，被拒时一个字节都没读", t, func() {
		ctx, seen := setupCp(t, "deny")

		// 源文件**故意不存在**：审批排在展开与读取之前，所以被拒时的错误必须是拒绝，
		// 而不是一句"文件不存在"——后者会说明 handleCp 先去碰了磁盘。
		_, err := handleCp(ctx, map[string]any{
			"src": "/tmp/opskat-cp-does-not-exist",
			"dst": "web-01:/etc/cron.d/backup",
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "USER DENIED")
		So(*seen, ShouldHaveLength, 1)
		So((*seen)[0].Type, ShouldEqual, "cp")
		// 审批主体是远端路径：grant 按资产存，本地路径不属于任何资产
		So((*seen)[0].Command, ShouldEqual, "/etc/cron.d/backup")
		// 本地端不产生审批项，用户能看见"另一头是哪里"的唯一位置就是 detail
		So((*seen)[0].Detail, ShouldContainSubstring, "/tmp/opskat-cp-does-not-exist")
	})
}

func TestCpDownloadRequiresApproval(t *testing.T) {
	Convey("SSH → 本地：审批在传输之前，被拒时目的文件根本不存在", t, func() {
		ctx, seen := setupCp(t, "deny")
		dst := filepath.Join(t.TempDir(), "stolen")

		_, err := handleCp(ctx, map[string]any{
			"src": "web-01:/etc/shadow",
			"dst": dst,
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "USER DENIED")
		So(*seen, ShouldHaveLength, 1)
		So((*seen)[0].Type, ShouldEqual, "cp")
		So((*seen)[0].Command, ShouldEqual, "/etc/shadow")
		So((*seen)[0].Detail, ShouldContainSubstring, dst)

		// 零字节：连目的文件都没被创建出来。
		_, statErr := os.Stat(dst)
		So(os.IsNotExist(statErr), ShouldBeTrue)
	})
}

func TestCpOSSEndpointUsesOSSPolicySubject(t *testing.T) {
	Convey("OSS 端点按自己的策略面审批，主体是策略串", t, func() {
		Convey("目的端是写", func() {
			ctx, seen := setupCp(t, "deny")
			_, err := handleCp(ctx, map[string]any{
				"src": "/tmp/opskat-cp-does-not-exist",
				"dst": "s3-prod:/mybucket/logs/app.log",
			})
			So(err, ShouldNotBeNil)
			So(*seen, ShouldHaveLength, 1)
			So((*seen)[0].Type, ShouldEqual, asset_entity.AssetTypeOSS)
			So((*seen)[0].Command, ShouldEqual, "object.write mybucket/logs/app.log")
		})

		Convey("源端是读", func() {
			ctx, seen := setupCp(t, "deny")
			_, err := handleCp(ctx, map[string]any{
				"src": "s3-prod:/mybucket/logs/app.log",
				"dst": filepath.Join(t.TempDir(), "app.log"),
			})
			So(err, ShouldNotBeNil)
			So(*seen, ShouldHaveLength, 1)
			So((*seen)[0].Type, ShouldEqual, asset_entity.AssetTypeOSS)
			So((*seen)[0].Command, ShouldEqual, "object.read mybucket/logs/app.log")
		})
	})
}

// TestCpMalformedDestinationFailsBeforeAnyApproval 锁住目的端形态校验的**位置**。
//
// ApprovalSubject 不能报错，目的端也不经过 List，所以在这一关之前，`s3-prod:/mybucket`
// 这种指不到对象的目的地会带着一个主体走完审批、到 Write 才失败：用户被要求批准一件
// 必然失败的事，而那条主体一旦被点"始终允许"还会落成一条比本次操作更宽的常驻授权。
func TestCpMalformedDestinationFailsBeforeAnyApproval(t *testing.T) {
	Convey("形态错误的目的地在弹框之前就失败", t, func() {
		ctx, seen := setupCp(t, "deny")

		_, err := handleCp(ctx, map[string]any{
			"src": "/tmp/opskat-cp-does-not-exist",
			"dst": "s3-prod:/mybucket",
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "must name an object")
		So(*seen, ShouldHaveLength, 0)
	})
}

func TestCpRejectsLocalToLocal(t *testing.T) {
	Convey("两端都在本地不是传输面的事", t, func() {
		ctx, seen := setupCp(t, "allow")
		dir := t.TempDir()
		src := filepath.Join(dir, "a.txt")
		So(os.WriteFile(src, []byte("payload"), 0o600), ShouldBeNil)
		dst := filepath.Join(dir, "b.txt")

		_, err := handleCp(ctx, map[string]any{"src": src, "dst": dst})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "at least one endpoint")
		So(*seen, ShouldHaveLength, 0)
		_, statErr := os.Stat(dst)
		So(os.IsNotExist(statErr), ShouldBeTrue)
	})
}

// TestCpRejectsRelativeLocalPath 锁 D11：AI 侧的本地路径必须绝对。审批项与审计行只记
// 这条端点串，相对路径要靠一个串里看不见的工作目录才能定位。
func TestCpRejectsRelativeLocalPath(t *testing.T) {
	Convey("相对的本地路径被拒", t, func() {
		ctx, seen := setupCp(t, "allow")

		_, err := handleCp(ctx, map[string]any{"src": "./app.log", "dst": "web-01:/tmp/app.log"})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "must be absolute")
		So(*seen, ShouldHaveLength, 0)
	})
}

// TestCpGlobMatchingNothingIsAnError 锁住"零命中"这条裁定：三个适配器一律把零命中
// 报成空结果 + nil（判断留给调用方），cp 必须把它变成错误。Go 的 Glob 不像 shell 那样
// 展开 `*/`，一次静默的零文件"成功"正是 D19 否决的那类结果。
func TestCpGlobMatchingNothingIsAnError(t *testing.T) {
	Convey("通配一条都没命中时报错，而不是报告一次零文件的成功", t, func() {
		ctx, seen := setupCp(t, "allow")
		dir := t.TempDir()

		_, err := handleCp(ctx, map[string]any{
			"src": filepath.Join(dir, "*.log"),
			"dst": "sink-01:/logs/",
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "no files matched")
		So(*seen, ShouldHaveLength, 1)
		So(cpFake.written, ShouldBeEmpty)
	})
}

// TestCpSurfacesListErrorVerbatim 锁住"展开错误原样透出"。期望值不是抄一遍字面量，
// 而是拿同一个适配器自己产出的错误比对——两个独立来源，措辞漂移时才会红。
func TestCpSurfacesListErrorVerbatim(t *testing.T) {
	Convey("非递归的通配命中目录时，适配器那句话原样透出", t, func() {
		ctx, _ := setupCp(t, "allow")
		dir := t.TempDir()
		So(os.Mkdir(filepath.Join(dir, "sub"), 0o750), ShouldBeNil)
		pattern := filepath.Join(dir, "*")

		local, adapterErr := helper.TransferAdapterFor(nil)
		So(adapterErr, ShouldBeNil)
		_, wantErr := local.List(context.Background(), nil, pattern, false)
		So(wantErr, ShouldNotBeNil)

		_, err := handleCp(ctx, map[string]any{"src": pattern, "dst": "sink-01:/logs/"})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldEqual, wantErr.Error())
	})
}

// TestCpMultiSourceRequiresTrailingSlash 锁 D16：多源是**形态**判定（recursive 或源含
// 通配），目的地必须显式以 "/" 收尾。不复刻 POSIX cp 的目的地推断——批的是哪些具体路径，
// 写的就得是哪些。
func TestCpMultiSourceRequiresTrailingSlash(t *testing.T) {
	Convey("多源形态的目的地必须以 / 收尾", t, func() {
		ctx, seen := setupCp(t, "allow")

		_, err := handleCp(ctx, map[string]any{
			"src": "/tmp/logs", "dst": "sink-01:/backup", "recursive": true,
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, `must end with "/"`)
		So(*seen, ShouldHaveLength, 0)
		So(cpFake.written, ShouldBeEmpty)
	})
}

// TestCpExpansionIsAuthorizedBeforeItRuns 锁 D18：递归/通配的源与目的范围在 List 前授权。
func TestCpExpansionIsAuthorizedBeforeItRuns(t *testing.T) {
	Convey("展开前先审批传输范围", t, func() {
		Convey("递归的主体是源目录自己", func() {
			ctx, seen := setupCp(t, "deny")

			_, err := handleCp(ctx, map[string]any{
				"src": "web-01:/var/log", "dst": filepath.Join(t.TempDir()) + "/", "recursive": true,
			})

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "USER DENIED")
			So(*seen, ShouldHaveLength, 1)
			So((*seen)[0].Type, ShouldEqual, "cp")
			So((*seen)[0].Command, ShouldEqual, "/var/log/")
		})

		// 主体的收窄归适配器：它才知道自己那一端的规则语义。入口层先按 glob 截一刀会
		// 把这条判断从适配器手里抢走——OSS 的前缀形态 key 里 "[" 是字面量（规则侧走
		// strings.HasPrefix），截断之后基点塌成空串，指名一个前缀换来的是整桶列举授权，
		// 而 object.list 是 D20 明确不丢弃的那一类，"始终允许"会把它落成常驻 grant。
		Convey("OSS 前缀里的字面量元字符不被当成通配截掉", func() {
			ctx, seen := setupCp(t, "deny")

			_, err := handleCp(ctx, map[string]any{
				"src": "s3-prod:/mybucket/logs[1]/", "dst": filepath.Join(t.TempDir()) + "/",
				"recursive": true,
			})

			So(err, ShouldNotBeNil)
			So(*seen, ShouldHaveLength, 1)
			So((*seen)[0].Type, ShouldEqual, asset_entity.AssetTypeOSS)
			So((*seen)[0].Command, ShouldEqual, "object.read mybucket/logs[1]/")
		})

		Convey("通配的主体是通配前的最后一层目录，不是 pattern 本身", func() {
			ctx, seen := setupCp(t, "deny")

			_, err := handleCp(ctx, map[string]any{
				"src": "web-01:/var/log/*.log", "dst": filepath.Join(t.TempDir()) + "/",
			})

			So(err, ShouldNotBeNil)
			So(*seen, ShouldHaveLength, 1)
			// 通配范围收窄到其目录边界，不能把 pattern 本身误当成目录授权。
			So((*seen)[0].Command, ShouldEqual, "/var/log/")
		})
	})
}

func TestCpTransfersFileAndReportsSummary(t *testing.T) {
	Convey("本地 → 远端：字节送达，汇总报出条数与字节数", t, func() {
		ctx, seen := setupCp(t, "allow")
		src := filepath.Join(t.TempDir(), "app.log")
		So(os.WriteFile(src, []byte("hello transfer"), 0o600), ShouldBeNil)

		out, err := handleCp(ctx, map[string]any{"src": src, "dst": "sink-01:/logs/app.log"})

		So(err, ShouldBeNil)
		So(*seen, ShouldHaveLength, 1)
		So(string(cpFake.written["/logs/app.log"]), ShouldEqual, "hello transfer")

		var res cpResult
		So(json.Unmarshal([]byte(out), &res), ShouldBeNil)
		So(res.Transferred, ShouldEqual, 1)
		So(res.Bytes, ShouldEqual, len("hello transfer"))
	})
}

// TestCpMultiSourceEntryLandsUnderDestinationBase 是"落点"这条契约的锁：多源形态下
// 目的路径是 dst 基点 + RelPath，不是字面 dst。只命中一个文件的通配同样是多源——
// 按条数判形态就会把这次传输落到 `/logs/`（一个前缀）上，而不是 `/logs/app.log`。
func TestCpMultiSourceEntryLandsUnderDestinationBase(t *testing.T) {
	Convey("多源展开出的条目落在目的基点之下", t, func() {
		ctx, _ := setupCp(t, "allow")
		dir := t.TempDir()
		So(os.WriteFile(filepath.Join(dir, "app.log"), []byte("one"), 0o600), ShouldBeNil)

		_, err := handleCp(ctx, map[string]any{
			"src": filepath.Join(dir, "*.log"), "dst": "sink-01:/logs/",
		})

		So(err, ShouldBeNil)
		So(string(cpFake.written["/logs/app.log"]), ShouldEqual, "one")
	})
}

// TestCpMultiSourceValidatesTheJoinedDestination 锁住 ValidateDestination 的**入参**与
// **次序**在多源形态下的那一半（单源那一半由 TestCpMalformedDestinationFailsBeforeAnyApproval
// 锁）：范围获批并展开后，要校验实际写入的具体路径（目的基点 + RelPath）；校验基点会让
// 每次多源传输拿一个恒为前缀形态的串去撞"必须指名一个对象"这类规则。
//
// 断言的不是 fake 返回了什么，而是 handleCp 把哪个字符串交给了它；fake 只是探针，拒绝
// 哪条路径由测试指定。
func TestCpMultiSourceValidatesTheJoinedDestination(t *testing.T) {
	Convey("范围获批后逐条校验目的基点 + RelPath", t, func() {
		ctx, seen := setupCp(t, "allow")
		cpFake.rejectDst = "/logs/app.log"
		dir := t.TempDir()
		So(os.WriteFile(filepath.Join(dir, "app.log"), []byte("one"), 0o600), ShouldBeNil)

		_, err := handleCp(ctx, map[string]any{
			"src": filepath.Join(dir, "*.log"), "dst": "sink-01:/logs/",
		})

		So(err, ShouldNotBeNil)
		So(cpFake.validated, ShouldResemble, []string{"/logs/app.log"})
		// 目的范围已经审批；具体落点校验失败后没有任何字节写入。
		So(*seen, ShouldHaveLength, 1)
		So(cpFake.written, ShouldBeEmpty)
	})
}
