package policy

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	. "github.com/smartystreets/goconvey/convey"
)

func TestPolicyDispatch(t *testing.T) {
	ctx := context.Background()
	Convey("TestPolicy 按 policyKind 分发", t, func() {
		Convey("command kind 应用 Current 策略(deny 命中)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindCommand,
				Current:    &asset_entity.CommandPolicy{DenyList: []string{"curl *"}},
			}, "curl http://example.com")
			So(out.Decision, ShouldEqual, aictx.Deny)
		})
		Convey("redis kind 应用 Current 策略(把 NeedConfirm 提升为 Allow)", func() {
			base := TestPolicy(ctx, PolicyTestInput{PolicyKind: PolicyKindRedis}, "SET k v")
			So(base.Decision, ShouldEqual, aictx.NeedConfirm)
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindRedis,
				Current:    &asset_entity.RedisPolicy{AllowList: []string{"SET *"}},
			}, "SET k v")
			So(out.Decision, ShouldEqual, aictx.Allow)
		})
		Convey("etcd kind 路由到 testEtcdPolicy(修复后非空策略可测)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindEtcd,
				Current:    &asset_entity.EtcdPolicy{Groups: []string{policy.BuiltinEtcdReadOnly}},
			}, "get /config")
			So(out.Decision, ShouldEqual, aictx.Allow)
		})
		Convey("query kind 路由到 testQueryPolicy(与直接调用等价)", func() {
			cur := &asset_entity.QueryPolicy{DenyTypes: []string{"insert"}}
			cmd := "INSERT INTO t VALUES (1)"
			So(
				TestPolicy(ctx, PolicyTestInput{PolicyKind: PolicyKindQuery, Current: cur}, cmd),
				ShouldResemble,
				testQueryPolicy(ctx, cur, nil, cmd),
			)
		})
		Convey("k8s kind 路由到 testK8sPolicy(与直接调用等价)", func() {
			cur := &asset_entity.K8sPolicy{DenyList: []string{"delete *"}}
			cmd := "kubectl delete pod x"
			So(
				TestPolicy(ctx, PolicyTestInput{PolicyKind: PolicyKindK8s, Current: cur}, cmd),
				ShouldResemble,
				testK8sPolicy(ctx, cur, nil, cmd),
			)
		})
		Convey("mongo kind 路由到 testMongoPolicy(deny 命中)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindMongo,
				Current:    &asset_entity.MongoPolicy{DenyTypes: []string{"dropDatabase"}},
			}, "dropDatabase")
			So(out.Decision, ShouldEqual, aictx.Deny)
		})
		Convey("mongo kind 应用 Current allow(find 放行)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindMongo,
				Current:    &asset_entity.MongoPolicy{AllowTypes: []string{"find"}},
			}, "find")
			So(out.Decision, ShouldEqual, aictx.Allow)
		})
		Convey("kafka kind 路由到 testKafkaPolicy(deny 命中)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindKafka,
				Current:    &asset_entity.KafkaPolicy{DenyList: []string{"topic.delete *"}},
			}, "topic.delete orders")
			So(out.Decision, ShouldEqual, aictx.Deny)
		})
		Convey("kafka kind 应用 Current allow(topic.read 放行)", func() {
			out := TestPolicy(ctx, PolicyTestInput{
				PolicyKind: PolicyKindKafka,
				Current:    &asset_entity.KafkaPolicy{AllowList: []string{"topic.read *"}},
			}, "topic.read orders")
			So(out.Decision, ShouldEqual, aictx.Allow)
		})
		Convey("未注册 kind(bogus)返回 NeedConfirm", func() {
			out := TestPolicy(ctx, PolicyTestInput{PolicyKind: "bogus"}, "anything")
			So(out.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}
