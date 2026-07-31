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
// 它存在是因为**远端源的展开**在这个包里没有别的办法观察：SSH 端要一台真机、对象存储端
// 要一个服务端，于是本文件此前每一条多源用例的源都是本地目录，而本地端不产生审批主体——
// 展开出的每一条**读**主体因此从未被任何断言看见（把 entry.Path 换成 src.path，即 D17
// 明令禁止的"批一条递归 pattern"，整个包仍然全绿）。这个内存端点把那一半接进断言。
const cpTestRemoteType = "cptestremote"

type cpFakeAdapter struct{ entries []helper.Entry }

var cpFakeRemote = &cpFakeAdapter{}

func init() { helper.RegisterTransferAdapter(cpTestRemoteType, cpFakeRemote) }

func (a *cpFakeAdapter) List(
	_ context.Context, _ *asset_entity.Asset, _ string, _ bool,
) (*helper.ListResult, error) {
	return &helper.ListResult{Entries: a.entries}, nil
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
	cpBatchApprovalFn = func(_ context.Context, subjects []cpSubject) (ApprovalResult, error) {
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

// TestCmdCpMultiSourceApprovesEveryExpandedPath 锁住本轮的硬不变式在 CLI 这条路上成立：
// 每一条实际被读写的路径，都要在动手之前作为**具体主体**进过审批（spec 第 9 行 / D17 /
// D18）。cp 工具在 opsctl 这条路上是预检过的（checker 为 nil，逐条主体那一关直接放行），
// 所以展开与逐条审批只能由 opsctl 自己走完——只把工具名改成 "cp" 会让 `cp -r` 在只批准了
// 基点的情况下传输 N 个文件。
func TestCmdCpMultiSourceApprovesEveryExpandedPath(t *testing.T) {
	root := t.TempDir()
	writeCpFixture(t, filepath.Join(root, "a.txt"))
	writeCpFixture(t, filepath.Join(root, "sub", "b.txt"))

	Convey("多源 cp 的审批主体是展开后的每一条具体路径", t, func() {
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
			So(subjectCommands(batches[0]), ShouldResemble, []string{
				"cp /opt/app/a.txt", "cp /opt/app/sub/b.txt",
			})
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
				"cp /opt/app/one.txt", "cp /opt/app/two.txt",
			})
			So(calls, ShouldHaveLength, 2)
			So(calls[0]["dst"], ShouldEqual, "1:/opt/app/one.txt")
			So(calls[1]["dst"], ShouldEqual, "1:/opt/app/two.txt")
		})

		Convey("通配同样算多源，即使只命中一个文件", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow, SessionID: "s"}, nil)

			exitCode := cmdCp(context.Background(), handlers, []string{filepath.Join(root, "*.txt"), "1:/opt/app/"}, "")

			So(exitCode, ShouldEqual, 0)
			So(subjectCommands(batches[0]), ShouldResemble, []string{"cp /opt/app/a.txt"})
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

		// 落点的形态校验也要排在批量审批之前：`2:/` 少写了桶名，展开出的每条落点都不是
		// 一个合法对象，用户没有理由为一份注定失败的清单点头。
		Convey("落点形态错误时不发起批量审批", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)

			exitCode := cmdCp(context.Background(), handlers, []string{"-r", root, "2:/"}, "")

			So(exitCode, ShouldEqual, 1)
			So(batches, ShouldBeEmpty)
			So(calls, ShouldBeEmpty)
		})

		// 零命中不是一次成功的传输：真实 cp 对无匹配同样非零退出，而这里放行的后果比"静默
		// 的零文件成功"还重一档——落点是由 entries[0] 拼出来的，空清单会当场 index out of
		// range（见 cpDstArg）。
		Convey("零命中报错，不发起批量审批", func() {
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)

			exitCode := cmdCp(context.Background(), handlers,
				[]string{filepath.Join(root, "*.nomatch"), "1:/opt/app/"}, "")

			So(exitCode, ShouldEqual, 1)
			So(batches, ShouldBeEmpty)
			So(calls, ShouldBeEmpty)
		})

		Convey("超过 200 条即报错，不截断也不审批", func() {
			big := t.TempDir()
			for i := 0; i < cpMaxEntries+1; i++ {
				writeCpFixture(t, filepath.Join(big, fmt.Sprintf("f%03d.txt", i)))
			}
			var batches [][]cpSubject
			stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)

			exitCode := cmdCp(context.Background(), handlers, []string{"-r", big, "1:/opt/app/"}, "")

			So(exitCode, ShouldEqual, 1)
			So(batches, ShouldBeEmpty)
			So(calls, ShouldBeEmpty)
		})
	})
}

// TestCmdCpMultiSourceApprovesEveryRemoteReadPath 锁住硬不变式的**读**那一半：远端源展开
// 出的每一条路径，都要作为一条具体主体进批量审批（spec 第 9 行 / D17）。
//
// 上面那个测试的源全是本地目录，而本地端不产生审批主体，于是它断的其实只有目的端的写主体。
// 读那一半在这里断：目的端取本地，批量清单里就只剩源端读主体，一条不多。主体必须是
// entry.Path 而不是源端基点——批一条基点等于批一条递归 pattern，正是 D17 否决的那件事。
func TestCmdCpMultiSourceApprovesEveryRemoteReadPath(t *testing.T) {
	Convey("远端源：展开出的每一条读路径都是一条独立主体", t, func() {
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
		So(subjectCommands(batches[0]), ShouldResemble, []string{
			cpTestRemoteType + " read /var/log/app.log",
			cpTestRemoteType + " read /var/log/nginx/access.log",
		})
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
		stubCpBatchApproval(t, &batches, ApprovalResult{Decision: aictx.Allow}, nil)
		var calls []map[string]any
		handlers := stubCpHandler(&calls)

		exitCode := cmdCp(context.Background(), handlers, []string{"-r", "1:/var/log", filepath.ToSlash(t.TempDir()) + "/"}, "")

		So(exitCode, ShouldEqual, 1)
		So(seen, ShouldHaveLength, 1)
		So(seen[0].Type, ShouldEqual, "cp")
		So(seen[0].Command, ShouldEqual, "/var/log")
		So(batches, ShouldBeEmpty)
		So(calls, ShouldBeEmpty)
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
