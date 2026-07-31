package permission

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

// ossStubPolicyStrings 是 exec DSL → 策略串的一张小表，用来在本包里扮演注册进来的
// PolicyStringsFunc。
//
// 真正的派生是 helper.ParseOSSCommand + (*OSSCommand).PolicyStrings()，由
// internal/ai/helper/oss_dsl_test.go 逐个操作锁住；本包不能 import 它——helper 反过来
// import 本包（transfer_ssh.go 取 GrantToolCp），会成 import cycle。生产接线是
// internal/ai/execimpl 在 init() 里调 RegisterPolicyStrings，本表只保证判定链拿到与
// 生产同形的输入。表里没有的命令按"解析失败"返回错误，与真解析器一致。
var ossStubPolicyStrings = map[string][]string{
	"object stat mybucket/report.txt":                 {"object.read mybucket/report.txt"},
	"object delete mybucket/report.txt":               {"object.delete mybucket/report.txt"},
	"object presign mybucket/report.txt --method=put": {"object.presign.write mybucket/report.txt"},
	"object presign mybucket/report.txt --method=get": {"object.presign.read mybucket/report.txt"},
	"object copy mybucket/a --to=other/b":             {"object.read mybucket/a", "object.write other/b"},
	"object move mybucket/a --to=other/b":             {"object.read mybucket/a", "object.write other/b", "object.delete mybucket/a"},
	"object delete mybucket/logs/":                    {"object.delete mybucket/logs/"},
	"object list mybucket/":                           {"object.list mybucket/"},
	"object move mybucket/a --to=other/logs/":         {"object.read mybucket/a", "object.write other/logs/", "object.delete mybucket/a"},
	"bucket list": {"bucket.list *"},
	"object put mybucket/report.txt --file=/tmp/r.txt": {"object.write mybucket/report.txt"},
}

// withOSSPolicyStrings 注册上表并在测试结束后还原，仓内 mock 惯例（全局单例 + t.Cleanup）。
func withOSSPolicyStrings(t *testing.T) {
	t.Helper()
	RegisterPolicyStrings(asset_entity.AssetTypeOSS, func(command string) ([]string, error) {
		if ps, ok := ossStubPolicyStrings[command]; ok {
			return ps, nil
		}
		return nil, fmt.Errorf("stub: cannot parse oss command %q", command)
	})
	t.Cleanup(func() { UnregisterPolicyStringsForTest(asset_entity.AssetTypeOSS) })
}

// withOSSGrants 注册一个 stub grant 仓库并写入 patterns（tool_name=oss），返回带 sessionID 的 ctx。
func withOSSGrants(t *testing.T, ctx context.Context, patterns ...string) context.Context {
	t.Helper()
	stub := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stub)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})

	const sessionID = "sess-oss"
	stub.sessions[sessionID] = &grant_entity.GrantSession{ID: sessionID, Status: grant_entity.GrantStatusApproved}
	for _, p := range patterns {
		stub.items[sessionID] = append(stub.items[sessionID], &grant_entity.GrantItem{
			GrantSessionID: sessionID, AssetID: 1, ToolName: asset_entity.AssetTypeOSS, Command: p,
		})
	}
	return aictx.WithSessionID(ctx, sessionID)
}

// TestCheckPermission_OSSDefaultPolicy 锁设计 §4.4 的三种结果：默认策略（builtin:oss-readonly
// + builtin:oss-dangerous-deny）下 object stat 放行、object delete 需确认、presign PUT 拒绝。
func TestCheckPermission_OSSDefaultPolicy(t *testing.T) {
	withOSSPolicyStrings(t)
	ctx, mockAsset, _ := setupPolicyTest(t)
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
		Return(&asset_entity.Asset{ID: 1, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}, nil).AnyTimes()

	allowed := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object stat mybucket/report.txt")
	assert.Equal(t, aictx.Allow, allowed.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, allowed.DecisionSource)

	listBuckets := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "bucket list")
	assert.Equal(t, aictx.Allow, listBuckets.Decision)

	needConfirm := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object delete mybucket/report.txt")
	assert.Equal(t, aictx.NeedConfirm, needConfirm.Decision)
	assert.Contains(t, needConfirm.HintRules, "object.read *")

	// presign GET 留在审批，只有 presign PUT 进地板（设计 §4.4 / D9）。
	presignGet := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object presign mybucket/report.txt --method=get")
	assert.Equal(t, aictx.NeedConfirm, presignGet.Decision)

	denied := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object presign mybucket/report.txt --method=put")
	assert.Equal(t, aictx.Deny, denied.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, denied.DecisionSource)
	assert.Equal(t, "object.presign.write *", denied.MatchedPattern)
}

// TestCheckPermission_OSSCopyChecksEveryPolicyString 锁决策 D7：copy/move 派生的每条策略串
// 都参与判定。只看目的地就等于放行"把受限对象复制到可读位置再读"的绕过路径。
func TestCheckPermission_OSSCopyChecksEveryPolicyString(t *testing.T) {
	withOSSPolicyStrings(t)

	t.Run("源被 deny 时整条 copy 被拒，哪怕目的地允许", func(t *testing.T) {
		ctx, mockAsset, _ := setupPolicyTest(t)
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(&asset_entity.Asset{
			ID: 1, Type: asset_entity.AssetTypeOSS,
			CmdPolicy: mustJSON(asset_entity.OSSPolicy{
				AllowList: []string{"object.* *"},
				DenyList:  []string{"object.read mybucket/*"},
			}),
		}, nil).AnyTimes()

		got := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object copy mybucket/a --to=other/b")
		assert.Equal(t, aictx.Deny, got.Decision)
		assert.Equal(t, "object.read mybucket/*", got.MatchedPattern)
	})

	t.Run("allow 名单必须覆盖每一条派生串", func(t *testing.T) {
		ctx, mockAsset, _ := setupPolicyTest(t)
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(&asset_entity.Asset{
			ID: 1, Type: asset_entity.AssetTypeOSS,
			CmdPolicy: mustJSON(asset_entity.OSSPolicy{
				AllowList: []string{"object.read mybucket/*", "object.write other/*"},
			}),
		}, nil).AnyTimes()

		// 读源 + 写目的都被 allow 覆盖。
		copied := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object copy mybucket/a --to=other/b")
		assert.Equal(t, aictx.Allow, copied.Decision)

		// move 还要删源，allow 名单没有 object.delete，整条命令退回审批。
		moved := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object move mybucket/a --to=other/b")
		assert.Equal(t, aictx.NeedConfirm, moved.Decision)
	})
}

// TestCheckPermission_OSSGrantMatchesEveryPolicyString 锁 §4.1 第 5 步：DB Grant 也要逐条命中，
// 一条 grant 不能覆盖 copy/move 的多个资源。
func TestCheckPermission_OSSGrantMatchesEveryPolicyString(t *testing.T) {
	withOSSPolicyStrings(t)

	t.Run("只批了读源，move 仍不放行", func(t *testing.T) {
		ctx, mockAsset, _ := setupPolicyTest(t)
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
			Return(&asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeOSS}, nil).AnyTimes()
		ctx = withOSSGrants(t, ctx, "object.read mybucket/*")

		got := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object move mybucket/a --to=other/b")
		assert.Equal(t, aictx.NeedConfirm, got.Decision)
	})

	t.Run("三条都批了才放行", func(t *testing.T) {
		ctx, mockAsset, _ := setupPolicyTest(t)
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
			Return(&asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeOSS}, nil).AnyTimes()
		ctx = withOSSGrants(t, ctx, "object.read mybucket/*", "object.write other/*", "object.delete mybucket/*")

		got := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object move mybucket/a --to=other/b")
		assert.Equal(t, aictx.Allow, got.Decision)
		assert.Equal(t, aictx.SourceGrantAllow, got.DecisionSource)
	})

	t.Run("grant 不能翻越默认地板", func(t *testing.T) {
		ctx, mockAsset, _ := setupPolicyTest(t)
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
			Return(&asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeOSS}, nil).AnyTimes()
		ctx = withOSSGrants(t, ctx, "object.presign.write *")

		got := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object presign mybucket/report.txt --method=put")
		assert.Equal(t, aictx.Deny, got.Decision)
		assert.Equal(t, aictx.SourcePolicyDeny, got.DecisionSource)
	})
}

// TestCheckPermission_OSSAcceptsPolicyStringSubject 锁设计 §6.2：cp 的 OSS 端点把
// ApprovalSubject 给出的策略串（object.read <bucket>/<key>）直接送进同一个检查，
// 因此这条形状必须不经 DSL 解析器就能判定——D18 依赖它在默认只读组下自动放行。
func TestCheckPermission_OSSAcceptsPolicyStringSubject(t *testing.T) {
	// 刻意不注册派生函数：策略串形状本来就不需要它。
	ctx, mockAsset, _ := setupPolicyTest(t)
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
		Return(&asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeOSS}, nil).AnyTimes()

	read := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object.read mybucket/app.log")
	assert.Equal(t, aictx.Allow, read.Decision)

	list := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object.list mybucket/logs/")
	assert.Equal(t, aictx.Allow, list.Decision)

	write := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object.write mybucket/app.log")
	assert.Equal(t, aictx.NeedConfirm, write.Decision)

	presignPut := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object.presign.write mybucket/app.log")
	assert.Equal(t, aictx.Deny, presignPut.Decision)
}

// TestCheckPermission_OSSWithoutDeriverFailsClosed 锁 RegisterPolicyStrings 缺席时的姿态：
// DSL 形状的命令退回 NeedConfirm，绝不能因为拿原串去撞规则而被 `*` 放行，也不能把
// presign PUT 这类地板操作降级成一次可批准的审批之外的东西。
func TestCheckPermission_OSSWithoutDeriverFailsClosed(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(&asset_entity.Asset{
		ID: 1, Type: asset_entity.AssetTypeOSS,
		CmdPolicy: mustJSON(asset_entity.OSSPolicy{AllowList: []string{"*"}}),
	}, nil).AnyTimes()

	got := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object stat mybucket/report.txt")
	assert.Equal(t, aictx.NeedConfirm, got.Decision)
}

// TestNormalizeGrantPatterns_OSS 锁 §4.3 与决策 D20。
func TestNormalizeGrantPatterns_OSS(t *testing.T) {
	withOSSPolicyStrings(t)

	t.Run("copy 落两条 grant，move 落三条", func(t *testing.T) {
		assert.Equal(t, []string{"object.read mybucket/a", "object.write other/b"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object copy mybucket/a --to=other/b"))
		assert.Equal(t, []string{"object.read mybucket/a", "object.write other/b", "object.delete mybucket/a"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object move mybucket/a --to=other/b"))
	})

	t.Run("resource 以 / 结尾的派生串不落成 grant", func(t *testing.T) {
		// 目录标记是合法的单个对象，删得掉；但同一个串当规则读是"递归前缀"，
		// 落库就等于一条递归删除授权（D20）。
		assert.Empty(t, NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object delete mybucket/logs/"))
		assert.Empty(t, NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object list mybucket/"))
	})

	t.Run("只丢前缀形状的那一条，其余照落", func(t *testing.T) {
		assert.Equal(t, []string{"object.read mybucket/a", "object.delete mybucket/a"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object move mybucket/a --to=other/logs/"))
	})

	t.Run("用户手写的策略形式 pattern 原样存下，含前缀形状", func(t *testing.T) {
		assert.Equal(t, []string{"object.read mybucket/logs/"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object.read mybucket/logs/"))
		assert.Equal(t, []string{"object.* *"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object.* *"))
	})

	t.Run("派生失败退回原串", func(t *testing.T) {
		assert.Equal(t, []string{"object frobnicate mybucket/a"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object frobnicate mybucket/a"))
	})
}
