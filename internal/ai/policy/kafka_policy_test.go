package policy

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policy_entity "github.com/opskat/opskat/internal/model/entity/policy"
)

// TestKafkaMessageWriteDeny_IsOptInNotDefault 锁住 message.write 的两条性质。
//
// message.write 是独立的可选拒绝组，不属于未配置资产采用的默认危险操作组。
// 「默认不拒绝」与「显式选择后拒绝」共同保护这个用户可配置契约。
func TestKafkaMessageWriteDeny_IsOptInNotDefault(t *testing.T) {
	ctx := context.Background()

	t.Run("默认策略不拒绝 produce", func(t *testing.T) {
		res := CheckKafkaPolicy(ctx, nil, "message.write orders")
		if res.Decision == aictx.Deny {
			t.Fatalf("默认策略把 message.write 判成 Deny（命中 %q）——该规则只应在勾选可选组后生效；"+
				"该规则只应由可选策略组控制", res.MatchedPattern)
		}
	})

	t.Run("勾选可选组后拒绝", func(t *testing.T) {
		pol := &asset_entity.KafkaPolicy{Groups: []string{policy_entity.BuiltinKafkaMessageWriteDeny}}
		res := CheckKafkaPolicy(ctx, pol, "message.write orders")
		if res.Decision != aictx.Deny {
			t.Fatalf("勾选 %s 后 message.write 应被拒绝，实际 decision=%v",
				policy_entity.BuiltinKafkaMessageWriteDeny, res.Decision)
		}
		// 只断言 Deny 区分不出「被我指的规则拒绝」与「被别的规则拒绝」——
		// 往清单里插一条 "*" 就能让前者悄悄变成后者。
		if res.MatchedPattern != "message.write *" {
			t.Fatalf("命中的规则 = %q, want %q", res.MatchedPattern, "message.write *")
		}
	})

	t.Run("可选组不波及读取", func(t *testing.T) {
		pol := &asset_entity.KafkaPolicy{Groups: []string{policy_entity.BuiltinKafkaMessageWriteDeny}}
		if res := CheckKafkaPolicy(ctx, pol, "message.read orders"); res.Decision == aictx.Deny {
			t.Fatalf("可选组只应拒绝写入，message.read 却被 %q 拒绝", res.MatchedPattern)
		}
	})
}
