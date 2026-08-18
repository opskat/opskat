package permission

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"

	"go.uber.org/mock/gomock"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCheckFileTransferPermission(t *testing.T) {
	Convey("cp 类型的权限检查", t, func() {
		Convey("没有 grant 时需要确认", func() {
			ctx := withStubGrant(t)
			r := CheckPermission(ctx, GrantToolCp, 1, "/etc/cron.d/backup")
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("命中 cp grant 的路径 pattern 时放行", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "/opt/app/*")

			r := CheckPermission(ctx, GrantToolCp, 1, "/opt/app/deploy.sh")
			So(r.Decision, ShouldEqual, aictx.Allow)
			So(r.MatchedPattern, ShouldEqual, "/opt/app/*")
		})

		Convey("cp grant 的 * 不跨目录", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "/opt/app/*")

			r := CheckPermission(ctx, GrantToolCp, 1, "/opt/app/sub/deploy.sh")
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("路径为空时需要确认，不能被 * 整串放行", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "*")

			r := CheckPermission(ctx, GrantToolCp, 1, "")
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}

func TestCheckFileTransferPermission_CommandPolicy(t *testing.T) {
	Convey("SSH command policy can allow cp by direction without authorizing shell commands", t, func() {
		ctx, mockAsset, _ := setupPolicyTest(t)
		asset := &asset_entity.Asset{
			ID:   1,
			Type: asset_entity.AssetTypeSSH,
			CmdPolicy: mustJSON(asset_entity.CommandPolicy{AllowList: []string{
				"cp:read:/var/log/",
				"cp:write:/srv/releases/*",
			}}),
		}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

		So(CheckPermission(ctx, GrantToolCpRead, 1, "/var/log/nginx/access.log").Decision, ShouldEqual, aictx.Allow)
		So(CheckPermission(ctx, GrantToolCpWrite, 1, "/var/log/nginx/access.log").Decision, ShouldEqual, aictx.NeedConfirm)
		So(CheckPermission(ctx, GrantToolCpWrite, 1, "/srv/releases/app.tar").Decision, ShouldEqual, aictx.Allow)
		So(CheckPermission(ctx, GrantToolCpRead, 1, "/srv/releases/app.tar").Decision, ShouldEqual, aictx.NeedConfirm)
		So(CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, "cp:read:/var/log/").Decision, ShouldEqual, aictx.NeedConfirm)
	})

	Convey("cp:* allows both directions and composes with existing grants", t, func() {
		ctx, mockAsset, _ := setupPolicyTest(t)
		asset := &asset_entity.Asset{
			ID:        1,
			Type:      asset_entity.AssetTypeSSH,
			CmdPolicy: mustJSON(asset_entity.CommandPolicy{AllowList: []string{"cp:*"}}),
		}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

		So(CheckPermission(ctx, GrantToolCpRead, 1, "/etc/hosts").Decision, ShouldEqual, aictx.Allow)
		So(CheckPermission(ctx, GrantToolCpWrite, 1, "/opt/app/config.yml").Decision, ShouldEqual, aictx.Allow)
	})

	Convey("the built-in file transfer group composes through command policy groups", t, func() {
		ctx, mockAsset, _ := setupPolicyTest(t)
		asset := &asset_entity.Asset{
			ID:        1,
			Type:      asset_entity.AssetTypeSSH,
			CmdPolicy: mustJSON(asset_entity.CommandPolicy{Groups: []string{"builtin:cp-full-access"}}),
		}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

		result := CheckPermission(ctx, GrantToolCpRead, 1, "/etc/hosts")
		So(result.Decision, ShouldEqual, aictx.Allow)
		So(result.MatchedPattern, ShouldEqual, "cp:*")
	})
}

// TestNormalizeGrantPatterns_CpSubjectStaysAsNarrowAsTheTransfer 锁住 cp 主体落成常驻
// 授权那一步的收窄，与 OSS 的 ossGrantRule 是同一条理由（决策 D21）。
//
// cp 的主体是路径本身，而路径可以合法地含 glob 元字符：`cp ./x 'web-01:/etc/*'` 写的是
// 一个名字就叫 `*` 的文件（引号挡住了本地 shell，远端 shell 没参与），递归展开也会产出
// `a[1].log` 这种真实文件名。同一个字符串在两个角色上要的东西相反：作为**名字**（被
// policy.MatchPathRule 拿去撞规则）必须原样，作为**规则**（落成 grant）必须只覆盖它自己。
// 不收窄的后果是一次"始终允许"换来 /etc 下每个文件的授权，而且 cp 的 grant 不分方向——
// 一个写目的地能换来整个目录的读取。
func TestNormalizeGrantPatterns_CpSubjectStaysAsNarrowAsTheTransfer(t *testing.T) {
	saveSystemSubject := func(ctx context.Context, subject string) []string {
		patterns := NormalizeGrantPatterns(GrantToolCp, subject, GrantOriginSystem)
		for _, p := range patterns {
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, p)
		}
		return patterns
	}

	Convey("系统给出的 cp 主体落成的 grant 只覆盖它自己", t, func() {
		Convey("目的端的通配不再授权它命中的每个文件", func() {
			ctx := withStubGrant(t)
			So(len(saveSystemSubject(ctx, "/etc/*")), ShouldEqual, 1)

			So(CheckPermission(ctx, GrantToolCp, 1, "/etc/passwd").Decision, ShouldEqual, aictx.NeedConfirm)
			So(CheckPermission(ctx, GrantToolCp, 1, "/etc/shadow").Decision, ShouldEqual, aictx.NeedConfirm)
			// 往返：这条 grant 仍要授权它自己来自的那次传输，否则是一条死行，
			// "始终允许"点了等于没点（决策 D21 更正记下的那次事故）。
			So(CheckPermission(ctx, GrantToolCp, 1, "/etc/*").Decision, ShouldEqual, aictx.Allow)
		})

		Convey("展开授权的基点不再授权它下面的每个文件", func() {
			ctx := withStubGrant(t)
			saveSystemSubject(ctx, "/var/log/*")

			So(CheckPermission(ctx, GrantToolCp, 1, "/var/log/app.log").Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("名字里带元字符的真实文件仍然授权得到自己", func() {
			ctx := withStubGrant(t)
			saveSystemSubject(ctx, "/var/log/a[1].log")

			So(CheckPermission(ctx, GrantToolCp, 1, "/var/log/a[1].log").Decision, ShouldEqual, aictx.Allow)
			So(CheckPermission(ctx, GrantToolCp, 1, "/var/log/a1.log").Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("名字里带反斜杠的真实文件不再落成死 grant", func() {
			ctx := withStubGrant(t)
			saveSystemSubject(ctx, `/var/log/a\b.log`)

			So(CheckPermission(ctx, GrantToolCp, 1, `/var/log/a\b.log`).Decision, ShouldEqual, aictx.Allow)
		})

		Convey("用户在弹窗里手写的 pattern 原样落库", func() {
			ctx := withStubGrant(t)
			for _, p := range NormalizeGrantPatterns(GrantToolCp, "/opt/app/*", GrantOriginUser) {
				SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, p)
			}

			r := CheckPermission(ctx, GrantToolCp, 1, "/opt/app/deploy.sh")
			So(r.Decision, ShouldEqual, aictx.Allow)
			So(r.MatchedPattern, ShouldEqual, "/opt/app/*")
		})
	})
}

// 方向化 cp 的 deny 规则必须真正拦住传输（deny 无条件先判，checkCommandPolicyPermission
// 的同一优先序）：opsctl policy deny <asset> -- 'cp:read:/x' 落库的就是这种规则，
// rule_persist 的遮蔽检测（cpDenyShadows）也按它生效假设。若检查器只扫 allow，
// 这条 deny 就是一条给人虚假安全感的死规则——写侧与查侧必须同一语义。
func TestCheckFileTransferPermission_DirectionalDenyEnforced(t *testing.T) {
	Convey("方向化 cp deny 规则先判", t, func() {
		ctx, mockAsset, _ := setupPolicyTest(t)
		asset := &asset_entity.Asset{
			ID:   1,
			Type: asset_entity.AssetTypeSSH,
			CmdPolicy: mustJSON(asset_entity.CommandPolicy{
				AllowList: []string{"cp:read:/etc/*"},
				DenyList:  []string{"cp:read:/etc/shadow"},
			}),
		}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

		r := CheckPermission(ctx, GrantToolCpRead, 1, "/etc/shadow")
		So(r.Decision, ShouldEqual, aictx.Deny)
		So(r.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		So(r.MatchedPattern, ShouldEqual, "cp:read:/etc/shadow")

		// 未命中 deny 的路径照常由 allow 放行。
		So(CheckPermission(ctx, GrantToolCpRead, 1, "/etc/hosts").Decision, ShouldEqual, aictx.Allow)
		// deny 不跨方向：读的 deny 不拦写。
		So(CheckPermission(ctx, GrantToolCpWrite, 1, "/etc/shadow").Decision, ShouldEqual, aictx.NeedConfirm)
	})

	Convey("cp:* deny 拦下所有方向的传输", t, func() {
		ctx, mockAsset, _ := setupPolicyTest(t)
		asset := &asset_entity.Asset{
			ID:        1,
			Type:      asset_entity.AssetTypeSSH,
			CmdPolicy: mustJSON(asset_entity.CommandPolicy{DenyList: []string{"cp:*"}}),
		}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

		So(CheckPermission(ctx, GrantToolCpRead, 1, "/etc/hosts").Decision, ShouldEqual, aictx.Deny)
		So(CheckPermission(ctx, GrantToolCpWrite, 1, "/opt/app/config.yml").Decision, ShouldEqual, aictx.Deny)
	})
}
