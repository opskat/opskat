package policy

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	. "github.com/smartystreets/goconvey/convey"
)

func TestPolicyKindRegistry(t *testing.T) {
	Convey("policyKind 注册表", t, func() {
		Convey("内置 7 个 kind 已注册", func() {
			for _, k := range []string{
				PolicyKindCommand, PolicyKindQuery, PolicyKindRedis,
				PolicyKindMongo, PolicyKindKafka, PolicyKindK8s, PolicyKindEtcd,
			} {
				_, ok := kindRegistry[k]
				So(ok, ShouldBeTrue)
			}
		})
		Convey("未知 kind 未注册", func() {
			_, ok := kindRegistry["bogus"]
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
		Convey("mongo → *MongoPolicy", func() {
			v, err := DecodeCurrentPolicy(PolicyKindMongo, []byte(`{"allow_types":["find"]}`))
			So(err, ShouldBeNil)
			mp, ok := v.(*asset_entity.MongoPolicy)
			So(ok, ShouldBeTrue)
			So(mp.AllowTypes, ShouldResemble, []string{"find"})
		})
		Convey("未注册 kind 报错", func() {
			_, err := DecodeCurrentPolicy("bogus", []byte(`{}`))
			So(err, ShouldNotBeNil)
		})
	})
}

func TestResolvePolicyKind(t *testing.T) {
	// resolver 单测:自行 seed 资产类型→kind(真实 handler 接线由 assettype 包测试覆盖)。
	seed := map[string]string{
		"ssh": PolicyKindCommand, "serial": PolicyKindCommand, "local": PolicyKindCommand,
		"database": PolicyKindQuery, "redis": PolicyKindRedis, "mongodb": PolicyKindMongo,
		"kafka": PolicyKindKafka, "k8s": PolicyKindK8s, "etcd": PolicyKindEtcd,
	}
	for typ, kind := range seed {
		policyent.RegisterAssetKind(typ, kind)
	}
	defer func() {
		for typ := range seed {
			policyent.UnregisterAssetKind(typ)
		}
	}()

	Convey("ResolvePolicyKind", t, func() {
		Convey("已注册资产类型 → kind", func() {
			for in, want := range seed {
				k, ok := ResolvePolicyKind(in)
				So(ok, ShouldBeTrue)
				So(k, ShouldEqual, want)
			}
		})
		Convey("前端别名 mongo(=kind)经 kind 兜底解析", func() {
			k, ok := ResolvePolicyKind("mongo")
			So(ok, ShouldBeTrue)
			So(k, ShouldEqual, PolicyKindMongo)
		})
		Convey("kubernetes 别名 → k8s", func() {
			k, ok := ResolvePolicyKind("kubernetes")
			So(ok, ShouldBeTrue)
			So(k, ShouldEqual, PolicyKindK8s)
		})
		Convey("直接传已注册 kind 原样返回", func() {
			k, ok := ResolvePolicyKind(PolicyKindCommand)
			So(ok, ShouldBeTrue)
			So(k, ShouldEqual, PolicyKindCommand)
		})
		Convey("未知类型 → false", func() {
			_, ok := ResolvePolicyKind("nope")
			So(ok, ShouldBeFalse)
		})
	})
}
