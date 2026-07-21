package permission

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/repository/grant_repo"

	. "github.com/smartystreets/goconvey/convey"
)

// withStubGrant 注册 stubGrantRepo 并返回带 sessionID 的 ctx。
// grant 匹配依赖 aictx.GetSessionID —— 不注入 sessionID 会直接返回空串，测试会假绿。
func withStubGrant(t *testing.T) context.Context {
	stub := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stub)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})
	return aictx.WithSessionID(context.Background(), "sess-cp")
}

func TestGrantIsolation(t *testing.T) {
	Convey("grant 池按工具面隔离", t, func() {
		// 用户在 cp 审批里点"本次会话允许"并把 pattern 编辑成 `*`（很常见）时，
		// 这条路径授权一旦被命令面看见，MatchCommandRule 的 `*` 会放行任意命令。
		Convey("cp 的路径授权不能放行命令", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "*")

			got := matchGrantPatternsWith(ctx, 1, nil, []string{"rm -rf /var/log"}, "exec", policy.MatchCommandRule)
			So(got, ShouldEqual, "")
		})

		Convey("命令授权不能放行文件传输", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", "exec", "*")

			got := matchGrantPatternsWith(ctx, 1, nil, []string{"/etc/cron.d/backup"}, GrantToolCp, policy.MatchPathRule)
			So(got, ShouldEqual, "")
		})

		Convey("存量 tool_name=exec 的行仍然为非 cp 检查生效", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", "exec", "redis-cli get *")

			got := matchGrantPatternsWith(ctx, 1, nil, []string{"redis-cli get foo"}, "redis", policy.MatchCommandRule)
			So(got, ShouldEqual, "redis-cli get *")
		})
	})
}
