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
// 背景：内置 deny 清单是一道**不可覆盖的地板**——EffectiveKafkaPolicy 无条件追加默认
// DenyList（policy_effective.go:176-177），而 checkKafkaPolicyRules 先查 deny、命中即返回。
// 实测显式 AllowList:["topic.delete *"] 仍被判 Deny 并命中 "topic.delete *"。
// 所以把 message.write 放进 BuiltinKafkaDangerousDeny 等于让 AI 永久丧失 produce 能力，
// 且 BuiltinKafkaOperator 里的 "message.write *" 会变成永远生效不了的死条目。
// 定为可选组之后，「默认不拒绝」本身就成了要钉住的契约——否则哪天有人把它挪进
// 默认清单，只会表现为 produce 静默失效。
func TestKafkaMessageWriteDeny_IsOptInNotDefault(t *testing.T) {
	ctx := context.Background()

	t.Run("默认策略不拒绝 produce", func(t *testing.T) {
		res := CheckKafkaPolicy(ctx, nil, "message.write orders")
		if res.Decision == aictx.Deny {
			t.Fatalf("默认策略把 message.write 判成 Deny（命中 %q）——该规则只应在勾选可选组后生效；"+
				"进了默认地板就没有任何配置能再放开 produce", res.MatchedPattern)
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
