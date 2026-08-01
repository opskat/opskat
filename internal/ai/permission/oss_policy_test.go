package permission

import (
	"context"
	"fmt"
	"path"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

// stubAuditRepo 只记下写进来的审计行，供"落了几条 grant 就该有几条 grant_submit"这类
// 断言使用。仓内 mock 惯例：注册全局单例 + t.Cleanup 还原。
type stubAuditRepo struct{ entries []*audit_entity.AuditLog }

func (r *stubAuditRepo) Create(_ context.Context, log *audit_entity.AuditLog) error {
	r.entries = append(r.entries, log)
	return nil
}

func (r *stubAuditRepo) List(context.Context, audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	return nil, 0, nil
}

func (r *stubAuditRepo) ListSessions(context.Context, int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

func withStubAudit(t *testing.T) *stubAuditRepo {
	t.Helper()
	stub := &stubAuditRepo{}
	orig := audit_repo.Audit()
	audit_repo.RegisterAudit(stub)
	t.Cleanup(func() { audit_repo.RegisterAudit(orig) })
	return stub
}

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
	"object put mybucket/report.txt --file=/tmp/r.txt":         {"object.write mybucket/report.txt"},
	"object copy mybucket/secrets/key.pem --to=public/key.pem": {"object.read mybucket/secrets/key.pem", "object.write public/key.pem"},
	"object delete mybucket/tmp/old.log":                       {"object.delete mybucket/tmp/old.log"},
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

// TestCheckPermission_OSSGroupGenericPolicy 锁 §4.1 的第 2 步与第 4 步：组的**通用**命令策略
// （groups.command_policy 列，与喂给 collectOSSPolicies 的 groups.oss_policy 是两列，只有
// CheckGroupGenericPolicy 这一条路能读到它）也参与 OSS 判定，且比对必须用 MatchOSSRule。
//
// 匹配函数不是形式选择：MatchCommandRule 把规则读成 `<程序> <子命令>` 并要求程序段字面
// 相等，于是 `object.* mybucket/secrets/` 的程序段 "object.*" 对不上命令的 "object.read"，
// 一条本该拦住的 deny 规则一条都匹配不上——静默 fail-open，正是 §4.1 第 2 步那句注释
// （以及 mongo/redis/etcd 传各自匹配器）在防的事故。同一条规则里的尾随 "/" 递归前缀
// （决策 D5）也只有 MatchOSSRule 懂。
func TestCheckPermission_OSSGroupGenericPolicy(t *testing.T) {
	withOSSPolicyStrings(t)

	t.Run("组通用 deny 拦住整条 copy，哪怕资产 allow 是 *", func(t *testing.T) {
		ctx, mockAsset, stubGrp := setupPolicyTest(t)
		stubGrp.groups[10] = &group_entity.Group{
			ID: 10, Name: "prod",
			CmdPolicy: `{"deny_list":["object.* mybucket/secrets/"]}`,
		}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(&asset_entity.Asset{
			ID: 1, Type: asset_entity.AssetTypeOSS, GroupID: 10,
			CmdPolicy: mustJSON(asset_entity.OSSPolicy{AllowList: []string{"*"}}),
		}, nil).AnyTimes()

		// deny 只命中"读源"那一条，目的地不受限：D7 的安全点换到组通用这一层同样成立。
		got := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object copy mybucket/secrets/key.pem --to=public/key.pem")
		assert.Equal(t, aictx.Deny, got.Decision)
		assert.Equal(t, aictx.SourcePolicyDeny, got.DecisionSource)
		assert.Equal(t, "object.* mybucket/secrets/", got.MatchedPattern)
	})

	t.Run("组通用 allow 盖过类型策略的 NeedConfirm", func(t *testing.T) {
		ctx, mockAsset, stubGrp := setupPolicyTest(t)
		stubGrp.groups[10] = &group_entity.Group{
			ID: 10, Name: "prod",
			CmdPolicy: `{"allow_list":["object.delete mybucket/tmp/"]}`,
		}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
			Return(&asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeOSS, GroupID: 10}, nil).AnyTimes()

		// 默认只读组不含 object.delete，类型策略给的是 NeedConfirm。
		got := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object delete mybucket/tmp/old.log")
		assert.Equal(t, aictx.Allow, got.Decision)
		assert.Equal(t, "object.delete mybucket/tmp/", got.MatchedPattern)
	})

	t.Run("组通用 allow 翻不过默认地板", func(t *testing.T) {
		ctx, mockAsset, stubGrp := setupPolicyTest(t)
		stubGrp.groups[10] = &group_entity.Group{
			ID: 10, Name: "prod",
			CmdPolicy: `{"allow_list":["*"]}`,
		}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
			Return(&asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeOSS, GroupID: 10}, nil).AnyTimes()

		// 组通用 allow 只优先于类型策略的 NeedConfirm，不优先于它的 Deny——
		// 先取组结果再判类型策略的话，presign PUT 这条地板会被一条 `*` 组规则掀掉。
		got := CheckPermission(ctx, asset_entity.AssetTypeOSS, 1, "object presign mybucket/report.txt --method=put")
		assert.Equal(t, aictx.Deny, got.Decision)
		assert.Equal(t, aictx.SourcePolicyDeny, got.DecisionSource)
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
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object copy mybucket/a --to=other/b", GrantOriginSystem))
		assert.Equal(t, []string{"object.read mybucket/a", "object.write other/b", "object.delete mybucket/a"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object move mybucket/a --to=other/b", GrantOriginSystem))
	})

	t.Run("resource 以 / 结尾的单对象操作不落成 grant", func(t *testing.T) {
		// 目录标记是合法的单个对象，删得掉；但同一个串当规则读是"递归前缀"，
		// 落库就等于一条递归删除授权（D20）。
		assert.Empty(t, NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object delete mybucket/logs/", GrantOriginSystem))
	})

	// D20 的丢弃针对的是"命令指名一个对象、规则却读成一片前缀"的落差，而列举没有这个
	// 落差：`object list mybucket/` 的命令语义与 `object.list mybucket/` 的规则语义是
	// 同一个范围。丢掉它会让 cp 的递归展开（DirList 主体恒是前缀形态）永远拿不到常驻
	// 授权、每次重新弹框，也会让同一个字符串在 exec 面被丢、在 cp 面被留，直接违反
	// §6.2「两条入口的授权互相复用」。别照着"以 / 收尾"这个症状把它改回 Empty。
	t.Run("列举的前缀照落 grant", func(t *testing.T) {
		assert.Equal(t, []string{"object.list mybucket/"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object list mybucket/", GrantOriginSystem))
	})

	t.Run("只丢前缀形状的那一条，其余照落", func(t *testing.T) {
		assert.Equal(t, []string{"object.read mybucket/a", "object.delete mybucket/a"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object move mybucket/a --to=other/logs/", GrantOriginSystem))
	})

	t.Run("用户手写的策略形式 pattern 原样存下，含前缀形状", func(t *testing.T) {
		assert.Equal(t, []string{"object.read mybucket/logs/"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object.read mybucket/logs/", GrantOriginUser))
		assert.Equal(t, []string{"object.* *"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object.* *", GrantOriginUser))
	})

	t.Run("派生失败一条 grant 都不落", func(t *testing.T) {
		// 退回原串会在授权列表里显示一条用户其实没拿到的授权：它的 action 段没有点，
		// policy.MatchOSSRule 对任何策略串都失配。
		assert.Empty(t, NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object frobnicate mybucket/a", GrantOriginSystem))
		assert.Empty(t, NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object frobnicate mybucket/a", GrantOriginUser))
	})
}

// TestNormalizeGrantPatterns_OSSOriginTellsSubjectsFromUserPatterns 是本接缝的分辨力
// 本体：**同一个字符串**按来源落成两种不同的 grant。
//
// 传输适配器给出的 ApprovalSubject 与用户在审批弹窗里手写的 pattern 都是策略串形状
// （`object.write b/k`），字符串本身分不出来。曾经的判据是"形状"（isOSSPolicyString →
// 一律豁免），于是 D20 的前缀丢弃与 D21 的转义在整条 cp 路径上双双失效：
// `cp ./a.txt s3-prod:/mybucket` 的畸形目的地主体 `object.write mybucket/` 原样落库，
// 而它当规则读时是**整桶可写**（决策 D5：光桶名与以 "/" 收尾等价）。
func TestNormalizeGrantPatterns_OSSOriginTellsSubjectsFromUserPatterns(t *testing.T) {
	withOSSPolicyStrings(t)

	t.Run("适配器给出的畸形前缀主体不落成常驻授权", func(t *testing.T) {
		assert.Empty(t, NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object.write mybucket/", GrantOriginSystem))
	})

	t.Run("适配器给出的具体 key 主体照 D21 转义", func(t *testing.T) {
		assert.Equal(t, []string{`object.read mybucket/secrets\*`},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object.read mybucket/secrets*", GrantOriginSystem))
	})

	t.Run("用户手写的同一条串不被收窄", func(t *testing.T) {
		// 他写的通配就是他要的授权范围（§4.3）：转义掉就等于把一条"读遍 secrets 开头
		// 的对象"的授权悄悄改成"只读那个名字里真的带星号的对象"。
		assert.Equal(t, []string{"object.read mybucket/secrets*"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object.read mybucket/secrets*", GrantOriginUser))
		assert.Equal(t, []string{"object.write mybucket/"},
			NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object.write mybucket/", GrantOriginUser))
	})

	t.Run("DSL 形状的输入无论谁写的都要收窄", func(t *testing.T) {
		// 用户敲进去的是一条**命令**，而命令与规则对同一个字符串的读法不同——D20 的
		// 那条落差与谁敲的无关。
		assert.Empty(t, NormalizeGrantPatterns(asset_entity.AssetTypeOSS, "object delete mybucket/logs/", GrantOriginUser))
	})
}

// TestOSSGrantRule_RoundTripsThroughTheMatcher 是 D21 更正的往返本体：一次批准落下的
// grant，必须在**下一次同一个请求**上命中，且不多命中别的东西。
//
// 这正是 c0de1b2c 弄坏、本次修回的那件事。当时转义写在 PolicyStrings()/ApprovalSubject()
// 里，于是同一条串两个角色都被转义：path.Match(`b/secrets\*`, `b/secrets\*`) = false
// —— 含通配元字符的 key 上"始终允许"从此不生效，每次重新弹框，还多落一条什么都不授权
// 的死行。正确配对是**规则转义、名字原样**。
//
// 参与比对的是两个独立来源：本包的归一化，与 policy.MatchOSSRule 的规则语义。
func TestOSSGrantRule_RoundTripsThroughTheMatcher(t *testing.T) {
	// 只有把规则当通配读才会命中的"别的对象"。
	victims := []string{
		"object.read mybucket/secretsFOO", "object.read mybucket/secrets.env",
		"object.read mybucket/logs/a1.log", "object.read mybucket/whatX.txt",
		"object.read mybucket/dist/a1.js", "object.read otherbucket/logs/app.log",
		"object.read mybucketX/k", "object.list mybucket/logsX/",
		"object.write mybucket/secretsFOO", "object.delete mybucket/logs/a1.log",
	}
	for _, name := range []string{
		// 名字侧（PolicyStrings / ApprovalSubject 交出来的原始串）。
		"object.read mybucket/secrets*",
		"object.read mybucket/logs/a[1].log",
		"object.read mybucket/what?.txt",
		`object.write mybucket/a\b.txt`,
		"object.delete mybucket/logs/a[1].log",
		// 桶段里的元字符走同一条：`object get 'my*/k'` 派生的规则不转义就授权 mybucket/k。
		"object.read my*/k",
		// 桶段里孤立的 `\` 不转义就是 path.Match 的 ErrBadPattern，而 MatchOSSRule 把
		// error 一律当 false —— 规则于是谁也匹配不上（deny 侧就是 fail-open）。
		`object.read my\/k`,
		// 不含元字符的名字必须逐字节不变，否则既有授权会集体失效。
		"object.read mybucket/logs/app.log",
		"bucket.list *",
		"object.list mybucket/logs/",
	} {
		t.Run(name, func(t *testing.T) {
			rule, ok := ossGrantRule(name)
			if !ok {
				t.Fatalf("ossGrantRule(%q) 丢弃了它，下面的断言会变成空转", name)
			}
			if !policy.MatchOSSRule(rule, name) {
				t.Errorf("grant %q 匹配不上它自己派生自的那条请求 %q —— 这是一条死行，"+
					`"始终允许"永远不会生效`, rule, name)
			}
			for _, other := range victims {
				if other == name {
					continue
				}
				if policy.MatchOSSRule(rule, other) {
					t.Errorf("grant %q 顺带授权了 %q", rule, other)
				}
			}
		})
	}
}

// TestOSSGrantRule_NeverEscapesAPrefix 咬住 cd012457 修掉的那个缺陷，它的根因与 D21
// 更正不同（HasPrefix 字面比较 vs path.Match），所以转义搬到本包之后必须重新守一遍。
//
// 规则 key 以 "/" 收尾时，policy.matchOSSResource 走的是 strings.HasPrefix ——
// 字面比较，没有转义语法。给前缀加反斜杠只能把它转坏：cp 一次 `s3:/b/a\b/` 点
// "始终允许"，落库的就是一条一条真实 key 都盖不住的死 grant。
func TestOSSGrantRule_NeverEscapesAPrefix(t *testing.T) {
	for _, tt := range []struct{ name, covers string }{
		{`object.list mybucket/a\b/`, `object.list mybucket/a\b/sub/deep/`},
		{"object.list mybucket/logs[1]/", "object.list mybucket/logs[1]/sub/deep/"},
		{"object.list mybucket/logs/", "object.list mybucket/logs/sub/deep/"},
		{"object.list mybucket/", "object.list mybucket/anything/deep/"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rule, ok := ossGrantRule(tt.name)
			if !ok {
				t.Fatalf("ossGrantRule(%q) 丢弃了它", tt.name)
			}
			if rule != tt.name {
				t.Errorf("ossGrantRule(%q) = %q，前缀形态的 key 段一个字节都不该动", tt.name, rule)
			}
			if !policy.MatchOSSRule(rule, tt.covers) {
				t.Errorf("grant %q 盖不住它刚刚批准的那次枚举 %q", rule, tt.covers)
			}
		})
	}
}

// TestEscapeOSSMeta_EscapedNameMatchesItselfAndNothingElse 用 path.Match 这个独立来源
// 反查转义的**字符集**：转出来的片段当模式读，必须恰好匹配它自己派生自的那个名字。
//
// 挑的名字覆盖了几个只靠推理容易判错的角落：
//   - 只有 `]` 没有 `[`：`[` 一旦转义，字符类就永远开不了，`]` 因此是字面量、无需转义；
//   - 以孤立 `\` 收尾：不转义就是 path.Match 的 ErrBadPattern，一条报错的模式对任何
//     名字都是 false——deny 规则静默失配，正是 §3.4 最怕的那种 fail-open；
//   - 整条名字全是元字符，以及空串；
//   - 多字节 UTF-8：转义是按字节扫的，续字节一律 >= 0x80 而四个元字符都是 ASCII，
//     所以汉字里不会冒出反斜杠。
func TestEscapeGlobMeta_EscapedNameMatchesItselfAndNothingElse(t *testing.T) {
	names := []string{
		"", "]", "a]b", "][", `\`, `a\`, `*?[\`, "**", "secrets*", "what?.txt",
		"logs/a[1].log", `a\b.txt`, "日本語*.log", "秘密?.txt", "plain/key.txt",
	}
	// 只有把模式当通配读才会命中的"别的名字"。
	victims := []string{
		"secretsFOO", "whatX.txt", "logs/a1.log", "aXb.txt", "日本語FOO.log", "秘密X.txt",
		"a", "ab", "x", "[]", "plain/keyZtxt",
	}
	for _, name := range names {
		pattern := escapeGlobMeta(name)
		ok, err := path.Match(pattern, name)
		if err != nil {
			t.Errorf("escapeGlobMeta(%q) = %q is not a valid path.Match pattern: %v", name, pattern, err)
			continue
		}
		if !ok {
			t.Errorf("escapeGlobMeta(%q) = %q does not match the name it came from", name, pattern)
		}
		for _, other := range slices.Concat(names, victims) {
			if other == name {
				continue
			}
			if ok, err := path.Match(pattern, other); err == nil && ok {
				t.Errorf("escapeGlobMeta(%q) = %q also matches %q", name, pattern, other)
			}
		}
	}
}

// TestHandleConfirm_OSSAllowAllLeavesNoDeadGrantRow 锁住 checker.go 那条兜底的去向：
// 归一化把全部 pattern 都丢掉时（D20 的目录标记），不该退回 []string{原命令}。
//
// 那条串（`object delete mybucket/logs/`，一条 DSL）当规则读时 action 段没有点，
// policy.MatchOSSRule 对任何策略串都失配——落库只是在授权列表 UI 里显示一条用户其实
// 没拿到的授权，外加一条同样不真实的 grant_submit 审计行。
//
// 但"什么都不落"不能等于"什么都不说"：命令本身仍然放行执行，用户点的是"始终允许"，
// 得不到任何解释就会以为拿到了一条常驻授权，下次同样命令又弹一次框，audit_logs 里也找
// 不到原因（wrap-up finding 6）。因此这里不再断言零审计行，而是断言恰好落一条独立
// ToolName 的 grant_discarded 审计行——不是 grant_submit，避免被 AuditLogPage 的
// "会话已允许模式" 聚合误读成一条真实 pattern。
func TestHandleConfirm_OSSAllowAllLeavesNoDeadGrantRow(t *testing.T) {
	withOSSPolicyStrings(t)
	stubAudit := withStubAudit(t)
	ctx, mockAsset, _ := setupPolicyTest(t)

	stubGrant := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stubGrant)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})

	asset := &asset_entity.Asset{ID: 1, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	checker := NewCommandPolicyChecker(func(context.Context, string, []ApprovalItem) ApprovalResponse {
		return ApprovalResponse{Decision: "allowAll"}
	})
	got := checker.HandleConfirm(aictx.WithSessionID(ctx, "sess-dead"), 1,
		asset_entity.AssetTypeOSS, "object delete mybucket/logs/")

	assert.Equal(t, aictx.Allow, got.Decision, "这一次操作本身仍然被批准，只是不产生常驻授权")
	assert.Empty(t, stubGrant.items["sess-dead"],
		"落库的每一条都该是能匹配上东西的授权；这一条只会在授权列表里骗人")
	// grant_submit 审计行与 grant 行是同一件事的两半：没有落成常驻授权，就没有
	// "用户提交了这些 pattern" 这回事。混进一条空的 grant_submit，日后查
	// audit_logs 的人会以为用户拿到了授权，而 grants 表里一条都没有。
	for _, e := range stubAudit.entries {
		assert.NotEqual(t, "grant_submit", e.ToolName,
			"没有落成常驻授权时不该有 grant_submit 审计行")
	}
	// 但沉默同样是缺陷：换一条不同 ToolName 的审计行把"为什么"记下来。
	if assert.Len(t, stubAudit.entries, 1, "应当恰好落一条 grant_discarded 审计行解释这次丢弃") {
		discarded := stubAudit.entries[0]
		assert.Equal(t, "grant_discarded", discarded.ToolName)
		assert.Equal(t, int64(1), discarded.AssetID)
		assert.Equal(t, "s3-prod", discarded.AssetName)
		assert.Equal(t, "object delete mybucket/logs/", discarded.Command)
		assert.Equal(t, "sess-dead", discarded.SessionID)
	}
}

// 反过来，能落的照落——否则"什么都不落"是个廉价的通过方式。
func TestHandleConfirm_OSSAllowAllSavesTheDerivedPolicyStrings(t *testing.T) {
	withOSSPolicyStrings(t)
	stubAudit := withStubAudit(t)
	ctx, mockAsset, _ := setupPolicyTest(t)

	stubGrant := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stubGrant)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})

	asset := &asset_entity.Asset{ID: 1, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	checker := NewCommandPolicyChecker(func(context.Context, string, []ApprovalItem) ApprovalResponse {
		return ApprovalResponse{Decision: "allowAll"}
	})
	got := checker.HandleConfirm(aictx.WithSessionID(ctx, "sess-live"), 1,
		asset_entity.AssetTypeOSS, "object move mybucket/a --to=other/b")

	assert.Equal(t, aictx.Allow, got.Decision)
	saved := make([]string, 0, len(stubGrant.items["sess-live"]))
	for _, item := range stubGrant.items["sess-live"] {
		saved = append(saved, item.Command)
	}
	assert.Equal(t, []string{"object.read mybucket/a", "object.write other/b", "object.delete mybucket/a"}, saved)
	// 反过来：真落了 grant 就必须有 grant_submit 审计行，否则"永远不写"是个廉价的通过方式。
	assert.Len(t, stubAudit.entries, 1, "落了 grant 就该有一条 grant_submit 审计行")
}

// TestHandleConfirm_OSSTransferSubjectIsNotAUserPattern 咬住 HandleConfirm 里另一半
// 接线，也是 §4.3【实施期更正】记下的那个洞本身：cp 的 OSS 端点交上来的主体**已经是
// 策略串形状**（`object.write mybucket/`），与用户手写的 pattern 逐字节同形。
//
// 把它当用户 pattern 放行，一次"始终允许"换来的就是一条整桶可写的常驻授权
// （决策 D5：光桶名与以 "/" 收尾等价）；具体 key 的主体则会绕过 D21 的转义，
// `secrets*` 这一个对象换来读遍一批对象。
func TestHandleConfirm_OSSTransferSubjectIsNotAUserPattern(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)

	stubGrant := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stubGrant)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})

	asset := &asset_entity.Asset{ID: 1, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	checker := NewCommandPolicyChecker(func(context.Context, string, []ApprovalItem) ApprovalResponse {
		return ApprovalResponse{Decision: "allowAll"}
	})

	// `cp ./a.txt s3-prod:/mybucket` —— 目的地少了 key，主体是前缀形状。
	got := checker.HandleConfirm(aictx.WithSessionID(ctx, "sess-bucket"), 1,
		asset_entity.AssetTypeOSS, "object.write mybucket/")
	assert.Equal(t, aictx.Allow, got.Decision)
	assert.Empty(t, stubGrant.items["sess-bucket"], "一条整桶可写的常驻授权，绝不能由一次畸形目的地换来")

	// `cp s3-prod:/mybucket/secrets* ./` —— key 里的 `*` 是字面量，落库必须转义。
	got = checker.HandleConfirm(aictx.WithSessionID(ctx, "sess-star"), 1,
		asset_entity.AssetTypeOSS, "object.read mybucket/secrets*")
	assert.Equal(t, aictx.Allow, got.Decision)
	saved := make([]string, 0, len(stubGrant.items["sess-star"]))
	for _, item := range stubGrant.items["sess-star"] {
		saved = append(saved, item.Command)
	}
	assert.Equal(t, []string{`object.read mybucket/secrets\*`}, saved)
}

// TestHandleConfirm_UserEditedOSSPatternKeepsItsWildcards 咬住 HandleConfirm 里的来源
// 接线：EditedItems 是用户手写的，不能被当成系统交上来的主体去收窄。
func TestHandleConfirm_UserEditedOSSPatternKeepsItsWildcards(t *testing.T) {
	withOSSPolicyStrings(t)
	ctx, mockAsset, _ := setupPolicyTest(t)

	stubGrant := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stubGrant)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})

	asset := &asset_entity.Asset{ID: 1, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	checker := NewCommandPolicyChecker(func(context.Context, string, []ApprovalItem) ApprovalResponse {
		return ApprovalResponse{
			Decision:    "allowAll",
			EditedItems: []ApprovalItem{{Type: "oss", AssetID: 1, Command: "object.read mybucket/logs/*"}},
		}
	})
	got := checker.HandleConfirm(aictx.WithSessionID(ctx, "sess-edit"), 1,
		asset_entity.AssetTypeOSS, "object.read mybucket/logs/app.log")

	assert.Equal(t, aictx.Allow, got.Decision)
	saved := make([]string, 0, len(stubGrant.items["sess-edit"]))
	for _, item := range stubGrant.items["sess-edit"] {
		saved = append(saved, item.Command)
	}
	assert.Equal(t, []string{"object.read mybucket/logs/*"}, saved)
}

// TestHandleConfirm_UnusableUserEditDoesNotFallBackToTheSystemPattern 咬住"用户编辑过"
// 与"归一化交出空列表"这两件事必须分开。
//
// 兜底那条的本意是"用户根本没编辑，就用系统交上来的主体"。写成 `len(patterns) == 0`
// 之后，它同时接住了另一件完全不同的事：用户**编辑了**，但那条编辑归一化不出任何
// 授权。这时回落到系统主体等于**静默丢弃用户的编辑**，还反过来给了他一条他没要的
// （通常更宽的）授权——用户改这一栏，绝大多数时候是想收窄。
//
// 这条路在 OSS 上是活的：归一化交出空列表对 OSS 是个正常答案（决策 D20 的目录标记、
// 派生失败），不是失败。shell 类到不了这里——shellGrantPatterns 对任何非空白输入至少
// 给一条 pattern，而 ParseApprovalResponse 已经保证编辑项的 Command 非空白。
func TestHandleConfirm_UnusableUserEditDoesNotFallBackToTheSystemPattern(t *testing.T) {
	withOSSPolicyStrings(t)
	stubAudit := withStubAudit(t)
	ctx, mockAsset, _ := setupPolicyTest(t)

	stubGrant := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stubGrant)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})

	asset := &asset_entity.Asset{ID: 1, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	checker := NewCommandPolicyChecker(func(context.Context, string, []ApprovalItem) ApprovalResponse {
		return ApprovalResponse{
			Decision: "allowAll",
			// 用户想改，但改出来的东西派生不出策略串（打错了 verb）。
			EditedItems: []ApprovalItem{{Type: "oss", AssetID: 1, Command: "object frobnicate mybucket/a"}},
		}
	})
	// 系统主体是一条 move，会派生出三条策略串——正是绝不该悄悄落库的那三条。
	got := checker.HandleConfirm(aictx.WithSessionID(ctx, "sess-bad-edit"), 1,
		asset_entity.AssetTypeOSS, "object move mybucket/a --to=other/b")

	assert.Equal(t, aictx.Allow, got.Decision, "这一次操作仍然被批准，只是不产生常驻授权")
	saved := make([]string, 0, len(stubGrant.items["sess-bad-edit"]))
	for _, item := range stubGrant.items["sess-bad-edit"] {
		saved = append(saved, item.Command)
	}
	assert.Empty(t, saved,
		"用户的编辑用不了就什么都不授权；回落到系统主体等于丢掉他的编辑再塞给他一条更宽的授权")
	for _, e := range stubAudit.entries {
		assert.NotEqual(t, "grant_submit", e.ToolName,
			"没有落成常驻授权时不该有 grant_submit 审计行")
	}
	// 同 TestHandleConfirm_OSSAllowAllLeavesNoDeadGrantRow：什么都不落不等于什么都不说，
	// 一条 grant_discarded 审计行把"为什么"记下来。
	assert.Len(t, stubAudit.entries, 1, "应当恰好落一条 grant_discarded 审计行解释这次丢弃")
}
