package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"

	. "github.com/smartystreets/goconvey/convey"
)

// cpAutoAllowedAssetType 是一个**审批主体交给 OSS 策略面**的内存端点：读写行为与 cpFake
// 完全相同（同一个实例），只有 ApprovalSubject 不同——它交出 (oss, object.read/write …)。
// 于是一个带内置默认策略的资产（builtin:oss-readonly 放行 object.read *）会把它的读判成
// Allow，一个弹框都不弹。
//
// 为什么不直接用真的 ossAdapter：那一端要一个服务端才动得起来，而这几条用例要断言的正是
// "零弹框之后本地那一头到底发生了什么"——字节落没落盘、门禁问没问过。
const cpAutoAllowedAssetType = "cpautoallowed"

type cpAutoAllowedAdapter struct{ *cpTestAdapter }

func (a cpAutoAllowedAdapter) ApprovalSubject(p string, dir helper.Direction) (string, string) {
	action := "object.read"
	switch dir {
	case helper.DirWrite:
		action = "object.write"
	case helper.DirList:
		action = "object.list"
	}
	return asset_entity.AssetTypeOSS, action + " " + strings.TrimPrefix(p, "/")
}

func init() { helper.RegisterTransferAdapter(cpAutoAllowedAssetType, cpAutoAllowedAdapter{cpFake}) }

// defaultOSSPolicyJSON 是"策略从没被人改过"的那份对象存储策略，取自产品自己的默认值而不是
// 抄一份字面量：内置只读组一旦不再放行 object.read *，下面几条用例的**前提**（远端零弹框）
// 就不成立了，那时该红的是它们自己的 So(*seen, ShouldHaveLength, 0)。
func defaultOSSPolicyJSON() string {
	var a asset_entity.Asset
	if err := a.SetOSSPolicy(asset_entity.DefaultOSSPolicy()); err != nil {
		panic(err)
	}
	return a.CmdPolicy
}

// cpWithLocalWriteGate 挂一个记录请求的 local_write 门禁，decision 是它的固定答复。
func cpWithLocalWriteGate(ctx context.Context, decision string) (context.Context, *[]LocalToolApprovalRequest) {
	reqs := &[]LocalToolApprovalRequest{}
	gate := NewLocalToolGate(func(_ context.Context, req LocalToolApprovalRequest) permission.ApprovalResponse {
		*reqs = append(*reqs, req)
		return permission.ApprovalResponse{Decision: decision}
	})
	return helper.WithLocalWriteGate(ctx, gate), reqs
}

// TestCpAutoAllowedTransferGatesTheLocalWrite 锁住 §6.2「本地端点不产生审批项」那条规则的
// **前提**：D11 说本地路径由"用户批准的那条命令串"完全决定，可两端都被策略/grant 自动放行
// 时压根没有那条串——一个弹框都没弹过。这一档下本地写改由 local_write 门禁把关。
//
// 复现过的洞：`cp(src="s3-prod:/b/k", dst="/Users/me/.ssh/authorized_keys")` 在一个谁也没
// 改过策略的对象存储资产上零交互跑完，事后只在 audit_logs 里留一行。
func TestCpAutoAllowedTransferGatesTheLocalWrite(t *testing.T) {
	Convey("两端都被自动放行时，本地落点要过一次 local_write 门禁", t, func() {
		Convey("门禁拒绝：报错，源端一个字节都没被读", func() {
			ctx, seen := setupCp(t, "deny")
			ctx, reqs := cpWithLocalWriteGate(ctx, "deny")
			seedCpSource("a.log")
			dst := filepath.Join(t.TempDir(), "authorized_keys")

			_, err := handleCp(ctx, map[string]any{"src": "auto-01:/src/a.log", "dst": dst})

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "USER DENIED")
			// 前提：远端那一端是 Allow，没有任何审批项进过对话框。
			So(*seen, ShouldHaveLength, 0)
			So(*reqs, ShouldHaveLength, 1)
			So((*reqs)[0].ToolName, ShouldEqual, LocalWriteToolName)
			So((*reqs)[0].Command, ShouldEqual, dst)
			So((*reqs)[0].SubCommands, ShouldResemble, []string{dst})
			// 门禁排在展开与传输之前：被拒时源端连列都没被列过。
			So(cpFake.listed, ShouldBeEmpty)
			_, statErr := os.Stat(dst)
			So(os.IsNotExist(statErr), ShouldBeTrue)
		})

		Convey("门禁放行：字节照旧送达，只问一次", func() {
			ctx, seen := setupCp(t, "deny")
			ctx, reqs := cpWithLocalWriteGate(ctx, "allow")
			seedCpSource("a.log")
			dst := filepath.Join(t.TempDir(), "app.log")

			_, err := handleCp(ctx, map[string]any{"src": "auto-01:/src/a.log", "dst": dst})

			So(err, ShouldBeNil)
			So(*seen, ShouldHaveLength, 0)
			So(*reqs, ShouldHaveLength, 1)
			content, readErr := os.ReadFile(dst) //nolint:gosec // 测试自己造的临时路径
			So(readErr, ShouldBeNil)
			So(string(content), ShouldEqual, "a.log")
		})

		// 门禁守的是**本地写**。目的端在资产上时它自己有审批主体，再多问一次本地门禁
		// 既问错了对象（那条路径不在本机），也会把一次自动放行的远端传输变成弹框。
		Convey("目的端在资产上时不问本地门禁", func() {
			ctx, seen := setupCp(t, "deny")
			ctx, reqs := cpWithLocalWriteGate(ctx, "deny")
			seedCpSource("a.log")

			// auto-rw-01 的策略连写也放行，因此这次传输两端都是 Allow、一个弹框都没有——
			// 恰好是上面那条用例的触发条件，唯一的差别就是目的端不在本地。
			_, err := handleCp(ctx, map[string]any{
				"src": "auto-rw-01:/src/a.log", "dst": "auto-rw-01:/backup/a.log",
			})

			So(err, ShouldBeNil)
			So(*seen, ShouldHaveLength, 0)
			So(*reqs, ShouldHaveLength, 0)
			So(string(cpFake.written["/backup/a.log"]), ShouldEqual, "a.log")
		})
	})
}

// TestCpAutoAllowedRecursiveGatesEveryLandingPathAtOnce：多源形态下门禁看到的是**展开后
// 的每一条落点**，一次问完。给一条 dst 基点等于让用户批一片目录，而批的必须是真正会被
// 写出来的那些路径（D16/D17 在本地这一侧的同一条要求）。
func TestCpAutoAllowedRecursiveGatesEveryLandingPathAtOnce(t *testing.T) {
	Convey("递归形态下每条落点都进同一次本地门禁", t, func() {
		ctx, seen := setupCp(t, "deny")
		ctx, reqs := cpWithLocalWriteGate(ctx, "deny")
		seedCpSource("a.log", "b.log")
		dir := t.TempDir()

		_, err := handleCp(ctx, map[string]any{
			"src": "auto-01:/src/", "dst": dir + "/", "recursive": true,
		})

		So(err, ShouldNotBeNil)
		So(*seen, ShouldHaveLength, 0)
		So(*reqs, ShouldHaveLength, 1)
		So((*reqs)[0].SubCommands, ShouldResemble, []string{dir + "/"})
		So(cpFake.written, ShouldBeEmpty)
		_, statErr := os.Stat(filepath.Join(dir, "a.log"))
		So(os.IsNotExist(statErr), ShouldBeTrue)
	})
}

// TestCpSkipsTheLocalWriteGateAfterAnApprovalDialog 锁住规则本身没被改掉：只要这次 cp 真的
// 弹过框，本地端就照旧不产生第二次审批（spec §6.2）——用户批准的那条串里已经写着两端原文。
func TestCpSkipsTheLocalWriteGateAfterAnApprovalDialog(t *testing.T) {
	Convey("弹过框的传输不再追问本地门禁", t, func() {
		ctx, seen := setupCp(t, "allow")
		ctx, reqs := cpWithLocalWriteGate(ctx, "deny")
		seedCpSource("a.log")
		dst := filepath.Join(t.TempDir(), "a.log")

		_, err := handleCp(ctx, map[string]any{"src": "sink-01:/src/a.log", "dst": dst})

		So(err, ShouldBeNil)
		So(*seen, ShouldHaveLength, 1)
		So(*reqs, ShouldHaveLength, 0)
		content, readErr := os.ReadFile(dst) //nolint:gosec // 测试自己造的临时路径
		So(readErr, ShouldBeNil)
		So(string(content), ShouldEqual, "a.log")
	})
}

// TestCpLocalWriteGateMissingIsNotAPass：门禁没接上时不许放行。与 permission.RequireChecker
// 同一条理由（#249）——"注入缺失 == 放行"是这条分支反复在修的那类 fail-open，而走到这里时
// 门禁已经是仅剩的那道门。
func TestCpLocalWriteGateMissingIsNotAPass(t *testing.T) {
	Convey("本地写门禁没接上时整次传输失败", t, func() {
		ctx, seen := setupCp(t, "deny")
		seedCpSource("a.log")
		dst := filepath.Join(t.TempDir(), "authorized_keys")

		_, err := handleCp(ctx, map[string]any{"src": "auto-01:/src/a.log", "dst": dst})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "local write gate")
		So(*seen, ShouldHaveLength, 0)
		_, statErr := os.Stat(dst)
		So(os.IsNotExist(statErr), ShouldBeTrue)
	})
}

// TestCpPreapprovedLocalWriteNeedsNoGate：opsctl 那条路上没有 AI 会话，也没有门禁可问——
// 用户自己在终端敲的那条命令串里就写着本地路径，D11 的前提在这里本来就成立。
func TestCpPreapprovedLocalWriteNeedsNoGate(t *testing.T) {
	Convey("已预检的调用方（opsctl）不经过本地写门禁", t, func() {
		ctx := setupCpPreapproved(t)
		seedCpSource("a.log")
		dst := filepath.Join(t.TempDir(), "a.log")

		_, err := handleCp(ctx, map[string]any{"src": "sink-01:/src/a.log", "dst": dst})

		So(err, ShouldBeNil)
		content, readErr := os.ReadFile(dst) //nolint:gosec // 测试自己造的临时路径
		So(readErr, ShouldBeNil)
		So(string(content), ShouldEqual, "a.log")
	})
}

// cpApprovalCall 是一次审批对话框调用：kind 与这一次送进去的全部条目。
//
// 批量审批的契约是"多少条主体进了**同一次**对话框"，把 items 摊平记录就看不出一次 6 条
// 与六次 1 条的区别——而"逐条弹 N 次框"正是 D17 明确否决掉的那个形态。
type cpApprovalCall struct {
	kind  string
	items []permission.ApprovalItem
}

// setupCpDialogs 复用 file_transfer_approval_test.go 的资产 mock 与内存端点（cpFake），
// 只把审批回调换成按**调用**记录的版本（后挂的 checker 覆盖前一个）。
//
// policyDecision 用于 setupCp 的普通策略回调，batchDecision 用于目录范围批量审批。
func setupCpDialogs(t *testing.T, policyDecision, batchDecision string) (context.Context, *[]cpApprovalCall) {
	t.Helper()
	ctx, _ := setupCp(t, policyDecision)

	calls := &[]cpApprovalCall{}
	checker := permission.NewCommandPolicyChecker(
		func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
			*calls = append(*calls, cpApprovalCall{kind: kind, items: items})
			if kind == permission.ApprovalKindBatch {
				return permission.ApprovalResponse{Decision: batchDecision}
			}
			return permission.ApprovalResponse{Decision: policyDecision}
		})
	return permission.WithPolicyChecker(ctx, checker), calls
}

// cpBatchCalls 过滤出批量审批的那些调用。
func cpBatchCalls(calls []cpApprovalCall) []cpApprovalCall {
	out := []cpApprovalCall{}
	for _, c := range calls {
		if c.kind == permission.ApprovalKindBatch {
			out = append(out, c)
		}
	}
	return out
}

// cpItemCommands 取出审批项的主体串，保持送进对话框时的顺序。
func cpItemCommands(items []permission.ApprovalItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Command)
	}
	return out
}

// seedCpSource 让内存端点把 /src/<name> 逐条交出来并可读，内容就是 name 自己。
func seedCpSource(names ...string) {
	for _, name := range names {
		p := "/src/" + name
		cpFake.entries = append(cpFake.entries, helper.Entry{Path: p, RelPath: name, Size: int64(len(name))})
		cpFake.contents[p] = []byte(name)
	}
}

// cpSourceNames 造 n 个条目名，用来验证范围审批不限制展开条数。
func cpSourceNames(n int) []string {
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("f%03d.log", i))
	}
	return names
}

// setupCpPreapproved 造 opsctl 那条路上的 context：审批已经在工具之外走完，因此 context 里
// 没有 PolicyChecker，只有 WithPreapproved 标记（cmd/opsctl/command/handler.go 的 callHandler）。
// 复用 setupCp 只为它注册的 mock asset repo 与 cpFake 复位——它返回的那个带 checker 的
// context 正是这条路上**不**该有的东西。
func setupCpPreapproved(t *testing.T) context.Context {
	t.Helper()
	setupCp(t, "allow")
	return permission.WithPreapproved(context.Background())
}

// 范围审批被拒时不得连接、展开或传输任何字节。
func TestCpMultiSourceRejectsDeniedDirectoryScopesBeforeListing(t *testing.T) {
	Convey("源与目的范围在同一批审批中被拒", t, func() {
		ctx, calls := setupCpDialogs(t, "allow", "deny")
		seedCpSource("a.log", "b.log")

		_, err := handleCp(ctx, map[string]any{
			"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "USER DENIED")

		batch := cpBatchCalls(*calls)
		So(batch, ShouldHaveLength, 1)
		So(cpItemCommands(batch[0].items), ShouldResemble, []string{"/src/", "/backup/"})
		So(cpFake.listed, ShouldBeEmpty)
		So(cpFake.written, ShouldBeEmpty)
	})
}

func TestCpRecursiveApprovesDirectoryScopesBeforeListing(t *testing.T) {
	Convey("recursive cp approves the source and destination directories without listing them first", t, func() {
		ctx, calls := setupCpDialogs(t, "deny", "deny")
		seedCpSource("a.log", "nested/b.log")

		_, err := handleCp(ctx, map[string]any{
			"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
		})

		So(err, ShouldNotBeNil)
		So(cpFake.listed, ShouldBeEmpty)
		batch := cpBatchCalls(*calls)
		So(batch, ShouldHaveLength, 1)
		So(cpItemCommands(batch[0].items), ShouldResemble, []string{"/src/", "/backup/"})
	})
}

// TestCpMultiSourceRefusesEntriesEscapingTheDestination 锁住落点的**边界**：目的路径是
// 目的基点拼上 RelPath，那条 RelPath 必须真的落在基点之下。
//
// 对象存储的 key 是一段不透明的字节串，`logs/../../id_rsa` 是一个合法的 S3 key，相对
// `logs/` 前缀展开出来的落点就是 `../../id_rsa`。目的端在本地时没有任何东西挡它：
// localAdapter.ValidateDestination 明写"本地文件系统对写入目标没有形态约束"，Write 又会
// 按需建父目录，于是这一条落到用户指名的目的地之外，汇总还照样报 transferred。
// 用户批准的是清单上的那些路径，写的就必须是那些（D16 / D17）。
func TestCpMultiSourceRefusesEntriesEscapingTheDestination(t *testing.T) {
	Convey("展开出的落点走出目的基点时，在审批之前就报错", t, func() {
		ctx, calls := setupCpDialogs(t, "allow", "allow")
		cpFake.entries = append(cpFake.entries,
			helper.Entry{Path: "/src/x", RelPath: "../escaped.log", Size: 3})
		cpFake.contents["/src/x"] = []byte("pwn")
		root := t.TempDir()
		dst := filepath.Join(root, "out")
		So(os.MkdirAll(dst, 0o750), ShouldBeNil)

		_, err := handleCp(ctx, map[string]any{
			"src": "sink-01:/src/", "dst": dst + "/", "recursive": true,
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "../escaped.log")
		// 排在批量审批之前：一份清单里混进一条落到别处的路径，用户批准的和发生的就
		// 不是一件事，事后再报错也已经晚了。
		So(cpBatchCalls(*calls), ShouldHaveLength, 1)
		_, statErr := os.Stat(filepath.Join(root, "escaped.log"))
		So(os.IsNotExist(statErr), ShouldBeTrue)
	})
}

// TestCpMultiSourceTransfersEveryEntry 锁多源批准之后的另一半：每一条都真的送达，
// 汇总报的是总条数与总字节，而不是第一条的。
func TestCpMultiSourceTransfersEveryEntry(t *testing.T) {
	Convey("批准后每条展开路径都被传输，汇总覆盖全部条目", t, func() {
		ctx, calls := setupCpDialogs(t, "allow", "allow")
		seedCpSource("a.log", "bb.log")

		out, err := handleCp(ctx, map[string]any{
			"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
		})

		So(err, ShouldBeNil)
		So(cpBatchCalls(*calls), ShouldHaveLength, 1)
		So(string(cpFake.written["/backup/a.log"]), ShouldEqual, "a.log")
		So(string(cpFake.written["/backup/bb.log"]), ShouldEqual, "bb.log")

		var res cpResult
		So(json.Unmarshal([]byte(out), &res), ShouldBeNil)
		So(res.Transferred, ShouldEqual, 2)
		So(res.Bytes, ShouldEqual, len("a.log")+len("bb.log"))
	})
}

// 源与目的范围逐字相同时，审批项应去重。
func TestCpApprovalSubjectsAreDeduplicated(t *testing.T) {
	Convey("重复的主体在批量审批里只出现一次", t, func() {
		ctx, calls := setupCpDialogs(t, "allow", "deny")
		seedCpSource("a.log")

		_, err := handleCp(ctx, map[string]any{
			"src": "sink-01:/src/", "dst": "sink-01:/src/", "recursive": true,
		})

		So(err, ShouldNotBeNil)
		batch := cpBatchCalls(*calls)
		So(batch, ShouldHaveLength, 1)
		So(cpItemCommands(batch[0].items), ShouldResemble, []string{"/src/"})
	})
}

func TestCpRangeApprovalDoesNotCapExpandedEntries(t *testing.T) {
	Convey("范围获批后不按展开条数截断", t, func() {
		Convey("201 条全部传输", func() {
			ctx, calls := setupCpDialogs(t, "allow", "allow")
			seedCpSource(cpSourceNames(201)...)

			_, err := handleCp(ctx, map[string]any{
				"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
			})

			So(err, ShouldBeNil)
			So(cpBatchCalls(*calls), ShouldHaveLength, 1)
			So(cpFake.written, ShouldHaveLength, 201)
		})

		Convey("恰好 200 条仍然放行", func() {
			ctx, calls := setupCpDialogs(t, "allow", "allow")
			seedCpSource(cpSourceNames(200)...)

			_, err := handleCp(ctx, map[string]any{
				"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
			})

			So(err, ShouldBeNil)
			So(cpBatchCalls(*calls), ShouldHaveLength, 1)
			So(cpFake.written, ShouldHaveLength, 200)
		})
	})
}

func TestCpReportsEachCompletedEntryToAnInteractiveCaller(t *testing.T) {
	Convey("recursive cp reports one progress event after each successful entry", t, func() {
		ctx := setupCpPreapproved(t)
		seedCpSource("a.log", "bb.log")
		// 枚举元数据可能在真正打开文件前已经过期；进度必须报告实际流过的字节。
		cpFake.entries[0].Size = 500
		cpFake.entries[1].Size = 600
		var progress []CpProgress
		ctx = WithCpProgressObserver(ctx, func(event CpProgress) {
			progress = append(progress, event)
		})

		_, err := handleCp(ctx, map[string]any{
			"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
		})

		So(err, ShouldBeNil)
		So(progress, ShouldResemble, []CpProgress{
			{Completed: 1, Total: 2, Src: "/src/a.log", Dst: "/backup/a.log", Bytes: 5},
			{Completed: 2, Total: 2, Src: "/src/bb.log", Dst: "/backup/bb.log", Bytes: 6},
		})
	})
}

// TestCpGlobFormRequiresTrailingSlash 锁 D16 在通配形态上的那一半：多源判的是**形态**
// （recursive 为真，或源含 `* ? [`），不是单看 recursive 那一个布尔。cmd/opsctl/command
// 那一侧曾经把判定收窄成只看 recursive（487f2aa0），同一个变异如果发生在这里，
// `cp(src="sink-01:/src/*.log", dst="sink-01:/backup")` 会安静地把展开出的每个条目都拼到
// 字面量 "backup" 后面，而不是报错——落点从此不再纯由输入决定。
func TestCpGlobFormRequiresTrailingSlash(t *testing.T) {
	Convey("通配形态（未设 recursive）的目的地同样必须以 / 收尾", t, func() {
		ctx, seen := setupCp(t, "allow")

		_, err := handleCp(ctx, map[string]any{
			"src": "sink-01:/src/*.log", "dst": "sink-01:/backup",
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, `must end with "/"`)
		// 排在展开与任何审批之前：源端一次都没被要求列出，用户也没看见过任何弹框。
		So(*seen, ShouldHaveLength, 0)
		So(cpFake.listed, ShouldBeEmpty)
		So(cpFake.written, ShouldBeEmpty)
	})
}

// TestCpMultiSourceFastFailsAndReportsProgress 锁 D19 的快速失败：任一条出错立即中止，
// 报出已传输 N/M 并点名失败的那条。不是 POSIX cp 的"继续并最终非零"——每个已传输的字节
// 都是一次已批准的副作用，出意外后继续会留下一个看起来完整、实际残缺的目的地。
func TestCpMultiSourceFastFailsAndReportsProgress(t *testing.T) {
	Convey("首个传输失败即中止，报出 N/M 与失败条目", t, func() {
		ctx, _ := setupCpDialogs(t, "allow", "allow")
		seedCpSource("a.log", "b.log", "c.log")
		delete(cpFake.contents, "/src/b.log") // 第二条读不出来
		dir := t.TempDir()

		_, err := handleCp(ctx, map[string]any{
			"src": "sink-01:/src/", "dst": dir + "/", "recursive": true,
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "1/3")
		So(err.Error(), ShouldContainSubstring, "/src/b.log")

		// 第一条已经落地——那是一次已批准的副作用，报出来而不是假装没发生；
		// 第三条则根本没被尝试。
		first, readErr := os.ReadFile(filepath.Join(dir, "a.log")) //nolint:gosec // 测试自己造的临时路径
		So(readErr, ShouldBeNil)
		So(string(first), ShouldEqual, "a.log")
		_, statErr := os.Stat(filepath.Join(dir, "c.log"))
		So(os.IsNotExist(statErr), ShouldBeTrue)
	})
}
