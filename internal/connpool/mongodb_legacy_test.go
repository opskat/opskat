package connpool

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	mongov1opts "go.mongodb.org/mongo-driver/mongo/options"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"
)

func TestConfigureMongoTransportV1(t *testing.T) {
	Convey("configureMongoTransportV1", t, func() {
		Convey("直连不设置 dialer", func() {
			cfg := &asset_entity.MongoDBConfig{Host: "h", Port: 27017}
			clientOpts := mongov1opts.Client().ApplyURI(buildMongoURI(cfg, ""))
			tunnel, err := configureMongoTransportV1(clientOpts, &asset_entity.Asset{}, cfg, nil)
			So(err, ShouldBeNil)
			So(tunnel, ShouldBeNil)
			So(clientOpts.Dialer, ShouldBeNil)
		})

		Convey("代理设置 dialer 且不强制直连", func() {
			cfg := &asset_entity.MongoDBConfig{
				Host: "h", Port: 27017,
				Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
			}
			clientOpts := mongov1opts.Client().ApplyURI(buildMongoURI(cfg, ""))
			tunnel, err := configureMongoTransportV1(clientOpts, &asset_entity.Asset{}, cfg, nil)
			So(err, ShouldBeNil)
			So(tunnel, ShouldBeNil)
			So(clientOpts.Dialer, ShouldNotBeNil)
		})

		Convey("隧道优先于代理且强制直连", func() {
			cfg := &asset_entity.MongoDBConfig{
				Host: "h", Port: 27017,
				Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
			}
			clientOpts := mongov1opts.Client().ApplyURI(buildMongoURI(cfg, ""))
			pool := sshpool.NewPool(nil, time.Minute)
			tunnel, err := configureMongoTransportV1(clientOpts, &asset_entity.Asset{SSHTunnelID: 5}, cfg, pool)
			So(err, ShouldBeNil)
			So(tunnel, ShouldNotBeNil)
			So(clientOpts.Dialer, ShouldNotBeNil)
			So(*clientOpts.Direct, ShouldBeTrue)
		})

		Convey("URI 模式下代理无需解析主机", func() {
			cfg := &asset_entity.MongoDBConfig{
				ConnectionURI: "mongodb://h1:27017,h2:27017/db?replicaSet=rs0",
				Proxy:         &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
			}
			clientOpts := mongov1opts.Client().ApplyURI(cfg.ConnectionURI)
			tunnel, err := configureMongoTransportV1(clientOpts, &asset_entity.Asset{}, cfg, nil)
			So(err, ShouldBeNil)
			So(tunnel, ShouldBeNil)
			So(clientOpts.Dialer, ShouldNotBeNil)
			So(clientOpts.Direct, ShouldBeNil)
		})

		Convey("代理链设置 dialer", func() {
			enabled := true
			cfg := &asset_entity.MongoDBConfig{
				Host: "h", Port: 27017,
				ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{{
					Type: asset_entity.ProxyChainLayerSOCKS5, Enabled: &enabled, Order: 1, Host: "p", Port: 1080,
				}}},
			}
			clientOpts := mongov1opts.Client().ApplyURI(buildMongoURI(cfg, ""))
			tunnel, err := configureMongoTransportV1(clientOpts, &asset_entity.Asset{}, cfg, nil)
			So(err, ShouldBeNil)
			So(tunnel, ShouldBeNil)
			So(clientOpts.Dialer, ShouldNotBeNil)
		})
	})
}
