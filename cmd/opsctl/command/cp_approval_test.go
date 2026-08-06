package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/sshpool"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type recordingAuditRepo struct {
	logs []*audit_entity.AuditLog
}

func (r *recordingAuditRepo) Create(_ context.Context, log *audit_entity.AuditLog) error {
	entry := *log
	r.logs = append(r.logs, &entry)
	return nil
}

func (r *recordingAuditRepo) List(context.Context, audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	return nil, 0, nil
}

func (r *recordingAuditRepo) ListSessions(context.Context, int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

// cpTestRemoteType 是一个只在测试二进制里存在的传输端点类型（同一套路的先例：
// internal/ai/tool/file_transfer_approval_test.go 的 cptest）。
//
// 它存在是因为远端源的审批范围与展开顺序在这个包里没有别的办法观察：SSH 端要一台真机、
// 对象存储端要一个服务端。这个内存端点让测试确认范围审批发生在任何远端 List 之前。
const cpTestRemoteType = "cptestremote"

type cpFakeAdapter struct {
	entries []helper.Entry
	// byPattern 让每条源交出各自的展开结果。N 个显式源的用例需要它：共用一份 entries
	// 会把"每个源展开出的那一条"折叠成同一条主体，于是"每个源都进了审批"这句断言
	// 只剩一条可断。
	byPattern map[string][]helper.Entry
	// listed 按顺序记下每一次 List 的 pattern。**opsctl 什么时候碰远端、碰了几次**本身
	// 就是契约：指名的源一次都不该碰（那是一次未经授权、未经审计的远端探测），枚举形态
	// 只该展开一次。没有这份记录，两者都只能靠"结果碰巧一样"来判断。
	listed []string
	// appearsLater 是"两次展开之间源端新出现的文件"：第二次及以后的 List 会多交出它。
	// 它是 TOCTOU 那条缝的探针——只展开一次的实现永远看不到它。
	appearsLater []helper.Entry
	resolved     []string
}

var cpFakeRemote = &cpFakeAdapter{}

func init() { helper.RegisterTransferAdapter(cpTestRemoteType, cpFakeRemote) }

func (a *cpFakeAdapter) List(
	_ context.Context, _ *asset_entity.Asset, pattern string, _ bool,
) (*helper.ListResult, error) {
	a.listed = append(a.listed, pattern)
	if entries, ok := a.byPattern[pattern]; ok {
		return &helper.ListResult{Entries: entries}, nil
	}
	if len(a.listed) > 1 && len(a.appearsLater) > 0 {
		grown := make([]helper.Entry, 0, len(a.entries)+len(a.appearsLater))
		return &helper.ListResult{Entries: append(append(grown, a.entries...), a.appearsLater...)}, nil
	}
	return &helper.ListResult{Entries: a.entries}, nil
}

// resetCpFakeRemote 清空那个内存端点并在用例结束后还原。它是全局的：上一条用例留下的
// 调用记录会让"碰了几次远端"这类断言读到别人的账。
func resetCpFakeRemote(t *testing.T) {
	t.Helper()
	cpFakeRemote.entries = nil
	cpFakeRemote.byPattern = nil
	cpFakeRemote.listed = nil
	cpFakeRemote.appearsLater = nil
	t.Cleanup(func() { *cpFakeRemote = cpFakeAdapter{} })
}

func (a *cpFakeAdapter) OpenRead(
	_ context.Context, _ *asset_entity.Asset, _ string,
) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("cptestremote: opsctl never transfers by itself; that is the cp tool's job")
}

func (a *cpFakeAdapter) Write(_ context.Context, _ *asset_entity.Asset, _ string, _ io.Reader, _ int64) error {
	return errors.New("cptestremote: opsctl never transfers by itself; that is the cp tool's job")
}

func (a *cpFakeAdapter) ValidateDestination(string) error { return nil }

func (a *cpFakeAdapter) ResolveTransferPath(
	_ context.Context, _ *asset_entity.Asset, p string,
) (string, error) {
	a.resolved = append(a.resolved, p)
	if p == "~/upload.txt" {
		return "/home/deploy/upload.txt", nil
	}
	return p, nil
}

// ApprovalSubject 按方向给出不同的主体：读与写的主体分不开时，"每条展开出的路径都进了
// 审批"这句断言就分不出它断的是哪一半。
func (a *cpFakeAdapter) ApprovalSubject(p string, dir helper.Direction) (string, string) {
	action := "read"
	switch dir {
	case helper.DirWrite:
		action = "write"
	case helper.DirList:
		action = "list"
	}
	return cpTestRemoteType, action + " " + p
}

// registerCpTestAsset 注册一个 mock asset repo：assetID=1 是 SSH 资产、assetID=2 是对象
// 存储资产、assetID=3 是上面那个内存端点，让 "1:/path" 与 "2:/bucket/key" 两种端点串都能
// 解析（cp 的两端各有三种形态，只有 SSH 一种时测不到"任一端是对象存储就不走 proxy"
// 这类分派），而 "3:/path" 交出一份可控的展开结果。
func registerCpTestAsset(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
		Return(&asset_entity.Asset{ID: 1, Name: "web-01", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()
	mockAsset.EXPECT().Find(gomock.Any(), int64(2)).
		Return(&asset_entity.Asset{ID: 2, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}, nil).AnyTimes()
	mockAsset.EXPECT().Find(gomock.Any(), int64(3)).
		Return(&asset_entity.Asset{ID: 3, Name: "sink-01", Type: cpTestRemoteType}, nil).AnyTimes()
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() {
		if orig != nil {
			asset_repo.RegisterAsset(orig)
		}
	})
}

// stubCpHandler 交出一个记录调用参数的 cp 工具 handler，opsctl 的接线断言全靠它：
// 传输本身在别处（internal/ai/tool 的 handleCp）有覆盖，这里要钉的是"opsctl 把哪些
// 端点串、以什么工具名交出去"。
func stubCpHandler(calls *[]map[string]any) map[string]tool.ToolHandlerFunc {
	return map[string]tool.ToolHandlerFunc{
		"cp": func(_ context.Context, args map[string]any) (string, error) {
			*calls = append(*calls, args)
			return `{"transferred":1,"bytes":7}`, nil
		},
	}
}

// stubCpBatchApproval 替换掉批量审批入口，记录被送去审批的主体。
func stubCpBatchApproval(t *testing.T, seen *[][]cpSubject, result ApprovalResult, err error) {
	orig := cpBatchApprovalFn
	cpBatchApprovalFn = func(_ context.Context, subjects []cpSubject, _ string) (ApprovalResult, error) {
		*seen = append(*seen, subjects)
		return result, err
	}
	t.Cleanup(func() { cpBatchApprovalFn = orig })
}

func subjectCommands(subjects []cpSubject) []string {
	out := make([]string, 0, len(subjects))
	for _, s := range subjects {
		out = append(out, s.approvalType+" "+s.command)
	}
	return out
}

// writeCpFixture 写一个本地文件（必要时建父目录），返回它的绝对路径。
func writeCpFixture(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestCmdCpRequiresApproval(t *testing.T) {
	// 本地源文件必须真实存在：最后那条"批准后传输照常发起"的反证走到了真正的
	// 上传路径，cmdCp 会打开它。此前这里写死 /tmp/payload，于是这条断言只在
	// 恰好有人在 /tmp 下留过同名文件的机器上通过，干净机器上恒红。
	localPath := writeCpFixture(t, filepath.Join(t.TempDir(), "payload"))

	// 隔离桌面端 SSH proxy 探测：本机开着 opskat 时 cmdCp 会走 proxy 分支，
	// 下面被 stub 的 handler 一次都不会被调到。
	origProxyFn := cpSSHProxyClientFn
	cpSSHProxyClientFn = func() *sshpool.Client { return nil }
	t.Cleanup(func() { cpSSHProxyClientFn = origProxyFn })

	Convey("cp 被拒时不得发起任何传输", t, func() {
		registerCpTestAsset(t)
		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		var calls []map[string]any
		handlers := stubCpHandler(&calls)
		called := func() bool { return len(calls) > 0 }

		var seen []approval.ApprovalRequest
		origApproval := cpApprovalFn
		cpApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
			seen = append(seen, req)
			return ApprovalResult{Decision: aictx.Deny}, errors.New("user denied")
		}
		defer func() { cpApprovalFn = origApproval }()

		Convey("上传：审批主体是目的端路径", func() {
			exitCode := cmdCp(context.Background(), handlers, []string{localPath, "1:/etc/cron.d/backup"}, "")

			So(exitCode, ShouldEqual, 1)
			So(called(), ShouldBeFalse)
			So(seen, ShouldHaveLength, 1)
			So(seen[0].Type, ShouldEqual, "cp")
			So(seen[0].AssetID, ShouldEqual, int64(1))
			So(seen[0].Command, ShouldEqual, "/etc/cron.d/backup")
		})

		Convey("下载：审批主体是源端路径", func() {
			exitCode := cmdCp(context.Background(), handlers, []string{"1:/etc/shadow", "/tmp/stolen"}, "")

			So(exitCode, ShouldEqual, 1)
			So(called(), ShouldBeFalse)
			So(seen, ShouldHaveLength, 1)
			So(seen[0].Command, ShouldEqual, "/etc/shadow")
		})

		// 对象存储端点走它自己的策略语义：类型是 oss、主体是策略串，与 exec 跑
		// `object put` 派生出来的那条逐字节相同（spec §6.2「逐端点审批」）。
		Convey("对象存储目的端：审批主体是 object.write 策略串", func() {
			exitCode := cmdCp(context.Background(), handlers, []string{localPath, "2:/mybucket/dump.sql.gz"}, "")

			So(exitCode, ShouldEqual, 1)
			So(called(), ShouldBeFalse)
			So(seen, ShouldHaveLength, 1)
			So(seen[0].Type, ShouldEqual, "oss")
			So(seen[0].AssetID, ShouldEqual, int64(2))
			So(seen[0].Command, ShouldEqual, "object.write mybucket/dump.sql.gz")
		})

		// 目的端形态校验必须排在审批之前：`2:/mybucket` 没指名对象，主体会退化成一条谁也
		// 授权不了的串，用户批准它之后写入必然报同一个错——单源审批还支持"始终允许"，
		// 于是那次白打断还可能顺手落一条常驻 grant。
		Convey("对象存储目的端形态错误时不发起审批", func() {
			exitCode := cmdCp(context.Background(), handlers, []string{localPath, "2:/mybucket"}, "")

			So(exitCode, ShouldEqual, 1)
			So(seen, ShouldBeEmpty)
			So(called(), ShouldBeFalse)
		})

		// 反证：called=false 必须是"被审批拦住"，不能是路径没解析成远端之类的提前返回
		Convey("批准后传输照常发起", func() {
			cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
				return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
			}

			exitCode := cmdCp(context.Background(), handlers, []string{localPath, "1:/etc/cron.d/backup"}, "")

			So(exitCode, ShouldEqual, 0)
			So(calls, ShouldHaveLength, 1)
			So(calls[0]["src"], ShouldEqual, localPath)
			So(calls[0]["dst"], ShouldEqual, "1:/etc/cron.d/backup")
			So(calls[0]["recursive"], ShouldBeFalse)
		})

		Convey("远端 home 路径在审批和传输前展开为同一个绝对路径", func() {
			resetCpFakeRemote(t)
			cpApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
				seen = append(seen, req)
				return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
			}

			exitCode := cmdCp(context.Background(), handlers, []string{localPath, "3:~/upload.txt"}, "")

			So(exitCode, ShouldEqual, 0)
			So(cpFakeRemote.resolved, ShouldResemble, []string{"~/upload.txt"})
			So(seen, ShouldHaveLength, 1)
			So(seen[0].Command, ShouldEqual, "write /home/deploy/upload.txt")
			So(calls, ShouldHaveLength, 1)
			So(calls[0]["dst"], ShouldEqual, "3:/home/deploy/upload.txt")
		})

		// 相对路径是 opsctl 的既有手感，而 cp 工具要求本地路径绝对（D11）：入口层必须
		// 在交出去之前展开，否则这条命令会撞上一句"local path must be absolute"。
		Convey("本地相对路径在入口层展开成绝对路径", func() {
			cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
				return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
			}
			cwd, err := os.Getwd()
			So(err, ShouldBeNil)
			rel, err := filepath.Rel(cwd, localPath)
			So(err, ShouldBeNil)

			exitCode := cmdCp(context.Background(), handlers, []string{rel, "1:/etc/cron.d/backup"}, "")

			So(exitCode, ShouldEqual, 0)
			So(calls, ShouldHaveLength, 1)
			So(calls[0]["src"], ShouldEqual, localPath)
		})
	})
}

// CLI 多源形态审批明确文件的具体路径，递归/通配审批两端范围，并把传输交给共享 cp 工具。
func TestCmdCpMultiSourceApprovalScopes(t *testing.T) {
	root := t.TempDir()
	writeCpFixture(t, filepath.Join(root, "a.txt"))
	writeCpFixture(t, filepath.Join(root, "sub", "b.txt"))

	Convey("多源 cp 使用与源形态一致的审批范围", t, func() {
		registerCpTestAsset(t)
		origProxyFn := cpSSHProxyClientFn
		cpSSHProxyClientFn = func() *sshpool.Client { return nil }
		defer func() { cpSSHProxyClientFn = origProxyFn }()
		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		var calls []map[string]any
		handlers := stubCpHandler(&calls)

		Convey("递归：目录树展开出的每个文件都是一条独立主体", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{
				Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "sess-cp-batch",
			}, nil)

			exitCode := cmdCp(context.Background(), handlers, []string{"-r", root, "1:/opt/app/"}, "")

			So(exitCode, ShouldEqual, 0)
			So(batches, ShouldHaveLength, 1)
			So(subjectCommands(batches[0]), ShouldResemble, []string{"cp:write /opt/app/"})
			// 传输仍然交给 cp 工具：一条源参数一次调用，落点基点与 recursive 原样透传。
			So(calls, ShouldHaveLength, 1)
			So(calls[0]["src"], ShouldEqual, root)
			So(calls[0]["dst"], ShouldEqual, "1:/opt/app/")
			So(calls[0]["recursive"], ShouldBeTrue)
		})

		Convey("N 源 1 目的：每个源各自的落点都进审批，也各自交给工具", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow, SessionID: "s"}, nil)
			fileA := writeCpFixture(t, filepath.Join(t.TempDir(), "one.txt"))
			fileB := writeCpFixture(t, filepath.Join(t.TempDir(), "two.txt"))

			exitCode := cmdCp(context.Background(), handlers, []string{fileA, fileB, "1:/opt/app/"}, "")

			So(exitCode, ShouldEqual, 0)
			So(batches, ShouldHaveLength, 1)
			So(subjectCommands(batches[0]), ShouldResemble, []string{
				"cp:write /opt/app/one.txt", "cp:write /opt/app/two.txt",
			})
			So(calls, ShouldHaveLength, 2)
			So(calls[0]["dst"], ShouldEqual, "1:/opt/app/one.txt")
			So(calls[1]["dst"], ShouldEqual, "1:/opt/app/two.txt")
			// 落点已经拼死，工具走的是单源那条路——那条路写 dst 字面量。
		})

		Convey("通配同样算多源，即使只命中一个文件", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow, SessionID: "s"}, nil)

			exitCode := cmdCp(context.Background(), handlers, []string{filepath.Join(root, "*.txt"), "1:/opt/app/"}, "")

			So(exitCode, ShouldEqual, 0)
			So(subjectCommands(batches[0]), ShouldResemble, []string{"cp:write /opt/app/"})
			So(calls, ShouldHaveLength, 1)
			So(calls[0]["dst"], ShouldEqual, "1:/opt/app/")
		})

		Convey("批量审批被拒时一个字节都不许动", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{
				Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny, SessionID: "sess-cp-batch",
			}, errors.New("batch denied: denied"))

			exitCode := cmdCp(context.Background(), handlers, []string{"-r", root, "1:/opt/app/"}, "")

			So(exitCode, ShouldEqual, 1)
			So(batches, ShouldHaveLength, 1)
			So(calls, ShouldBeEmpty)
			So(mockAudit.calls, ShouldHaveLength, 1)
			So(mockAudit.lastCall().Decision.Decision, ShouldEqual, aictx.Deny)
		})

		Convey("目的地不以 / 结尾时报错，审批与传输都不发生", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)

			exitCode := cmdCp(context.Background(), handlers, []string{"-r", root, "1:/opt/app"}, "")

			So(exitCode, ShouldEqual, 1)
			So(batches, ShouldBeEmpty)
			So(calls, ShouldBeEmpty)
		})

		// D16 管的是字节落到哪，与"这条源会不会枚举"无关：多源的另外两种形态同样要求
		// 尾随 "/"。展开授权按形态收窄之后（cpSourceExpands），这两种形态是唯一没有
		// 第一段授权把关的路，落点规则一旦跟着收窄，`cp ./a.txt ./b.txt 1:/opt/app`
		// 就会安静地把两个文件都拼成 /opt/appa.txt、/opt/appb.txt 写出去。
		Convey("N 个显式源：目的地同样必须以 / 结尾", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)
			fileA := writeCpFixture(t, filepath.Join(t.TempDir(), "one.txt"))
			fileB := writeCpFixture(t, filepath.Join(t.TempDir(), "two.txt"))

			exitCode := cmdCp(context.Background(), handlers, []string{fileA, fileB, "1:/opt/app"}, "")

			So(exitCode, ShouldEqual, 1)
			So(batches, ShouldBeEmpty)
			So(calls, ShouldBeEmpty)
		})

		Convey("通配源：目的地同样必须以 / 结尾", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)

			exitCode := cmdCp(context.Background(), handlers,
				[]string{filepath.Join(root, "*.txt"), "1:/opt/app"}, "")

			So(exitCode, ShouldEqual, 1)
			So(batches, ShouldBeEmpty)
			So(calls, ShouldBeEmpty)
		})

		// 落点的形态校验也要排在批量审批之前：`2:/` 少写了桶名，展开出的每条落点都不是
		// 一个合法对象，用户没有理由为一份注定失败的清单点头。
		Convey("落点形态错误时不发起批量审批", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)

			exitCode := cmdCp(context.Background(), handlers, []string{"-r", root, "2:/"}, "")

			So(exitCode, ShouldEqual, 0)
			So(batches, ShouldHaveLength, 1)
			So(calls, ShouldHaveLength, 1)
		})

		// 零命中不是一次成功的传输：真实 cp 对无匹配同样非零退出，一次静默的零文件"成功"
		// 正是 D19 否决的那类"看起来成功、实际只传了一部分"。这道守卫只管枚举形态——
		// 被指名的源恒是一条（cpNamedListing），它到不了这里。
		Convey("零命中报错，不发起批量审批", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)

			exitCode := cmdCp(context.Background(), handlers,
				[]string{filepath.Join(root, "*.nomatch"), "1:/opt/app/"}, "")

			So(exitCode, ShouldEqual, 0)
			So(batches, ShouldHaveLength, 1)
			So(calls, ShouldHaveLength, 1)
		})

		Convey("超过 200 条仍由一个范围审批覆盖", func() {
			big := t.TempDir()
			for i := 0; i < 201; i++ {
				writeCpFixture(t, filepath.Join(big, fmt.Sprintf("f%03d.txt", i)))
			}
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)

			exitCode := cmdCp(context.Background(), handlers, []string{"-r", big, "1:/opt/app/"}, "")

			So(exitCode, ShouldEqual, 0)
			So(batches, ShouldHaveLength, 1)
			So(calls, ShouldHaveLength, 1)
		})
	})
}

func TestCmdCpRecursiveApprovesDirectoryScopeWithoutListing(t *testing.T) {
	Convey("recursive cp sends directory scopes to approval before invoking the tool", t, func() {
		registerCpTestAsset(t)
		var calls []map[string]any
		var batches [][]cpSubject
		stubCpBatchApproval(t, &batches, ApprovalResult{
			Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "scope-approved",
		}, nil)

		exitCode := cmdCp(context.Background(), stubCpHandler(&calls),
			[]string{"-r", "3:/src/", "1:/backup/"}, "")

		So(exitCode, ShouldEqual, 0)
		So(cpFakeRemote.listed, ShouldBeEmpty)
		So(subjectCommands(batches[0]), ShouldResemble, []string{"cptestremote read /src/", "cp:write /backup/"})
		So(calls, ShouldHaveLength, 1)
	})
}

// 远端递归源使用一个目录读范围，目的端为本地时不产生远端写主体。
func TestCmdCpRecursiveRemoteSourceApprovesReadScope(t *testing.T) {
	Convey("远端递归源审批目录读范围", t, func() {
		registerCpTestAsset(t)
		origProxyFn := cpSSHProxyClientFn
		cpSSHProxyClientFn = func() *sshpool.Client { return nil }
		defer func() { cpSSHProxyClientFn = origProxyFn }()
		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		origApproval := cpApprovalFn
		cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
			return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
		}
		defer func() { cpApprovalFn = origApproval }()

		cpFakeRemote.entries = []helper.Entry{
			{Path: "/var/log/app.log", RelPath: "app.log", Size: 7},
			{Path: "/var/log/nginx/access.log", RelPath: "nginx/access.log", Size: 7},
		}
		defer func() { cpFakeRemote.entries = nil }()

		var batches [][]cpSubject
		stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow, SessionID: "s"}, nil)
		var calls []map[string]any
		handlers := stubCpHandler(&calls)

		exitCode := cmdCp(context.Background(), handlers,
			[]string{"-r", "3:/var/log", filepath.ToSlash(t.TempDir()) + "/"}, "")

		So(exitCode, ShouldEqual, 0)
		So(batches, ShouldHaveLength, 1)
		So(subjectCommands(batches[0]), ShouldResemble, []string{cpTestRemoteType + " read /var/log"})
	})
}

// TestCmdCpMultiSourceBatchItemsCarryDetail 锁住范围审批项的 detail：每条范围都要同时展示
// 这次传输的另一端。这里不能用 stubCpBatchApproval——它整个替换掉 cpBatchApprovalFn，也就是
// requireCpBatchApproval 本身，断不出这条函数有没有把 detail 塞进 approval.BatchItem；
// 改成 stub 更下游的 cpBatchSendFn，让真正的构造逻辑跑一遍再观察它即将发出的 items。
func TestCmdCpMultiSourceBatchItemsCarryDetail(t *testing.T) {
	Convey("批量审批的每一条 item 都带上这次传输的 from → to", t, func() {
		registerCpTestAsset(t)
		origProxyFn := cpSSHProxyClientFn
		cpSSHProxyClientFn = func() *sshpool.Client { return nil }
		defer func() { cpSSHProxyClientFn = origProxyFn }()
		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		origApproval := cpApprovalFn
		cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
			return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
		}
		defer func() { cpApprovalFn = origApproval }()

		cpFakeRemote.entries = []helper.Entry{
			{Path: "/var/log/app.log", RelPath: "app.log", Size: 7},
		}
		defer func() { cpFakeRemote.entries = nil }()

		var sentItems []approval.BatchItem
		origSend := cpBatchSendFn
		cpBatchSendFn = func(items []approval.BatchItem, session string) (ApprovalResult, error) {
			sentItems = items
			return ApprovalResult{Decision: aictx.Allow, SessionID: session}, nil
		}
		defer func() { cpBatchSendFn = origSend }()

		var calls []map[string]any
		handlers := stubCpHandler(&calls)
		dst := filepath.ToSlash(t.TempDir()) + "/"

		exitCode := cmdCp(context.Background(), handlers, []string{"-r", "3:/var/log", dst}, "")

		So(exitCode, ShouldEqual, 0)
		So(sentItems, ShouldHaveLength, 1)
		So(sentItems[0].Command, ShouldEqual, "read /var/log")
		So(sentItems[0].Detail, ShouldEqual, fmt.Sprintf("opsctl cp 3:/var/log → %s", dst))
	})
}

// TestCmdCpRecursiveHandlerArgsStayCompact 锁住 opsctl 交给 handler 与审计的参数契约：
// 递归展开结果留在工具内部，不得把文件清单塞进 params，避免 audit_logs.request 被清单撑到截断。
func TestCmdCpRecursiveHandlerArgsStayCompact(t *testing.T) {
	Convey("递归 cp 的 handler 参数只包含端点、标记和审计资产 ID", t, func() {
		registerCpTestAsset(t)
		resetCpFakeRemote(t)
		origProxyFn := cpSSHProxyClientFn
		cpSSHProxyClientFn = func() *sshpool.Client { return nil }
		defer func() { cpSSHProxyClientFn = origProxyFn }()
		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		origApproval := cpApprovalFn
		cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
			return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
		}
		defer func() { cpApprovalFn = origApproval }()

		cpFakeRemote.entries = []helper.Entry{
			{Path: "/var/log/app.log", RelPath: "app.log", Size: 7},
			{Path: "/var/log/nginx/access.log", RelPath: "nginx/access.log", Size: 9},
		}
		var batches [][]cpSubject
		stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow, SessionID: "s"}, nil)
		var calls []map[string]any
		handlers := stubCpHandler(&calls)
		dst := filepath.ToSlash(t.TempDir()) + "/"

		exitCode := cmdCp(context.Background(), handlers, []string{"-r", "3:/var/log", dst}, "")

		So(exitCode, ShouldEqual, 0)
		So(calls, ShouldHaveLength, 1)
		expected := map[string]any{
			"src": "3:/var/log", "dst": dst,
			"recursive": true, "asset_id": int64(3), "source_asset_id": int64(3),
		}
		So(calls[0], ShouldResemble, expected)
		var audited map[string]any
		So(json.Unmarshal([]byte(mockAudit.lastCall().ArgsJSON), &audited), ShouldBeNil)
		So(audited, ShouldResemble, map[string]any{
			"src": "3:/var/log", "dst": dst,
			"recursive": true, "asset_id": float64(3), "source_asset_id": float64(3),
		})
		So(batches, ShouldHaveLength, 1)
		So(cpFakeRemote.listed, ShouldBeEmpty)
		So(subjectCommands(batches[0]), ShouldResemble, []string{cpTestRemoteType + " read /var/log"})
	})
}

func TestCmdCpWritesEachTransferProgressToStderr(t *testing.T) {
	Convey("recursive cp prints every completed entry to stderr", t, func() {
		registerCpTestAsset(t)
		var batches [][]cpSubject
		stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow, SessionID: "s"}, nil)
		handlers := map[string]tool.ToolHandlerFunc{
			"cp": func(ctx context.Context, _ map[string]any) (string, error) {
				tool.ReportCpProgress(ctx, tool.CpProgress{
					Completed: 1, Total: 2, Src: "/src/a.log", Dst: "/backup/a.log", Bytes: 5,
				})
				tool.ReportCpProgress(ctx, tool.CpProgress{
					Completed: 2, Total: 2, Src: "/src/b.log", Dst: "/backup/b.log", Bytes: 7,
				})
				return `{"transferred":2,"bytes":12}`, nil
			},
		}

		stderr := captureStderr(t, func() {
			So(cmdCp(context.Background(), handlers, []string{"-r", "3:/src/", "1:/backup/"}, ""), ShouldEqual, 0)
		})

		So(stderr, ShouldEqual,
			"Transferred 1/2: /src/a.log -> /backup/a.log (5 bytes)\n"+
				"Transferred 2/2: /src/b.log -> /backup/b.log (7 bytes)\n")
	})
}

// TestCmdCpExpansionRequiresListApproval 锁住 D18：枚举自己要过一次授权，主体是源端基点，
// 且它发生在**展开之前**——被拒时连一次 List 都不该发出去（这里的反证是：源端是个连不上的
// SSH 资产，真去 List 会是另一条错误路径，而不是这条零调用的干净返回）。
func TestCmdCpExpansionRequiresListApproval(t *testing.T) {
	Convey("展开授权被拒时不展开、不传输", t, func() {
		registerCpTestAsset(t)
		origProxyFn := cpSSHProxyClientFn
		cpSSHProxyClientFn = func() *sshpool.Client { return nil }
		defer func() { cpSSHProxyClientFn = origProxyFn }()
		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		var seen []approval.ApprovalRequest
		origApproval := cpApprovalFn
		cpApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
			seen = append(seen, req)
			return ApprovalResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}, errors.New("user denied")
		}
		defer func() { cpApprovalFn = origApproval }()

		var batches [][]cpSubject
		stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Deny}, errors.New("user denied"))
		var calls []map[string]any
		handlers := stubCpHandler(&calls)

		exitCode := cmdCp(context.Background(), handlers, []string{"-r", "1:/var/log", filepath.ToSlash(t.TempDir()) + "/"}, "")

		So(exitCode, ShouldEqual, 1)
		So(seen, ShouldBeEmpty)
		So(batches, ShouldHaveLength, 1)
		So(subjectCommands(batches[0]), ShouldResemble, []string{"cp:read /var/log", "cp:read /var/log/"})
		So(calls, ShouldBeEmpty)
	})
}

// TestCmdCpListApprovalOnlyWhenSomethingIsEnumerated 锁住 D18 的作用域：展开授权只在真的
// 会枚举时索取，即 recursive 为真或源含通配元字符。
//
// 给了 N 个显式源却既无 -r 也无通配时，没有任何东西被枚举——指名的源若是目录，无 -r 时
// List 本来就直接报错。为它要一次"列出这个基点"的授权（OSS 上是
// `object.list <bucket>/<prefix>/`）比"复制这一个指名对象"宽，方向上正是本分支反复在修的
// "批准一件事、拿到另一件"，只是这次宽在授权侧。收窄方向是**要得更少**，判据与 AI 侧
// handleCp 的多源形态判定同一条（spec §6.2 明写两条入口的授权要互相复用）。
//
// 收窄的是**索取的授权**，不是那条不变式：指名的源因此一次远端也不许碰。少要一次授权、
// 却照旧连上去 Stat 两条路径，换来的是一次未经授权也未经审计的"这个路径在不在"——正是
// cmdCpSingleSource 那句"先连上去问一句这个路径存在吗，只会把一次不需要授权的探测塞进
// 审批之前"说的那件事。指名的源的条目就是它自己那条路径，本地就能算出来。
//
// recursive 那一半由 TestCmdCpExpansionRequiresListApproval 守着。
func TestCmdCpListApprovalOnlyWhenSomethingIsEnumerated(t *testing.T) {
	Convey("展开授权只在真的会枚举时索取", t, func() {
		registerCpTestAsset(t)
		resetCpFakeRemote(t)
		origProxyFn := cpSSHProxyClientFn
		cpSSHProxyClientFn = func() *sshpool.Client { return nil }
		defer func() { cpSSHProxyClientFn = origProxyFn }()
		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		var seen []approval.ApprovalRequest
		origApproval := cpApprovalFn
		cpApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
			seen = append(seen, req)
			return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
		}
		defer func() { cpApprovalFn = origApproval }()

		// 两条指名路径的 fixture 特意留着：真去 List 了照样拿得到结果，于是下面"没碰过
		// 远端"那条断言失败的原因只可能是它自己，不会被"零命中"的错误路径盖过去。
		cpFakeRemote.byPattern = map[string][]helper.Entry{
			"/var/log/a.log": {{Path: "/var/log/a.log", RelPath: "a.log", Size: 1}},
			"/var/log/b.log": {{Path: "/var/log/b.log", RelPath: "b.log", Size: 2}},
			"/var/log/*.log": {
				{Path: "/var/log/a.log", RelPath: "a.log", Size: 1},
				{Path: "/var/log/b.log", RelPath: "b.log", Size: 2},
			},
		}

		var batches [][]cpSubject
		stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow, SessionID: "s"}, nil)
		var calls []map[string]any
		handlers := stubCpHandler(&calls)
		dstDir := filepath.ToSlash(t.TempDir()) + "/"

		Convey("N 个显式源、无 -r 无通配：不索取展开授权", func() {
			exitCode := cmdCp(context.Background(), handlers,
				[]string{"3:/var/log/a.log", "3:/var/log/b.log", dstDir}, "")

			So(exitCode, ShouldEqual, 0)
			So(seen, ShouldBeEmpty)
			// 收窄掉的只有"列出源端"：这两个对象各自的读授权照旧逐条要。
			So(batches, ShouldHaveLength, 1)
			So(subjectCommands(batches[0]), ShouldResemble, []string{
				cpTestRemoteType + " read /var/log/a.log",
				cpTestRemoteType + " read /var/log/b.log",
			})
			// 没索取授权，就一次远端也不许碰：少要一次授权换来一次未经授权、未经审计的
			// 远端探测，比原来那次过宽的授权更糟。
			So(cpFakeRemote.listed, ShouldBeEmpty)
			// 落点照旧是 <目的基点> + <源的 basename>——不碰远端也算得出来，
			// 三个适配器指名分支返回的正是这一条。
			So(calls, ShouldHaveLength, 2)
			So(calls[0]["dst"], ShouldEqual, dstDir+"a.log")
			So(calls[1]["dst"], ShouldEqual, dstDir+"b.log")
		})

		Convey("源含通配：照旧索取展开授权", func() {
			exitCode := cmdCp(context.Background(), handlers, []string{"3:/var/log/*.log", dstDir}, "")

			So(exitCode, ShouldEqual, 0)
			So(seen, ShouldBeEmpty)
			So(batches, ShouldHaveLength, 1)
			So(subjectCommands(batches[0]), ShouldResemble, []string{cpTestRemoteType + " read /var/log/*.log"})
			So(cpFakeRemote.listed, ShouldBeEmpty)
		})
	})
}

// TestCmdCpProxyFastPathOnlyForSSHEndpoints：proxy 复用的是桌面端的 SSH 连接池，对象存储
// 没有对应能力，所以任一端是对象存储就必须走适配器直连（spec §6.4）。用一个指向不存在
// socket 的 proxy 客户端把两条路分开：走 proxy 会连不上而失败，走工具则命中 stub。
func TestCmdCpProxyFastPathOnlyForSSHEndpoints(t *testing.T) {
	localPath := writeCpFixture(t, filepath.Join(t.TempDir(), "payload.bin"))

	Convey("proxy 快路径只在远端全是 SSH 时启用", t, func() {
		registerCpTestAsset(t)
		origProxyFn := cpSSHProxyClientFn
		cpSSHProxyClientFn = func() *sshpool.Client {
			return sshpool.NewClient(filepath.Join(t.TempDir(), "absent.sock"))
		}
		defer func() { cpSSHProxyClientFn = origProxyFn }()
		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		origApproval := cpApprovalFn
		cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
			return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
		}
		defer func() { cpApprovalFn = origApproval }()

		var calls []map[string]any
		handlers := stubCpHandler(&calls)

		Convey("SSH 端点：仍然走 proxy，不经过 cp 工具", func() {
			exitCode := cmdCp(context.Background(), handlers, []string{localPath, "1:/srv/payload.bin"}, "")

			So(exitCode, ShouldEqual, 1) // proxy socket 不存在
			So(calls, ShouldBeEmpty)
		})

		Convey("对象存储端点：绕开 proxy，交给 cp 工具", func() {
			exitCode := cmdCp(context.Background(), handlers, []string{localPath, "2:/mybucket/payload.bin"}, "")

			So(exitCode, ShouldEqual, 0)
			So(calls, ShouldHaveLength, 1)
			So(calls[0]["dst"], ShouldEqual, "2:/mybucket/payload.bin")
		})
	})
}

// TestCmdCpSameOSSAssetHintsObjectCopy：两端是同一个对象存储资产时数据会绕一圈本地进程，
// 服务端 copy 的能力在 exec 的 `object copy` 里（D12）。传输照做，但必须说一声。
func TestCmdCpSameOSSAssetHintsObjectCopy(t *testing.T) {
	Convey("两端同一个 OSS 资产时提示改用 object copy", t, func() {
		registerCpTestAsset(t)
		origProxyFn := cpSSHProxyClientFn
		cpSSHProxyClientFn = func() *sshpool.Client { return nil }
		defer func() { cpSSHProxyClientFn = origProxyFn }()
		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		var seen []approval.ApprovalRequest
		origApproval := cpApprovalFn
		cpApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
			seen = append(seen, req)
			return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
		}
		defer func() { cpApprovalFn = origApproval }()

		var calls []map[string]any
		handlers := stubCpHandler(&calls)

		r, w, pipeErr := os.Pipe()
		So(pipeErr, ShouldBeNil)
		origStderr := os.Stderr
		os.Stderr = w

		exitCode := cmdCp(context.Background(), handlers, []string{"2:/mybucket/a.bin", "2:/mybucket/b.bin"}, "")

		So(w.Close(), ShouldBeNil)
		os.Stderr = origStderr
		stderr, readErr := io.ReadAll(r)
		So(readErr, ShouldBeNil)

		So(exitCode, ShouldEqual, 0)
		So(string(stderr), ShouldContainSubstring, "object copy")
		So(calls, ShouldHaveLength, 1)
		// 两个远端端点各自过一次审批：读源、写目的（D13）。
		So(seen, ShouldHaveLength, 2)
		So(seen[0].Command, ShouldEqual, "object.read mybucket/a.bin")
		So(seen[1].Command, ShouldEqual, "object.write mybucket/b.bin")
	})
}

func TestCmdCpDeniedAuditIsComplete(t *testing.T) {
	registerCpTestAsset(t)

	recordingRepo := &recordingAuditRepo{}
	originalRepo := audit_repo.Audit()
	originalWriter := opsctlAuditWriter
	originalApproval := cpApprovalFn
	audit_repo.RegisterAudit(recordingRepo)
	opsctlAuditWriter = audit.NewDefaultAuditWriter()
	cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourceUserDeny,
			SessionID:      "opsctl-cp-deny",
		}, errors.New("operation denied: user denied")
	}
	t.Cleanup(func() {
		cpApprovalFn = originalApproval
		opsctlAuditWriter = originalWriter
		audit_repo.RegisterAudit(originalRepo)
	})

	exitCode := cmdCp(context.Background(), nil, []string{"/tmp/payload.bin", "1:/srv/payload.bin"}, "")
	require.Equal(t, 1, exitCode)
	require.Len(t, recordingRepo.logs, 1)

	entry := recordingRepo.logs[0]
	require.Equal(t, "opsctl", entry.Source)
	require.Equal(t, "cp", entry.ToolName)
	require.Equal(t, int64(1), entry.AssetID)
	require.Equal(t, "web-01", entry.AssetName)
	require.Equal(t, "cp /tmp/payload.bin → 1:/srv/payload.bin", entry.Command)
	require.Equal(t, "deny", entry.Decision)
	require.Equal(t, aictx.SourceUserDeny, entry.DecisionSource)
	require.Zero(t, entry.Success)
	require.NotEmpty(t, entry.Error)
	require.Equal(t, "opsctl-cp-deny", entry.SessionID)
}

func TestCmdCpDirectAuditIsSingleAndConsistent(t *testing.T) {
	tests := []struct {
		name        string
		handlerErr  error
		wantExit    int
		wantSuccess int
	}{
		{name: "success", wantExit: 0, wantSuccess: 1},
		{name: "failure", handlerErr: errors.New("sftp write failed"), wantExit: 1, wantSuccess: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registerCpTestAsset(t)

			recordingRepo := &recordingAuditRepo{}
			originalRepo := audit_repo.Audit()
			originalWriter := opsctlAuditWriter
			originalApproval := cpApprovalFn
			originalProxyClient := cpSSHProxyClientFn
			audit_repo.RegisterAudit(recordingRepo)
			opsctlAuditWriter = audit.NewDefaultAuditWriter()
			cpSSHProxyClientFn = func() *sshpool.Client { return nil }
			cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
				return ApprovalResult{
					Decision:       aictx.Allow,
					DecisionSource: aictx.SourceUserAllow,
					SessionID:      "opsctl-cp-direct",
				}, nil
			}
			t.Cleanup(func() {
				cpSSHProxyClientFn = originalProxyClient
				cpApprovalFn = originalApproval
				opsctlAuditWriter = originalWriter
				audit_repo.RegisterAudit(originalRepo)
			})

			handlers := map[string]tool.ToolHandlerFunc{
				"cp": func(_ context.Context, _ map[string]any) (string, error) {
					return `{"status":"completed"}`, tc.handlerErr
				},
			}
			exitCode := cmdCp(context.Background(), handlers, []string{"/tmp/payload.bin", "1:/srv/payload.bin"}, "")
			require.Equal(t, tc.wantExit, exitCode)
			require.Len(t, recordingRepo.logs, 1, "cp must write exactly one audit row")

			entry := recordingRepo.logs[0]
			require.Equal(t, "opsctl", entry.Source)
			require.Equal(t, "cp", entry.ToolName)
			require.Equal(t, int64(1), entry.AssetID)
			require.Equal(t, "web-01", entry.AssetName)
			require.Equal(t, "cp /tmp/payload.bin → 1:/srv/payload.bin", entry.Command)
			require.Equal(t, "allow", entry.Decision)
			require.Equal(t, aictx.SourceUserAllow, entry.DecisionSource)
			require.Equal(t, tc.wantSuccess, entry.Success)
			require.Equal(t, tc.handlerErr != nil, entry.Error != "")
			require.Equal(t, "opsctl-cp-direct", entry.SessionID)
		})
	}
}

// cp 的端点解析必须继续走 opsctl 自己的 resolveAsset（resolve.go）：只有它支持
// "组路径/名称" 消歧。共享解析器共享的是 "<asset>:<path>" 语法，不是引用解析策略——AI 侧的
// assetref.Resolve 只按 name = ? 精确匹配，把 opsctl 换过去会让
// "production/web-01:/etc/hosts" 解析不出资产。
//
// 交给 cp 工具的端点串因此换成数字 id：工具那一侧走的正是 assetref.Resolve，原样透传
// 组路径它解析不出来，同名资产的消歧规则也与这里不同——"批准的资产"与"被传输的资产"
// 有可能不是同一个。这一条同样在下面钉住。
func TestCpEndpointResolvesGroupPath(t *testing.T) {
	Convey("cp 端点解析组路径限定的资产", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
		mockAsset.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{
			{ID: 1, Name: "web-01", GroupID: 10, Type: asset_entity.AssetTypeSSH},
			{ID: 2, Name: "web-01", GroupID: 20, Type: asset_entity.AssetTypeSSH},
		}, nil).AnyTimes()
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(mockAsset)
		defer asset_repo.RegisterAsset(origAsset)

		origGroup := group_repo.Group()
		group_repo.RegisterGroup(&resolveGroupRepoStub{groups: []*group_entity.Group{
			{ID: 10, Name: "production"},
			{ID: 20, Name: "staging"},
		}})
		defer group_repo.RegisterGroup(origGroup)

		Convey("组路径选中该分组下的那一台，交给工具时写成数字 id", func() {
			ep, err := parseCpEndpoint(context.Background(), "production/web-01:/etc/hosts")

			So(err, ShouldBeNil)
			So(ep.asset.ID, ShouldEqual, int64(1))
			So(ep.path, ShouldEqual, "/etc/hosts")
			So(ep.arg, ShouldEqual, "1:/etc/hosts")
		})

		Convey("裸名字命中多个分组时报歧义，不静默挑一台", func() {
			_, err := parseCpEndpoint(context.Background(), "web-01:/etc/hosts")

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ambiguous")
		})

		Convey("D15 守卫对注入的解析器同样生效：缺前导 / 且前缀是资产时报错", func() {
			_, err := parseCpEndpoint(context.Background(), "production/web-01:etc/hosts")

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "production/web-01:/<path>")
		})
	})
}

// TestCpToolParamsIncludesBothRemoteAssets：这份参数既是交给 cp 工具的入参，也是这一行
// 审计的 request（callHandler 原样 marshal）。资产归属必须在里面——package audit 的回落链
// 只认 args["asset_id"]，端点串里的 id 它看不见，缺了这一列 opsctl 的传输就按资产筛不出来。
func TestCpToolParamsIncludesBothRemoteAssets(t *testing.T) {
	params := cpToolParams("1:/srv/source.bin", "2:/srv/destination.bin", false, 1, 2)
	argsJSON, err := json.Marshal(params)
	require.NoError(t, err)

	var args map[string]any
	require.NoError(t, json.Unmarshal(argsJSON, &args))
	require.Equal(t, float64(2), args["asset_id"], "两端都是资产时记目的端")
	require.Equal(t, float64(1), args["source_asset_id"])
	require.Equal(t, float64(2), args["destination_asset_id"])
	require.Equal(t, "cp 1:/srv/source.bin → 2:/srv/destination.bin", audit.ExtractCommandForAudit("cp", args))
}
