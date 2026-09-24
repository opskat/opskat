package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/smartystreets/goconvey/convey"
)

func TestRedisHandler(t *testing.T) {
	convey.Convey("Redis Handler", t, func() {
		h := &redisHandler{}
		convey.Convey("SafeView", func() {
			a := &asset_entity.Asset{Type: "redis", Status: 1}
			_ = a.SetRedisConfig(&asset_entity.RedisConfig{
				Host: "10.0.0.1", Port: 6379, Username: "default",
				Password: "secret", Database: 3,
			})
			view := h.SafeView(a)
			convey.So(view["host"], convey.ShouldEqual, "10.0.0.1")
			convey.So(view["port"], convey.ShouldEqual, 6379)
			convey.So(view["username"], convey.ShouldEqual, "default")
			convey.So(view["redis_db"], convey.ShouldEqual, 3)
			_, hasPassword := view["password"]
			convey.So(hasPassword, convey.ShouldBeFalse)
		})
		convey.Convey("ApplyCreateArgs", func() {
			a := &asset_entity.Asset{Type: "redis"}
			err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
				"host": "10.0.0.1", "port": float64(6379),
				"username": "default", "ssh_asset_id": float64(7),
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetRedisConfig()
			convey.So(cfg.Host, convey.ShouldEqual, "10.0.0.1")
			convey.So(cfg.Port, convey.ShouldEqual, 6379)
			convey.So(cfg.Username, convey.ShouldEqual, "default")
			convey.So(cfg.SSHAssetID, convey.ShouldEqual, 7)
		})
		convey.Convey("ApplyUpdateArgs", func() {
			a := &asset_entity.Asset{Type: "redis"}
			_ = a.SetRedisConfig(&asset_entity.RedisConfig{
				Host: "10.0.0.1", Port: 6379, Username: "default",
			})
			err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{"host": "10.0.0.2"})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetRedisConfig()
			convey.So(cfg.Host, convey.ShouldEqual, "10.0.0.2")
			convey.So(cfg.Port, convey.ShouldEqual, 6379)
			convey.So(cfg.Username, convey.ShouldEqual, "default")
		})

		convey.Convey("ApplyCreateArgs 集群模式写入 mode/nodes/node_address_map", func() {
			a := &asset_entity.Asset{Type: "redis"}
			err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
				"mode":  "cluster",
				"nodes": []any{"10.0.0.1:6379", "10.0.0.2:6379"},
				"node_address_map": map[string]any{
					"10.0.0.1:6379": "127.0.0.1:16379",
				},
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetRedisConfig()
			convey.So(cfg.Mode, convey.ShouldEqual, "cluster")
			convey.So(cfg.Nodes, convey.ShouldResemble, []string{"10.0.0.1:6379", "10.0.0.2:6379"})
			convey.So(cfg.NodeAddressMap, convey.ShouldResemble, map[string]string{"10.0.0.1:6379": "127.0.0.1:16379"})
		})

		convey.Convey("ApplyCreateArgs 只写入当前模式用到的字段", func() {
			a := &asset_entity.Asset{Type: "redis"}
			err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
				"mode":        "cluster",
				"nodes":       []any{"10.0.0.1:6379"},
				"host":        "10.0.0.9",
				"port":        6379,
				"master_name": "stray",
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetRedisConfig()
			convey.So(cfg.Host, convey.ShouldBeEmpty)
			convey.So(cfg.Port, convey.ShouldEqual, 0)
			convey.So(cfg.MasterName, convey.ShouldBeEmpty)
			convey.So(cfg.Nodes, convey.ShouldResemble, []string{"10.0.0.1:6379"})
			// 存储的 config 不应带 host/port 空键(E33:曾写入 "host":"","port":0)。
			convey.So(a.Config, convey.ShouldNotContainSubstring, `"host"`)
			convey.So(a.Config, convey.ShouldNotContainSubstring, `"port"`)
		})

		convey.Convey("ApplyUpdateArgs 切回单机时清掉集群字段", func() {
			a := &asset_entity.Asset{Type: "redis"}
			_ = a.SetRedisConfig(&asset_entity.RedisConfig{
				Mode: "cluster", Nodes: []string{"10.0.0.1:6379"},
				NodeAddressMap: map[string]string{"172.18.0.2:6379": "10.0.0.1:16379"},
			})
			err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{
				"mode": "standalone", "host": "10.0.0.5", "port": 6380,
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetRedisConfig()
			convey.So(cfg.EffectiveMode(), convey.ShouldEqual, "standalone")
			convey.So(cfg.Host, convey.ShouldEqual, "10.0.0.5")
			convey.So(cfg.Nodes, convey.ShouldBeEmpty)
			convey.So(cfg.NodeAddressMap, convey.ShouldBeEmpty)
			convey.So(h.SafeView(a)["nodes"], convey.ShouldBeNil)
		})

		convey.Convey("ApplyUpdateArgs 在集群/哨兵之间切换且未给 nodes 时不沿用旧模式的节点", func() {
			a := &asset_entity.Asset{Name: "cache", Type: "redis", Status: 1}
			convey.So(a.SetRedisConfig(&asset_entity.RedisConfig{
				Mode: asset_entity.RedisModeSentinel, Nodes: []string{"10.0.0.1:26379"}, MasterName: "mymaster",
			}), convey.ShouldBeNil)
			err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{"mode": "cluster"})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetRedisConfig()
			convey.So(cfg.Nodes, convey.ShouldBeEmpty)
			convey.So(a.Validate(), convey.ShouldNotBeNil)
		})

		convey.Convey("ApplyCreateArgs 哨兵模式加密 sentinel_password", func() {
			a := &asset_entity.Asset{Type: "redis"}
			err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
				"mode":              "sentinel",
				"nodes":             []any{"10.0.0.1:26379"},
				"master_name":       "mymaster",
				"sentinel_username": "sentuser",
				"sentinel_password": "sent-secret",
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetRedisConfig()
			convey.So(cfg.MasterName, convey.ShouldEqual, "mymaster")
			convey.So(cfg.SentinelUsername, convey.ShouldEqual, "sentuser")
			convey.So(cfg.SentinelPassword, convey.ShouldNotEqual, "sent-secret")
			decrypted, decErr := credential_svc.Default().Decrypt(cfg.SentinelPassword)
			convey.So(decErr, convey.ShouldBeNil)
			convey.So(decrypted, convey.ShouldEqual, "sent-secret")
		})

		convey.Convey("ApplyUpdateArgs 只改提供的字段，sentinel_password 缺省保留已存储值", func() {
			a := &asset_entity.Asset{Type: "redis"}
			encrypted, _ := credential_svc.Default().Encrypt("old-sentinel-secret")
			_ = a.SetRedisConfig(&asset_entity.RedisConfig{
				Mode: "sentinel", Nodes: []string{"10.0.0.1:26379"}, MasterName: "mymaster",
				SentinelUsername: "sentuser", SentinelPassword: encrypted,
			})
			err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{
				"nodes": []any{"10.0.0.1:26379", "10.0.0.2:26379"},
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetRedisConfig()
			convey.So(cfg.Nodes, convey.ShouldResemble, []string{"10.0.0.1:26379", "10.0.0.2:26379"})
			convey.So(cfg.MasterName, convey.ShouldEqual, "mymaster")
			convey.So(cfg.SentinelUsername, convey.ShouldEqual, "sentuser")
			convey.So(cfg.SentinelPassword, convey.ShouldEqual, encrypted)
		})

		convey.Convey("ApplyUpdateArgs 提供 sentinel_password 时重新加密", func() {
			a := &asset_entity.Asset{Type: "redis"}
			_ = a.SetRedisConfig(&asset_entity.RedisConfig{
				Mode: "sentinel", Nodes: []string{"10.0.0.1:26379"}, MasterName: "mymaster",
			})
			err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{
				"sentinel_password": "new-sentinel-secret",
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetRedisConfig()
			decrypted, decErr := credential_svc.Default().Decrypt(cfg.SentinelPassword)
			convey.So(decErr, convey.ShouldBeNil)
			convey.So(decrypted, convey.ShouldEqual, "new-sentinel-secret")
		})

		convey.Convey("SafeView 集群模式显示 mode/nodes 不含密钥", func() {
			a := &asset_entity.Asset{Type: "redis", Status: 1}
			_ = a.SetRedisConfig(&asset_entity.RedisConfig{
				Mode: "cluster", Nodes: []string{"10.0.0.1:6379"}, Username: "default",
			})
			view := h.SafeView(a)
			convey.So(view["mode"], convey.ShouldEqual, "cluster")
			convey.So(view["nodes"], convey.ShouldResemble, []string{"10.0.0.1:6379"})
			_, hasSentinelPassword := view["sentinel_password"]
			convey.So(hasSentinelPassword, convey.ShouldBeFalse)
		})

		convey.Convey("SafeView 哨兵模式显示 master_name 不含密钥", func() {
			a := &asset_entity.Asset{Type: "redis", Status: 1}
			_ = a.SetRedisConfig(&asset_entity.RedisConfig{
				Mode: "sentinel", Nodes: []string{"10.0.0.1:26379"}, MasterName: "mymaster",
				SentinelUsername: "sentuser", SentinelPassword: "cipher",
			})
			view := h.SafeView(a)
			convey.So(view["mode"], convey.ShouldEqual, "sentinel")
			convey.So(view["master_name"], convey.ShouldEqual, "mymaster")
			_, hasSentinelPassword := view["sentinel_password"]
			convey.So(hasSentinelPassword, convey.ShouldBeFalse)
		})

		convey.Convey("SafeView 单机模式行为不变", func() {
			a := &asset_entity.Asset{Type: "redis", Status: 1}
			_ = a.SetRedisConfig(&asset_entity.RedisConfig{
				Host: "10.0.0.1", Port: 6379, Username: "default",
			})
			view := h.SafeView(a)
			_, hasMode := view["mode"]
			convey.So(hasMode, convey.ShouldBeFalse)
			_, hasNodes := view["nodes"]
			convey.So(hasNodes, convey.ShouldBeFalse)
		})

		convey.Convey("ValidateCreateArgs 集群模式不要求 host/username 但要求 nodes", func() {
			convey.So(h.ValidateCreateArgs(map[string]any{
				"mode": "cluster", "nodes": []any{"10.0.0.1:6379"},
			}), convey.ShouldBeNil)
			convey.So(h.ValidateCreateArgs(map[string]any{"mode": "cluster"}), convey.ShouldNotBeNil)
		})

		convey.Convey("ValidateCreateArgs 单机模式仍要求 host/port/username", func() {
			convey.So(h.ValidateCreateArgs(map[string]any{
				"host": "10.0.0.1", "port": float64(6379), "username": "default",
			}), convey.ShouldBeNil)
			convey.So(h.ValidateCreateArgs(map[string]any{"username": "default"}), convey.ShouldNotBeNil)
		})

		convey.Convey("ValidateCreateArgs 在审批前拒绝模式相关错误并指出具体字段(E27)", func() {
			convey.Convey("哨兵模式缺 master_name", func() {
				err := h.ValidateCreateArgs(map[string]any{
					"mode": "sentinel", "nodes": []any{"10.0.0.1:26379"},
				})
				convey.So(err, convey.ShouldNotBeNil)
				convey.So(err.Error(), convey.ShouldContainSubstring, "master_name")
			})
			convey.Convey("node_address_map 映射行非法", func() {
				err := h.ValidateCreateArgs(map[string]any{
					"mode": "cluster", "nodes": []any{"10.0.0.1:6379"},
					"node_address_map": map[string]any{"10.0.0.1:6379": "bad-no-port"},
				})
				convey.So(err, convey.ShouldNotBeNil)
				convey.So(err.Error(), convey.ShouldContainSubstring, "node_address_map")
			})
			convey.Convey("节点缺端口", func() {
				err := h.ValidateCreateArgs(map[string]any{
					"mode": "cluster", "nodes": []any{"nohostport"},
				})
				convey.So(err, convey.ShouldNotBeNil)
				convey.So(err.Error(), convey.ShouldContainSubstring, "nodes")
			})
			convey.Convey("未知 mode", func() {
				err := h.ValidateCreateArgs(map[string]any{"mode": "weird", "host": "x"})
				convey.So(err, convey.ShouldNotBeNil)
				convey.So(err.Error(), convey.ShouldContainSubstring, "mode")
			})
		})

		convey.Convey("集群模式的完整创建通过 validateRedis 校验（走自动化契约）", func() {
			prepared, err := PrepareCreate(asset_entity.AssetTypeRedis, map[string]any{
				"mode": "cluster", "nodes": []any{"10.0.0.1:6379"}, "username": "default",
			})
			convey.So(err, convey.ShouldBeNil)
			a := &asset_entity.Asset{Type: "redis", Status: 1, Name: "cluster-asset"}
			convey.So(prepared.Handler.ApplyCreateArgs(context.Background(), a, prepared.Config), convey.ShouldBeNil)
			convey.So(a.Validate(), convey.ShouldBeNil)
		})

		convey.Convey("集群模式缺 nodes 时 validateRedis 报错并在自动化契约里可见", func() {
			_, err := PrepareCreate(asset_entity.AssetTypeRedis, map[string]any{
				"mode": "cluster", "username": "default",
			})
			convey.So(err, convey.ShouldNotBeNil)
			convey.So(err.Error(), convey.ShouldContainSubstring, "nodes")
		})
	})
}
