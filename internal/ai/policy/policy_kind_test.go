package policy

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestPolicyKindRegistry(t *testing.T) {
	Convey("policyKind 注册表", t, func() {
		Convey("内置 5 个 kind 已注册", func() {
			for _, k := range []string{PolicyKindCommand, PolicyKindQuery, PolicyKindRedis, PolicyKindK8s, PolicyKindEtcd} {
				_, ok := kindRegistry[k]
				So(ok, ShouldBeTrue)
			}
		})
		Convey("mongo/kafka 暂未注册(留待阶段 1b)", func() {
			_, ok := kindRegistry[PolicyKindMongo]
			So(ok, ShouldBeFalse)
			_, ok = kindRegistry[PolicyKindKafka]
			So(ok, ShouldBeFalse)
		})
	})
}

func TestDecodeCurrentPolicy(t *testing.T) {
	Convey("DecodeCurrentPolicy", t, func() {
		Convey("command → *CommandPolicy", func() {
			v, err := DecodeCurrentPolicy(PolicyKindCommand, []byte(`{"allow_list":["ls *"]}`))
			So(err, ShouldBeNil)
			cp, ok := v.(*asset_entity.CommandPolicy)
			So(ok, ShouldBeTrue)
			So(cp.AllowList, ShouldResemble, []string{"ls *"})
		})
		Convey("未注册 kind 报错", func() {
			_, err := DecodeCurrentPolicy(PolicyKindMongo, []byte(`{}`))
			So(err, ShouldNotBeNil)
		})
	})
}

func TestResolvePolicyKind(t *testing.T) {
	Convey("ResolvePolicyKind", t, func() {
		Convey("资产类型/前端 policyType → kind", func() {
			cases := map[string]string{
				"ssh":        PolicyKindCommand,
				"serial":     PolicyKindCommand,
				"local":      PolicyKindCommand,
				"database":   PolicyKindQuery,
				"redis":      PolicyKindRedis,
				"k8s":        PolicyKindK8s,
				"kubernetes": PolicyKindK8s,
				"etcd":       PolicyKindEtcd,
			}
			for in, want := range cases {
				k, ok := ResolvePolicyKind(in)
				So(ok, ShouldBeTrue)
				So(k, ShouldEqual, want)
			}
		})
		Convey("直接传已注册 kind 原样返回", func() {
			k, ok := ResolvePolicyKind(PolicyKindCommand)
			So(ok, ShouldBeTrue)
			So(k, ShouldEqual, PolicyKindCommand)
		})
		Convey("mongo/kafka 未注册 → false(保持 unsupported 行为)", func() {
			_, ok := ResolvePolicyKind("mongo")
			So(ok, ShouldBeFalse)
			_, ok = ResolvePolicyKind("kafka")
			So(ok, ShouldBeFalse)
		})
		Convey("未知类型 → false", func() {
			_, ok := ResolvePolicyKind("nope")
			So(ok, ShouldBeFalse)
		})
	})
}
