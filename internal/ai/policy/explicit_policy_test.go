package policy

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestExplicitPolicyDoesNotInheritUnselectedDefaultDenyGroups(t *testing.T) {
	Convey("an explicit policy controls which built-in deny groups apply", t, func() {
		ctx := context.Background()

		Convey("Redis", func() {
			result := CheckRedisPolicy(ctx, &asset_entity.RedisPolicy{AllowList: []string{"GET *"}}, "FLUSHALL")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("etcd", func() {
			result := CheckEtcdPolicy(ctx, &asset_entity.EtcdPolicy{AllowList: []string{"get *"}}, "auth disable")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("Kafka", func() {
			result := CheckKafkaPolicy(ctx, &asset_entity.KafkaPolicy{AllowList: []string{"topic.read *"}}, "topic.delete orders")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("OSS", func() {
			result := CheckOSSPolicy(ctx, &asset_entity.OSSPolicy{AllowList: []string{"object.read *"}}, []string{"object.presign.write bucket/key"})
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}
