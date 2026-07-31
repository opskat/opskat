package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"

	. "github.com/smartystreets/goconvey/convey"
)

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
// 两个 decision 对应 §6.5 的两段：展开授权（DirList，单条审批）与传输授权（批量）。
// 多源用例几乎都要"先让展开过去，再看批量那一次"，只给一个答复就到不了第二段。
func setupCpDialogs(t *testing.T, expandDecision, transferDecision string) (context.Context, *[]cpApprovalCall) {
	t.Helper()
	ctx, _ := setupCp(t, expandDecision)

	calls := &[]cpApprovalCall{}
	checker := permission.NewCommandPolicyChecker(
		func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
			*calls = append(*calls, cpApprovalCall{kind: kind, items: items})
			if kind == permission.ApprovalKindBatch {
				return permission.ApprovalResponse{Decision: transferDecision}
			}
			return permission.ApprovalResponse{Decision: expandDecision}
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

// cpSourceNames 造 n 个条目名，用来把展开条数顶到 200 条上限的两侧。
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

// TestCpApprovedListingIsTransferredWithoutReExpanding 锁住 TOCTOU 那条缝被关掉：调用方
// 交来的清单就是它已经逐条审批过的那一份，工具照它传输、**不再自己展开**。
//
// opsctl 展开一次算审批主体、工具再展开一次实际传输，两次之间源端新出现的文件会被传输
// 而从未进过审批清单——违反 spec 第 9 行的硬不变式。
func TestCpApprovedListingIsTransferredWithoutReExpanding(t *testing.T) {
	Convey("透传已批准的清单时，工具照它传输", t, func() {
		Convey("清单之外的条目一个字节都不动，也不重新展开", func() {
			ctx := setupCpPreapproved(t)
			// 源端此刻比审批时多了一个 b.log：它可读、也会被重新展开命中，但它没进过
			// 任何一份审批清单。
			seedCpSource("a.log", "b.log")

			out, err := handleCp(ctx, map[string]any{
				"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
				CpApprovedListingArg: &helper.ListResult{Entries: []helper.Entry{
					{Path: "/src/a.log", RelPath: "a.log", Size: int64(len("a.log"))},
				}},
			})

			So(err, ShouldBeNil)
			// 一次展开都没发生：重新展开会把 b.log 一起传走。
			So(cpFake.listed, ShouldBeEmpty)
			So(cpFake.written, ShouldHaveLength, 1)
			So(string(cpFake.written["/backup/a.log"]), ShouldEqual, "a.log")

			var res cpResult
			So(json.Unmarshal([]byte(out), &res), ShouldBeNil)
			So(res.Transferred, ShouldEqual, 1)
		})

		// 单源形态写的是 dst 字面量、没有基点拼接，一份按 RelPath 算落点的清单在那里无处可用。
		// 收下它再安静地忽略，等于工具又自己展开了一次——正是这个参数要关掉的那件事。
		Convey("清单与单源形态对不上时报错，而不是安静地重新展开", func() {
			ctx := setupCpPreapproved(t)
			seedCpSource("a.log")

			_, err := handleCp(ctx, map[string]any{
				"src": "sink-01:/src/a.log", "dst": "sink-01:/backup/a.log",
				CpApprovedListingArg: &helper.ListResult{Entries: []helper.Entry{
					{Path: "/src/a.log", RelPath: "a.log", Size: 5},
				}},
			})

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, CpApprovedListingArg)
			So(cpFake.written, ShouldBeEmpty)
		})
	})
}

// TestCpApprovedListingStillObeysTheGuards：清单来自可信调用方**不等于**可以少做检查——
// 容器性守卫（落点不许走出目的基点）与 200 条上限照旧执行。跳过它们就是本分支反复在修的
// 那类 fail-open。
//
// 两个用例都在源端另放一条干净的条目：工具若忽略清单转而重新展开，它们都会变成一次安静的
// 成功，而不是"错误恰好也出现了"。
func TestCpApprovedListingStillObeysTheGuards(t *testing.T) {
	Convey("透传的清单照样过守卫", t, func() {
		Convey("落点走出目的基点时报错，一个字节都不传", func() {
			ctx := setupCpPreapproved(t)
			seedCpSource("ok.log")
			root := t.TempDir()
			dst := filepath.Join(root, "out")
			So(os.MkdirAll(dst, 0o750), ShouldBeNil)

			_, err := handleCp(ctx, map[string]any{
				"src": "sink-01:/src/", "dst": dst + "/", "recursive": true,
				CpApprovedListingArg: &helper.ListResult{Entries: []helper.Entry{
					{Path: "/src/ok.log", RelPath: "../escaped.log", Size: 6},
				}},
			})

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "../escaped.log")
			_, statErr := os.Stat(filepath.Join(root, "escaped.log"))
			So(os.IsNotExist(statErr), ShouldBeTrue)
		})

		Convey("超过 200 条时报错，不截断也不传输", func() {
			ctx := setupCpPreapproved(t)
			seedCpSource("ok.log")
			entries := make([]helper.Entry, 0, 201)
			for _, name := range cpSourceNames(201) {
				entries = append(entries, helper.Entry{Path: "/src/" + name, RelPath: name})
			}

			_, err := handleCp(ctx, map[string]any{
				"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
				CpApprovedListingArg: &helper.ListResult{Entries: entries},
			})

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "201")
			So(err.Error(), ShouldContainSubstring, "200")
			So(cpFake.written, ShouldBeEmpty)
		})
	})
}

// TestCpMultiSourceApprovesEveryExpandedPathAtOnce 是硬不变式本体（spec §6.5 第二段 / D17）：
// 展开出的每一条都作为独立主体进入**同一次**批量审批，被拒时零字节被读写。
func TestCpMultiSourceApprovesEveryExpandedPathAtOnce(t *testing.T) {
	Convey("展开出的每条路径都是独立主体，一次性进同一个对话框", t, func() {
		ctx, calls := setupCpDialogs(t, "allow", "deny")
		seedCpSource("a.log", "b.log")

		_, err := handleCp(ctx, map[string]any{
			"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "USER DENIED")

		batch := cpBatchCalls(*calls)
		So(batch, ShouldHaveLength, 1)
		// 源读与目的写成对出现：一条待批准的传输是"从这里读、写到那里"，把两半拆到
		// 一份两百行清单的首尾两段，就没法逐条审阅它到底要把哪个文件送到哪里。
		So(cpItemCommands(batch[0].items), ShouldResemble, []string{
			"/src/a.log", "/backup/a.log", "/src/b.log", "/backup/b.log",
		})
		So(cpFake.written, ShouldBeEmpty)
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
		So(cpBatchCalls(*calls), ShouldHaveLength, 0)
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

// TestCpApprovalSubjectsAreDeduplicated：源与目的落在同一个前缀上时，一条 entry 的读主体
// 与写主体逐字相同。重复条目让用户在同一份清单里读两遍同一句话，查一遍策略也是白查
// （200 条上限数的是展开出的 entry，不是审批项，重复条目吃不掉它）。
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
		So(cpItemCommands(batch[0].items), ShouldResemble, []string{"/src/a.log"})
	})
}

// TestCpApprovalCapRefusesToTruncate 锁 D19 的 200 条上限：超出即报错并报出实际条数，
// **绝不静默截断**——一次看起来成功、实际只复制了一部分的传输比报错糟得多。
func TestCpApprovalCapRefusesToTruncate(t *testing.T) {
	Convey("展开条数撞上审批上限", t, func() {
		Convey("超过 200 条时报错，一个字节都不传", func() {
			ctx, calls := setupCpDialogs(t, "allow", "allow")
			seedCpSource(cpSourceNames(201)...)

			_, err := handleCp(ctx, map[string]any{
				"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true,
			})

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "201")
			So(err.Error(), ShouldContainSubstring, "200")
			So(cpBatchCalls(*calls), ShouldHaveLength, 0)
			So(cpFake.written, ShouldBeEmpty)
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
