package opsctl

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestGrantPatternAndOrigin 咬住 opsctl 这条 grant 落库路径上的**来源**接线。
//
// 它是设计 §4.3 那个洞在 CLI 侧的出口：cp / exec 交上来的 req.Command 是系统推导出来的
// 主体，必须按 GrantOriginSystem 收窄（D20 前缀丢弃、D21 转义）；用户在弹窗里改写过的
// EditedItems 才是他自己要的授权范围，原样落库。
//
// 第二段断言把这个选择接到它真正的后果上：同一条主体，两种来源落下**两条不同的**
// 常驻授权。只断言枚举值相等的话，改一个常量就能改掉安全语义而测试照绿。
func TestGrantPatternAndOrigin(t *testing.T) {
	const subject = "object.read mybucket/secrets*"

	t.Run("没有编辑项：系统交上来的主体", func(t *testing.T) {
		pattern, origin := grantPatternAndOrigin(subject, nil)
		require.Equal(t, subject, pattern)
		require.Equal(t, permission.GrantOriginSystem, origin)

		// key 里的 `*` 是字面量，落库必须转义成只覆盖这一个对象（决策 D21）。
		require.Equal(t,
			[]string{`object.read mybucket/secrets\*`},
			permission.NormalizeGrantPatterns(asset_entity.AssetTypeOSS, pattern, origin))
	})

	t.Run("有编辑项：用户手写的授权范围", func(t *testing.T) {
		edited := []permission.ApprovalItem{
			{Type: "oss", AssetID: 1, Command: "object.read mybucket/logs/*"},
		}
		pattern, origin := grantPatternAndOrigin(subject, edited)
		require.Equal(t, "object.read mybucket/logs/*", pattern,
			"落库的必须是用户改写后的那条，不是系统原来的主体")
		require.Equal(t, permission.GrantOriginUser, origin)

		// 他写的通配就是他要的范围，一个字节都不该动。
		require.Equal(t,
			[]string{"object.read mybucket/logs/*"},
			permission.NormalizeGrantPatterns(asset_entity.AssetTypeOSS, pattern, origin))
	})
}
