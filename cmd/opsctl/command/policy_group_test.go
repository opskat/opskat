package command

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/repository/policy_group_repo"
	"github.com/opskat/opskat/internal/repository/policy_group_repo/mock_policy_group_repo"
)

// --- 测试脚手架：在 T5 的 policy 环境上补 policy_group_repo 注入缝 ---

type policyGroupTestEnv struct {
	*policyTestEnv
	pgRepo    *mock_policy_group_repo.MockPolicyGroupRepo
	creates   []*policy_group_entity.PolicyGroup
	pgUpdates []*policy_group_entity.PolicyGroup
	pgDeletes []int64
	nextAuto  int64
}

func newPolicyGroupTestEnv(t *testing.T) *policyGroupTestEnv {
	t.Helper()
	env := &policyGroupTestEnv{
		policyTestEnv: newPolicyTestEnv(t),
		nextAuto:      77,
	}
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	env.pgRepo = mock_policy_group_repo.NewMockPolicyGroupRepo(ctrl)
	orig := policy_group_repo.PolicyGroup()
	policy_group_repo.RegisterPolicyGroup(env.pgRepo)
	t.Cleanup(func() { policy_group_repo.RegisterPolicyGroup(orig) })
	return env
}

func decodeCommandPolicy(t *testing.T, policyJSON string) *policyent.CommandPolicy {
	t.Helper()
	cp := &policyent.CommandPolicy{}
	require.NoError(t, json.Unmarshal([]byte(policyJSON), cp))
	return cp
}

func userCommandGroup(id int64, name string, cp *policyent.CommandPolicy) *policy_group_entity.PolicyGroup {
	pg := &policy_group_entity.PolicyGroup{ID: id, Name: name, PolicyType: policyent.PolicyKindCommand}
	if cp != nil {
		data, _ := json.Marshal(cp)
		pg.Policy = string(data)
	}
	return pg
}

// expectUserGroup 注册一个用户组的 Find 期望并记录 Update 调用。
func (env *policyGroupTestEnv) expectUserGroup(pg *policy_group_entity.PolicyGroup) {
	env.pgRepo.EXPECT().Find(gomock.Any(), pg.ID).Return(pg, nil).AnyTimes()
	env.pgRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, x *policy_group_entity.PolicyGroup) error {
		env.pgUpdates = append(env.pgUpdates, x)
		return nil
	}).AnyTimes()
}

// expectCreateSuccess 记录 Create 调用并模拟自增 ID。
func (env *policyGroupTestEnv) expectCreateSuccess() {
	env.pgRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, pg *policy_group_entity.PolicyGroup) error {
		env.creates = append(env.creates, pg)
		pg.ID = env.nextAuto
		env.nextAuto++
		return nil
	}).AnyTimes()
}

// --- TTY 门禁（spec Testing decisions：group 写类 / attach / detach 无 TTY exit 3） ---

func TestPolicyGroupAndAttachWriteSubcommandsNeedTTY(t *testing.T) {
	for _, args := range [][]string{
		{"group", "create", "--name", "ops", "--type", "command"},
		{"group", "copy", "builtin:linux-readonly", "--name", "ops"},
		{"group", "allow", "5", "--", "uptime"},
		{"group", "deny", "5", "--", "reboot"},
		{"group", "rm", "5"},
		{"group", "rm", "5", "2"},
		{"attach", "web-01", "builtin:linux-readonly"},
		{"detach", "web-01", "builtin:linux-readonly"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env := newPolicyGroupTestEnv(t)
			env.interactive = false
			env.expectSSHAsset(t, 5, "web-01", nil)
			env.pgRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)
			env.pgRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
			env.pgRepo.EXPECT().Delete(gomock.Any(), gomock.Any()).Times(0)

			code := env.run(args...)

			assert.Equal(t, refusalExitCode, code)
			lines := strings.Split(strings.TrimSpace(env.stderrBuf.String()), "\n")
			require.NotEmpty(t, lines)
			assert.Equal(t, needsTTYMarker, lines[0])
			assert.Empty(t, env.updates)
			assert.Empty(t, env.groupUps)
		})
	}
}

// list / show 与 policy show 同档：只读免 TTY。
func TestPolicyGroupListShowRunWithoutTTY(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.interactive = false
	env.pgRepo.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()
	env.pgRepo.EXPECT().ListByType(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	code := env.run("group", "list")
	require.Equal(t, 0, code)
	assert.Contains(t, env.stdoutBuf.String(), "builtin:linux-readonly")

	// run 的输出会累积在同一个 buffer 里，过滤断言前先清空。
	env.stdoutBuf.Reset()
	code = env.run("group", "list", "--type", "query")
	require.Equal(t, 0, code)
	assert.Contains(t, env.stdoutBuf.String(), "builtin:sql-readonly")
	assert.NotContains(t, env.stdoutBuf.String(), "builtin:linux-readonly")

	code = env.run("group", "show", "builtin:dangerous-deny")
	require.Equal(t, 0, code)
	assert.Contains(t, env.stdoutBuf.String(), "rm -rf /*")
	// 只读组要给出 copy 出路（恒定 ASCII 命令），不直通服务层中文错误串。
	assert.Contains(t, env.stdoutBuf.String(), "opsctl policy group copy builtin:dangerous-deny")
}

// --- copy：显式传名，不依赖服务层拼的中文默认后缀 ---

func TestPolicyGroupCopyExplicitNameAndPrintsNewID(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectCreateSuccess()

	code := env.run("group", "copy", "builtin:linux-readonly", "--name", "my-readonly")

	require.Equal(t, 0, code)
	require.Len(t, env.creates, 1)
	assert.Equal(t, "my-readonly", env.creates[0].Name)
	assert.NotContains(t, env.creates[0].Name, "副本")
	assert.Equal(t, policyent.PolicyKindCommand, env.creates[0].PolicyType)
	assert.Contains(t, env.stdoutBuf.String(), "77", "new group id printed for follow-up commands")
}

func TestPolicyGroupCopyRequiresExplicitName(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.pgRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)

	code := env.run("group", "copy", "builtin:linux-readonly")

	assert.Equal(t, 1, code)
	assert.Empty(t, env.creates)
}

func TestPolicyGroupCopyDeclinedConfirmCreatesNothing(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.confirmIn = strings.NewReader("n\n")
	env.pgRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)

	code := env.run("group", "copy", "builtin:linux-readonly", "--name", "ops")

	assert.Equal(t, 1, code)
	assert.Empty(t, env.creates)
}

// --- 内置 / 扩展组可写性：CLI 本地化拒绝理由 + ASCII copy 出路 ---

func TestPolicyGroupEditBuiltinRefusedWithLocalRoute(t *testing.T) {
	for _, args := range [][]string{
		{"group", "allow", "builtin:dangerous-deny", "--", "uptime"},
		{"group", "deny", "builtin:linux-readonly", "--", "reboot"},
		{"group", "rm", "builtin:dangerous-deny"},
		{"group", "rm", "builtin:dangerous-deny", "1"},
		{"group", "rm", "ext:oss/readonly", "1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env := newPolicyGroupTestEnv(t)
			env.pgRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
			env.pgRepo.EXPECT().Delete(gomock.Any(), gomock.Any()).Times(0)

			code := env.run(args...)

			assert.Equal(t, 1, code)
			stderr := env.stderrBuf.String()
			// 拒绝理由是 CLI 本地化消息：给出 copy 出路的命令原文，
			// 且不含服务层硬编码中文错误串（policy_group.go:124/:131）。
			assert.Contains(t, stderr, "opsctl policy group copy")
			assert.NotContains(t, stderr, "不可删除")
			assert.NotContains(t, stderr, "无效的权限组")
		})
	}
}

func TestPolicyGroupRefusalFollowsLocaleButRouteStaysASCII(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.ctx = aictx.WithPolicyLang(env.ctx, "zh-cn")
	env.pgRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)

	code := env.run("group", "rm", "builtin:dangerous-deny")

	assert.Equal(t, 1, code)
	stderr := env.stderrBuf.String()
	assert.Contains(t, stderr, "复制", "human-readable body follows locale")
	assert.Contains(t, stderr, "opsctl policy group copy builtin:dangerous-deny --name", "route stays constant ASCII")
}

// --- 决策 19 出路闭合：在用户组副本上删掉单条 deny ---

func TestPolicyGroupRmEntryRemovesOnlyThatRule(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectUserGroup(userCommandGroup(5, "ops-copy", &policyent.CommandPolicy{
		AllowList: []string{"uptime", "df -h"},
		DenyList:  []string{"rm -rf *"},
	}))

	// show 的编号：allow 先、deny 后（#1 uptime / #2 df -h / #3 rm -rf *）。
	code := env.run("group", "show", "5")
	require.Equal(t, 0, code)

	code = env.run("group", "rm", "5", "3")

	require.Equal(t, 0, code)
	require.Len(t, env.pgUpdates, 1)
	cp := decodeCommandPolicy(t, env.pgUpdates[0].Policy)
	assert.Equal(t, []string{"uptime", "df -h"}, cp.AllowList)
	assert.Empty(t, cp.DenyList, "only the deny entry is removed")

	// 不存在的编号要失败且不落任何改动。
	env.pgUpdates = nil
	assert.Equal(t, 1, env.run("group", "rm", "5", "9"))
	assert.Empty(t, env.pgUpdates)
}

func TestPolicyGroupRmWholeDeletesUserGroup(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectUserGroup(userCommandGroup(5, "ops-copy", nil))
	env.pgRepo.EXPECT().Delete(gomock.Any(), int64(5)).DoAndReturn(func(_ context.Context, id int64) error {
		env.pgDeletes = append(env.pgDeletes, id)
		return nil
	}).AnyTimes()

	code := env.run("group", "rm", "5")

	require.Equal(t, 0, code)
	assert.Equal(t, []int64{5}, env.pgDeletes)
	assert.Empty(t, env.pgUpdates)
}

// --- group allow / deny：写用户组自己的策略 JSON ---

func TestPolicyGroupAllowWritesUserGroupPolicy(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectUserGroup(userCommandGroup(5, "ops-copy", nil))

	code := env.run("group", "allow", "5", "--", "uptime")

	require.Equal(t, 0, code)
	require.Len(t, env.pgUpdates, 1)
	cp := decodeCommandPolicy(t, env.pgUpdates[0].Policy)
	assert.Contains(t, cp.AllowList, "uptime")

	require.Len(t, env.auditor.rows, 1)
	assert.Equal(t, "policy_rule", env.auditor.rows[0].ToolName)
}

func TestPolicyGroupAllowDeclinedConfirmWritesNothing(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.confirmIn = strings.NewReader("n\n")
	env.expectUserGroup(userCommandGroup(5, "ops-copy", nil))

	code := env.run("group", "allow", "5", "--", "uptime")

	assert.Equal(t, 1, code)
	assert.Empty(t, env.pgUpdates)
	assert.Empty(t, env.auditor.rows)
}

// query 形状的组只能落语句类型：回显必须标注"结果比请求更宽"（决策 12）。
func TestPolicyGroupAllowQueryKindLandsStatementType(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	pg := &policy_group_entity.PolicyGroup{ID: 6, Name: "sql-copy", PolicyType: policyent.PolicyKindQuery}
	env.expectUserGroup(pg)

	code := env.run("group", "allow", "6", "--", "SELECT * FROM users")

	require.Equal(t, 0, code)
	assert.Contains(t, env.stderrBuf.String(), policyBroaderMark)
	require.Len(t, env.pgUpdates, 1)
	qp := &policyent.QueryPolicy{}
	require.NoError(t, json.Unmarshal([]byte(env.pgUpdates[0].Policy), qp))
	assert.Contains(t, qp.AllowTypes, "SELECT")
}

func TestPolicyGroupAllowShadowedByOwnDenyIsRefused(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectUserGroup(userCommandGroup(5, "ops-copy", &policyent.CommandPolicy{
		DenyList: []string{"rm -rf *"},
	}))

	code := env.run("group", "allow", "5", "--", "rm -rf /data/cache")

	assert.Equal(t, 1, code)
	stderr := env.stderrBuf.String()
	assert.Contains(t, stderr, "rm -rf *")
	assert.Contains(t, stderr, "opsctl policy group rm 5")
	assert.Empty(t, env.pgUpdates)
	assert.Empty(t, env.auditor.rows)
}

// --- attach / detach：前置判定与 Groups 列 ---

func TestPolicyAttachTypeMismatchFailsBeforeWrite(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", nil)
	pg := &policy_group_entity.PolicyGroup{ID: 9, Name: "sql-copy", PolicyType: policyent.PolicyKindQuery}
	env.expectUserGroup(pg)

	code := env.run("attach", "web-01", "9")

	assert.Equal(t, 1, code)
	stderr := env.stderrBuf.String()
	assert.Contains(t, stderr, "query")
	assert.Contains(t, stderr, "ssh")
	assert.Empty(t, env.updates, "mismatch must fail before any write")
}

func TestPolicyAttachBuiltinGroupToAssetWritesGroupsColumn(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", nil)

	code := env.run("attach", "web-01", "builtin:linux-readonly")

	require.Equal(t, 0, code)
	require.Len(t, env.updates, 1)
	cp, err := env.updates[0].GetCommandPolicy()
	require.NoError(t, err)
	assert.Contains(t, cp.Groups, "builtin:linux-readonly")
	require.Len(t, env.auditor.rows, 1)
}

func TestPolicyAttachAlreadyAttachedIsRefused(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", &policyent.CommandPolicy{
		Groups: []string{"builtin:linux-readonly"},
	})

	code := env.run("attach", "web-01", "builtin:linux-readonly")

	assert.Equal(t, 1, code)
	assert.Empty(t, env.updates)
}

func TestPolicyDetachRemovesGroupRef(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", &policyent.CommandPolicy{
		Groups: []string{"builtin:linux-readonly", "builtin:dangerous-deny"},
	})

	code := env.run("detach", "web-01", "builtin:linux-readonly")

	require.Equal(t, 0, code)
	require.Len(t, env.updates, 1)
	cp, err := env.updates[0].GetCommandPolicy()
	require.NoError(t, err)
	assert.Equal(t, []string{"builtin:dangerous-deny"}, cp.Groups)
}

func TestPolicyDetachMissingRefFails(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectSSHAsset(t, 5, "web-01", nil)

	code := env.run("detach", "web-01", "builtin:linux-readonly")

	assert.Equal(t, 1, code)
	assert.Empty(t, env.updates)
}

// k8s 资产的引用组按 K8sPolicy 解释落库（type_registry.go 的注册语义：k8s 列的
// 引用组按 command 表解析）。资产只有一列 command_policy、按形状解释，因此判据是
// K8sPolicy 视角能读到引用组——运行时 collectK8sPolicies 读的就是这个视角。
func TestPolicyAttachK8sAssetWritesK8sColumn(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	asset := &asset_entity.Asset{ID: 7, Name: "k8s-01", Type: asset_entity.AssetTypeK8s}
	env.assetRepo.EXPECT().Find(gomock.Any(), int64(7)).Return(asset, nil).AnyTimes()
	env.assetRepo.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
	env.assetRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *asset_entity.Asset) error {
		env.updates = append(env.updates, a)
		return nil
	}).AnyTimes()

	code := env.run("attach", "k8s-01", "builtin:k8s-readonly")

	require.Equal(t, 0, code)
	require.Len(t, env.updates, 1)
	kp, err := env.updates[0].GetK8sPolicy()
	require.NoError(t, err)
	assert.Contains(t, kp.Groups, "builtin:k8s-readonly")
}

// 组目标：列由所挂权限组自己的 PolicyType 决定，任何 kind 都可能生效。
func TestPolicyAttachGroupTargetWritesKindColumn(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	group := &group_entity.Group{ID: 2, Name: "prod"}
	env.groupRepo.EXPECT().Find(gomock.Any(), int64(2)).Return(group, nil).AnyTimes()
	env.groupRepo.EXPECT().List(gomock.Any()).Return([]*group_entity.Group{group}, nil).AnyTimes()
	env.groupRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, g *group_entity.Group) error {
		env.groupUps = append(env.groupUps, g)
		return nil
	}).AnyTimes()

	code := env.run("attach", "--group", "prod", "builtin:sql-readonly")

	require.Equal(t, 0, code)
	require.Len(t, env.groupUps, 1)
	qp, err := env.groupUps[0].GetQueryPolicy()
	require.NoError(t, err)
	assert.Contains(t, qp.Groups, "builtin:sql-readonly")
}

// --- create ---

func TestPolicyGroupCreateWritesUserGroup(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.expectCreateSuccess()

	code := env.run("group", "create", "--name", "ops", "--type", "redis")

	require.Equal(t, 0, code)
	require.Len(t, env.creates, 1)
	assert.Equal(t, "ops", env.creates[0].Name)
	assert.Equal(t, policyent.PolicyKindRedis, env.creates[0].PolicyType)
	require.Len(t, env.auditor.rows, 1)
}

func TestPolicyGroupCreateValidatesNameAndType(t *testing.T) {
	env := newPolicyGroupTestEnv(t)
	env.pgRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)

	assert.Equal(t, 1, env.run("group", "create", "--type", "redis"))
	assert.Equal(t, 1, env.run("group", "create", "--name", "ops", "--type", "no-such-kind"))
	assert.Empty(t, env.creates)
}
