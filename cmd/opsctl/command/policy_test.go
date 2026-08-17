package command

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/group_repo/mock_group_repo"
)

// --- 测试脚手架 ---

// grantRepoStub 记录 UpdateItems 的调用（rm grant 的撤销路径）并返回预置 items。
type grantRepoStub struct {
	grant_repo.GrantRepo
	items         []*grant_entity.GrantItem
	session       *grant_entity.GrantSession
	updateCalls   [][]*grant_entity.GrantItem
	listedSession string
}

func (s *grantRepoStub) ListApprovedItems(_ context.Context, sessionID string) ([]*grant_entity.GrantItem, error) {
	s.listedSession = sessionID
	return s.items, nil
}

func (s *grantRepoStub) ListItems(_ context.Context, sessionID string) ([]*grant_entity.GrantItem, error) {
	return s.items, nil
}

func (s *grantRepoStub) GetSession(_ context.Context, _ string) (*grant_entity.GrantSession, error) {
	return s.session, nil
}

func (s *grantRepoStub) UpdateItems(_ context.Context, _ string, items []*grant_entity.GrantItem) error {
	s.updateCalls = append(s.updateCalls, items)
	s.items = items
	return nil
}

// recordingAuditWriter 捕获 opsctl 侧写下的审计行。
type recordingAuditWriter struct {
	rows []audit.ToolCallInfo
}

func (w *recordingAuditWriter) WriteToolCall(_ context.Context, info audit.ToolCallInfo) {
	w.rows = append(w.rows, info)
}

type policyTestEnv struct {
	ctx         context.Context
	assetRepo   *mock_asset_repo.MockAssetRepo
	groupRepo   *mock_group_repo.MockGroupRepo
	grantRepo   *grantRepoStub
	auditor     *recordingAuditWriter
	updates     []*asset_entity.Asset
	groupUps    []*group_entity.Group
	interactive bool
	confirmIn   io.Reader
	stdoutBuf   bytes.Buffer
	stderrBuf   bytes.Buffer
}

// newPolicyTestEnv 装配 policy 命令的全部注入缝：repo、审计、TTY 判定、确认输入与
// stdout/stderr 捕获。tx runner 直接执行 fn（事务边界本身不是这里的被测行为）。
func newPolicyTestEnv(t *testing.T) *policyTestEnv {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	env := &policyTestEnv{
		ctx:         dbutil.WithTransactionRunner(context.Background(), func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }),
		assetRepo:   mock_asset_repo.NewMockAssetRepo(ctrl),
		groupRepo:   mock_group_repo.NewMockGroupRepo(ctrl),
		grantRepo:   &grantRepoStub{session: &grant_entity.GrantSession{ID: "sess-1", Status: grant_entity.GrantStatusApproved, Createtime: time.Now().Add(-2 * time.Hour).Unix()}},
		auditor:     &recordingAuditWriter{},
		confirmIn:   strings.NewReader("y\n"),
		interactive: true,
	}

	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(env.assetRepo)
	origGroup := group_repo.Group()
	group_repo.RegisterGroup(env.groupRepo)
	origGrant := grant_repo.Grant()
	grant_repo.RegisterGrant(env.grantRepo)
	origAudit := opsctlAuditWriter
	opsctlAuditWriter = env.auditor
	origStdinTTY, origStderrTTY := stdinIsTerminal, stderrIsTerminal
	origConfirm := policyConfirmStreams
	stdinIsTerminal = func() bool { return env.interactive }
	stderrIsTerminal = func() bool { return env.interactive }
	policyConfirmStreams = func() (io.Reader, io.Writer) { return env.confirmIn, &env.stderrBuf }

	t.Cleanup(func() {
		asset_repo.RegisterAsset(origAsset)
		group_repo.RegisterGroup(origGroup)
		grant_repo.RegisterGrant(origGrant)
		opsctlAuditWriter = origAudit
		stdinIsTerminal, stderrIsTerminal = origStdinTTY, origStderrTTY
		policyConfirmStreams = origConfirm
	})
	return env
}

// run 同步捕获一次 cmdPolicy 的 stdout/stderr（管道在命令返回后收口，读尽再断言，
// 不依赖后台 goroutine 的调度时机）。
func (env *policyTestEnv) run(args ...string) int {
	outR, outW, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	errR, errW, err2 := os.Pipe()
	if err2 != nil {
		panic(err2)
	}
	origStdout, origStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	code := cmdPolicy(aictx.WithAuditSource(env.ctx, "opsctl"), args, "sess-1")
	os.Stdout, os.Stderr = origStdout, origStderr
	_ = outW.Close()
	_ = errW.Close()
	_, _ = env.stdoutBuf.ReadFrom(outR)
	_, _ = env.stderrBuf.ReadFrom(errR)
	return code
}

// expectSSHAsset 注册一个 ssh 资产的 Find/List 期望，并记录 Update 调用。
func (env *policyTestEnv) expectSSHAsset(t *testing.T, id int64, name string, seed *policy.CommandPolicy) *asset_entity.Asset {
	t.Helper()
	asset := &asset_entity.Asset{ID: id, Name: name, Type: asset_entity.AssetTypeSSH}
	if seed != nil {
		require.NoError(t, asset.SetCommandPolicy(seed))
	}
	env.assetRepo.EXPECT().Find(gomock.Any(), id).DoAndReturn(func(_ context.Context, wantID int64) (*asset_entity.Asset, error) {
		return asset, nil
	}).AnyTimes()
	env.assetRepo.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
	env.assetRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *asset_entity.Asset) error {
		env.updates = append(env.updates, a)
		return nil
	}).AnyTimes()
	return asset
}

// --- TTY 门禁：allow/deny/rm 无 TTY 拒绝，show 免 TTY ---

func TestPolicyWriteSubcommandsNeedTTY(t *testing.T) {
	for _, sub := range []string{"allow", "deny", "rm"} {
		t.Run(sub, func(t *testing.T) {
			env := newPolicyTestEnv(t)
			env.interactive = false
			env.expectSSHAsset(t, 5, "web-01", nil)
			// rm 也要在无 TTY 时拒绝，且不触发任何写路径。
			env.assetRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)

			var args []string
			if sub == "rm" {
				args = []string{sub, "web-01", "1"}
			} else {
				args = []string{sub, "web-01", "--", "uptime"}
			}
			code := env.run(args...)

			assert.Equal(t, refusalExitCode, code)
			lines := strings.Split(strings.TrimSpace(env.stderrBuf.String()), "\n")
			require.NotEmpty(t, lines)
			assert.Equal(t, needsTTYMarker, lines[0])
			assert.Empty(t, env.updates)
			assert.Empty(t, env.grantRepo.updateCalls)
		})
	}
}

func TestPolicyShowRunsWithoutTTY(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.interactive = false
	env.expectSSHAsset(t, 5, "web-01", &policy.CommandPolicy{
		AllowList: []string{"uptime"},
		DenyList:  []string{"rm -rf *"},
	})

	code := env.run("show", "web-01")

	assert.Equal(t, 0, code)
	out := env.stdoutBuf.String()
	assert.Contains(t, out, "uptime")
	assert.Contains(t, out, "rm -rf *")
}

// --- 写入路径：回显、二次确认、落库、审计 ---

func TestPolicyAllowEchoesLandedRulesAndWritesAfterConfirm(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", nil)

	code := env.run("allow", "web-01", "--", "systemctl restart nginx", "df -h")

	require.Equal(t, 0, code)
	stderr := env.stderrBuf.String()
	assert.Contains(t, stderr, "systemctl restart nginx")
	assert.Contains(t, stderr, "df -h")
	// 回显发生在确认之前，且提示里出现确认字样。
	assert.Contains(t, stderr, "y")

	require.Len(t, env.updates, 1)
	p, err := env.updates[0].GetCommandPolicy()
	require.NoError(t, err)
	assert.Contains(t, p.AllowList, "systemctl restart nginx")
	assert.Contains(t, p.AllowList, "df -h")

	require.Len(t, env.auditor.rows, 1)
	assert.Equal(t, "policy_rule", env.auditor.rows[0].ToolName)
}

func TestPolicyAllowDeclinedConfirmWritesNothing(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.confirmIn = strings.NewReader("n\n")
	env.expectSSHAsset(t, 5, "web-01", nil)

	code := env.run("allow", "web-01", "--", "uptime")

	assert.Equal(t, 1, code)
	assert.Empty(t, env.updates)
	assert.Empty(t, env.auditor.rows)
}

func TestPolicyDenyWritesDenySide(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", nil)

	code := env.run("deny", "web-01", "--", "rm -rf *")

	require.Equal(t, 0, code)
	require.Len(t, env.updates, 1)
	p, err := env.updates[0].GetCommandPolicy()
	require.NoError(t, err)
	assert.Contains(t, p.DenyList, "rm -rf *")
	assert.NotContains(t, p.AllowList, "rm -rf *")
}

// database 落点只表达语句类型：回显必须标注"结果比请求更宽"。
func TestPolicyAllowMarksBroaderLanding(t *testing.T) {
	env := newPolicyTestEnv(t)
	asset := &asset_entity.Asset{ID: 6, Name: "db-01", Type: asset_entity.AssetTypeDatabase}
	env.assetRepo.EXPECT().Find(gomock.Any(), int64(6)).Return(asset, nil).AnyTimes()
	env.assetRepo.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
	env.assetRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *asset_entity.Asset) error {
		env.updates = append(env.updates, a)
		return nil
	}).AnyTimes()

	code := env.run("allow", "db-01", "--", "SELECT * FROM users")

	require.Equal(t, 0, code)
	assert.Contains(t, env.stderrBuf.String(), "SELECT")
	assert.Contains(t, env.stderrBuf.String(), policyBroaderMark)
	require.Len(t, env.updates, 1)
	p, err := env.updates[0].GetQueryPolicy()
	require.NoError(t, err)
	assert.Contains(t, p.AllowTypes, "SELECT")
}

// --- 遮蔽检测（决策 19）：拒绝写入并点名遮蔽方与出路 ---

func TestPolicyAllowShadowedByOwnDenyIsRefused(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", &policy.CommandPolicy{DenyList: []string{"rm -rf *"}})

	code := env.run("allow", "web-01", "--", "rm -rf /data/cache")

	assert.Equal(t, 1, code)
	stderr := env.stderrBuf.String()
	assert.Contains(t, stderr, "rm -rf *")
	assert.Contains(t, stderr, "web-01")
	assert.Contains(t, stderr, "opsctl policy rm")
	assert.Empty(t, env.updates)
	assert.Empty(t, env.auditor.rows)
}

func TestPolicyAllowShadowedByPolicyGroupGivesCopyDetachRoute(t *testing.T) {
	env := newPolicyTestEnv(t)
	// 默认策略引用内置危险拒绝组：builtin:dangerous-deny 的 deny 规则参与遮蔽。
	env.expectSSHAsset(t, 5, "web-01", &policy.CommandPolicy{
		Groups: []string{policy.BuiltinDangerousDeny},
	})

	// 从内置组里挑一条真实存在的 deny 规则做遮蔽方。
	builtin := findBuiltinDenyFor(t, policy.BuiltinDangerousDeny)
	code := env.run("allow", "web-01", "--", builtin)

	assert.Equal(t, 1, code)
	stderr := env.stderrBuf.String()
	assert.Contains(t, stderr, builtin)
	assert.Contains(t, stderr, policy.BuiltinDangerousDeny)
	assert.Contains(t, stderr, "opsctl policy group copy")
	assert.Contains(t, stderr, "opsctl policy detach")
	assert.Empty(t, env.updates)
}

func findBuiltinDenyFor(t *testing.T, builtinID string) string {
	t.Helper()
	pg := policy_group_entity.FindBuiltin(builtinID)
	require.NotNil(t, pg)
	var cp policy.CommandPolicy
	require.NoError(t, json.Unmarshal([]byte(pg.Policy), &cp))
	require.NotEmpty(t, cp.DenyList)
	return cp.DenyList[0]
}

// --- 全或无：任一目标失败整体失败 ---

func TestPolicyAllowAllOrNothingAcrossTargets(t *testing.T) {
	env := newPolicyTestEnv(t)
	clean := &asset_entity.Asset{ID: 5, Name: "web-01", Type: asset_entity.AssetTypeSSH}
	blocked := &asset_entity.Asset{ID: 7, Name: "web-02", Type: asset_entity.AssetTypeSSH}
	require.NoError(t, blocked.SetCommandPolicy(&policy.CommandPolicy{DenyList: []string{"rm -rf *"}}))
	for _, a := range []*asset_entity.Asset{clean, blocked} {
		env.assetRepo.EXPECT().Find(gomock.Any(), a.ID).Return(a, nil).AnyTimes()
	}
	env.assetRepo.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{clean, blocked}, nil).AnyTimes()
	env.assetRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, x *asset_entity.Asset) error {
		env.updates = append(env.updates, x)
		return nil
	}).AnyTimes()

	code := env.run("allow", "web-01", "web-02", "--", "rm -rf /data/cache")

	assert.Equal(t, 1, code)
	assert.Empty(t, env.updates, "clean target must not be written when the other fails")
}

// --- 目标解析：--type 的两种语义 ---

func TestPolicyGroupTargetRequiresType(t *testing.T) {
	env := newPolicyTestEnv(t)
	group := &group_entity.Group{ID: 2, Name: "prod"}
	env.groupRepo.EXPECT().Find(gomock.Any(), int64(2)).Return(group, nil).AnyTimes()
	env.groupRepo.EXPECT().List(gomock.Any()).Return([]*group_entity.Group{group}, nil).AnyTimes()
	env.groupRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)

	code := env.run("allow", "--group", "prod", "--", "uptime")

	assert.Equal(t, 1, code)
	assert.Contains(t, env.stderrBuf.String(), "--type")
	assert.Empty(t, env.groupUps)
}

func TestPolicyGroupTargetWritesGroupColumn(t *testing.T) {
	env := newPolicyTestEnv(t)
	group := &group_entity.Group{ID: 2, Name: "prod"}
	env.groupRepo.EXPECT().Find(gomock.Any(), int64(2)).Return(group, nil).AnyTimes()
	env.groupRepo.EXPECT().List(gomock.Any()).Return([]*group_entity.Group{group}, nil).AnyTimes()
	env.groupRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, g *group_entity.Group) error {
		env.groupUps = append(env.groupUps, g)
		return nil
	}).AnyTimes()

	code := env.run("allow", "--group", "prod", "--type", "ssh", "--", "uptime")

	require.Equal(t, 0, code)
	require.Len(t, env.groupUps, 1)
	p, err := env.groupUps[0].GetCommandPolicy()
	require.NoError(t, err)
	assert.Contains(t, p.AllowList, "uptime")
}

func TestPolicyTypeAssertionMismatchFailsBeforeWrite(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", nil)

	code := env.run("allow", "web-01", "--type", "redis", "--", "GET *")

	assert.Equal(t, 1, code)
	assert.Contains(t, env.stderrBuf.String(), "redis")
	assert.Empty(t, env.updates)
}

// 归一化为空（OSS 目录标记场景）：什么都不落并报错。
func TestPolicyAllowNormalizationEmptyFails(t *testing.T) {
	env := newPolicyTestEnv(t)
	asset := &asset_entity.Asset{ID: 8, Name: "oss-01", Type: asset_entity.AssetTypeOSS}
	env.assetRepo.EXPECT().Find(gomock.Any(), int64(8)).Return(asset, nil).AnyTimes()
	env.assetRepo.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
	env.assetRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)

	code := env.run("allow", "oss-01", "--", "object delete mybucket/logs/")

	assert.Equal(t, 1, code)
	assert.Contains(t, env.stderrBuf.String(), "object delete mybucket/logs/")
	assert.Empty(t, env.updates)
}

// --- show：来源层标注、遮蔽标注、grant 剩余时间 ---

func TestPolicyShowAttributesLayersAndGrants(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", &policy.CommandPolicy{
		AllowList: []string{"uptime"},
		Groups:    []string{policy.BuiltinDangerousDeny},
	})
	builtinDeny := findBuiltinDenyFor(t, policy.BuiltinDangerousDeny)
	env.grantRepo.items = []*grant_entity.GrantItem{{
		ID: 12, GrantSessionID: "sess-1", ToolName: "exec", AssetID: 5, Command: "df -h", Createtime: time.Now().Add(-2 * time.Hour).Unix(),
	}}

	code := env.run("show", "web-01")

	require.Equal(t, 0, code)
	out := env.stdoutBuf.String()
	assert.Contains(t, out, "uptime")
	assert.Contains(t, out, builtinDeny)
	assert.Contains(t, out, policy.BuiltinDangerousDeny)
	assert.Contains(t, out, "df -h")
	assert.Contains(t, out, "h left", "grant remaining time rendered")
}

// --- rm：撤自身规则与 grant ---

func TestPolicyRmRemovesOwnRule(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", &policy.CommandPolicy{
		AllowList: []string{"uptime", "df -h"},
		DenyList:  []string{"reboot"},
	})

	// show 编号：allow 先、deny 后，按列顺序连续编号（#1 uptime / #2 df -h / #3 reboot）。
	require.Equal(t, 0, env.run("show", "web-01"))

	code := env.run("rm", "web-01", "2")

	require.Equal(t, 0, code)
	require.Len(t, env.updates, 1)
	p, err := env.updates[0].GetCommandPolicy()
	require.NoError(t, err)
	assert.Equal(t, []string{"uptime"}, p.AllowList)
	assert.Equal(t, []string{"reboot"}, p.DenyList)

	// 撤一条不存在的编号要失败。
	env.updates = nil
	assert.Equal(t, 1, env.run("rm", "web-01", "9"))
	assert.Empty(t, env.updates)
}

func TestPolicyRmRemovesGrantItem(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", nil)
	env.grantRepo.items = []*grant_entity.GrantItem{
		{ID: 12, GrantSessionID: "sess-1", ToolName: "exec", AssetID: 5, Command: "df -h"},
		{ID: 13, GrantSessionID: "sess-1", ToolName: "exec", AssetID: 5, Command: "uptime"},
	}

	code := env.run("rm", "web-01", "g12")

	require.Equal(t, 0, code)
	require.Len(t, env.grantRepo.updateCalls, 1)
	remaining := env.grantRepo.updateCalls[0]
	require.Len(t, remaining, 1)
	assert.Equal(t, "uptime", remaining[0].Command)
}

// --- 终端"永久允许"接缝（决策 13）：与 policy allow 同一条写入路径 ---

func TestWriteAllowAlwaysRuleWritesPermanentRule(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", nil)

	err := writeAllowAlwaysRule(aictx.WithAuditSource(env.ctx, "opsctl"), 5, "exec", []string{"uptime"})

	require.NoError(t, err)
	require.Len(t, env.updates, 1)
	p, err := env.updates[0].GetCommandPolicy()
	require.NoError(t, err)
	assert.Contains(t, p.AllowList, "uptime")
	assert.NotContains(t, p.DenyList, "uptime")
}

func TestWriteAllowAlwaysRuleCpNeedsDirection(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", nil)

	err := writeAllowAlwaysRule(env.ctx, 5, "cp", []string{`/etc/\*`})

	require.Error(t, err)
	assert.Empty(t, env.updates)
}

// --- locale：给人读的跟随 ctx，机器可读的恒定 ---

func TestPolicyOutputFollowsLocaleButMarkerStaysASCII(t *testing.T) {
	env := newPolicyTestEnv(t)
	env.interactive = false
	env.ctx = aictx.WithPolicyLang(env.ctx, "zh-cn")
	env.expectSSHAsset(t, 5, "web-01", nil)

	code := env.run("allow", "web-01", "--", "uptime")

	assert.Equal(t, refusalExitCode, code)
	lines := strings.Split(strings.TrimSpace(env.stderrBuf.String()), "\n")
	require.NotEmpty(t, lines)
	assert.Equal(t, needsTTYMarker, lines[0])
	assert.Contains(t, env.stderrBuf.String(), "终端", "human-readable body follows locale")
}

// face 进 pattern 的照抄形态（spec 定稿：资产目标的 --type 是资产类型断言、可省略，
// cp 的 face 属于 pattern 空间——cp:read:/cp:write: 前缀即 grant 层的方向隔离语义）：
// `opsctl policy allow <asset> -- 'cp:write:/x'` 必须可执行，且原样落成该资产
// CommandPolicy 里的方向化 cp 规则——不能被拒收，也不能丢前缀落成普通命令规则。
func TestPolicyAllowFacePrefixedPatternLandsDirectionalCpRule(t *testing.T) {
	for _, facePat := range []string{"cp:write:/etc/app/config.yml", "cp:read:/var/log/app/"} {
		env := newPolicyTestEnv(t)
		env.expectSSHAsset(t, 5, "web-01", nil)

		code := env.run("allow", "web-01", "--", facePat)

		require.Equal(t, 0, code, "face-prefixed pattern must not be rejected")
		require.Len(t, env.updates, 1)
		p, err := env.updates[0].GetCommandPolicy()
		require.NoError(t, err)
		assert.Contains(t, p.AllowList, facePat, "landed rule keeps the directional cp prefix verbatim")
		assert.NotContains(t, p.DenyList, facePat)
	}
}

// --type=x / --group=x 的内联形态不得吞掉后面的位置参数（parsePolicyGroupFlags 的
// inline 判定是同一条约定）。
func TestParsePolicyWriteFlagsInlineEqualsForm(t *testing.T) {
	declared, groups, targets, err := parsePolicyWriteFlags([]string{"--type=ssh", "web-01"})
	require.NoError(t, err)
	assert.Equal(t, "ssh", declared)
	assert.Empty(t, groups)
	assert.Equal(t, []string{"web-01"}, targets)

	_, groups, targets, err = parsePolicyWriteFlags([]string{"--group=prod", "web-01"})
	require.NoError(t, err)
	assert.Equal(t, []string{"prod"}, groups)
	assert.Equal(t, []string{"web-01"}, targets)

	declared, groups, targets, err = parsePolicyWriteFlags([]string{"web-01", "--type=ssh"})
	require.NoError(t, err)
	assert.Equal(t, "ssh", declared)
	assert.Empty(t, groups)
	assert.Equal(t, []string{"web-01"}, targets)
}
