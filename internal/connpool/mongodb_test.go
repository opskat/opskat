package connpool

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"
)

func TestBuildMongoURI(t *testing.T) {
	Convey("buildMongoURI", t, func() {
		Convey("基本连接（用户名+密码）", func() {
			cfg := &asset_entity.MongoDBConfig{
				Host:     "localhost",
				Port:     27017,
				Username: "admin",
				Database: "mydb",
			}
			uri := buildMongoURI(cfg, "secret")
			So(uri, ShouldEqual, "mongodb://admin:secret@localhost:27017/mydb")
		})

		Convey("带 AuthSource 和 ReplicaSet", func() {
			cfg := &asset_entity.MongoDBConfig{
				Host:       "mongo.example.com",
				Port:       27017,
				Username:   "user",
				Database:   "appdb",
				AuthSource: "admin",
				ReplicaSet: "rs0",
			}
			uri := buildMongoURI(cfg, "pass")
			So(uri, ShouldContainSubstring, "mongodb://user:pass@mongo.example.com:27017/appdb")
			So(uri, ShouldContainSubstring, "authSource=admin")
			So(uri, ShouldContainSubstring, "replicaSet=rs0")
		})

		Convey("无用户名（无认证）", func() {
			cfg := &asset_entity.MongoDBConfig{
				Host: "localhost",
				Port: 27017,
			}
			uri := buildMongoURI(cfg, "")
			So(uri, ShouldEqual, "mongodb://localhost:27017")
		})

		Convey("默认端口（Port=0 时使用 27017）", func() {
			cfg := &asset_entity.MongoDBConfig{
				Host:     "db.example.com",
				Port:     0,
				Username: "root",
			}
			uri := buildMongoURI(cfg, "pw")
			So(uri, ShouldContainSubstring, "db.example.com:27017")
		})
	})
}

func TestInjectPassword(t *testing.T) {
	Convey("injectPassword", t, func() {
		Convey("向包含用户名但无密码的 URI 注入密码", func() {
			uri := "mongodb://admin@localhost:27017/mydb"
			result := injectPassword(uri, "secret")
			So(result, ShouldContainSubstring, "admin:secret@")
		})

		Convey("空密码时不修改 URI", func() {
			uri := "mongodb://admin@localhost:27017/mydb"
			result := injectPassword(uri, "")
			So(result, ShouldEqual, uri)
		})

		Convey("URI 中已有密码时不覆盖", func() {
			uri := "mongodb://admin:existing@localhost:27017/mydb" //nolint:gosec // test data
			result := injectPassword(uri, "newpass")
			So(result, ShouldEqual, uri)
		})

		Convey("URI 无用户信息时原样返回", func() {
			uri := "mongodb://localhost:27017/mydb"
			result := injectPassword(uri, "secret")
			So(result, ShouldEqual, uri)
		})
	})
}

func TestParseHostFromURI(t *testing.T) {
	Convey("parseHostFromURI", t, func() {
		Convey("标准 URI（含端口）", func() {
			host, port, err := parseHostFromURI("mongodb://user:pass@mongo.example.com:27017/db")
			So(err, ShouldBeNil)
			So(host, ShouldEqual, "mongo.example.com")
			So(port, ShouldEqual, 27017)
		})

		Convey("URI 不含端口时默认使用 27017", func() {
			host, port, err := parseHostFromURI("mongodb://user:pass@mongo.example.com/db")
			So(err, ShouldBeNil)
			So(host, ShouldEqual, "mongo.example.com")
			So(port, ShouldEqual, 27017)
		})

		Convey("多主机副本集时取第一个", func() {
			host, port, err := parseHostFromURI("mongodb://host1:27017,host2:27018,host3:27019/db")
			So(err, ShouldBeNil)
			So(host, ShouldEqual, "host1")
			So(port, ShouldEqual, 27017)
		})

		Convey("非标准端口", func() {
			host, port, err := parseHostFromURI("mongodb://localhost:37017/")
			So(err, ShouldBeNil)
			So(host, ShouldEqual, "localhost")
			So(port, ShouldEqual, 37017)
		})
	})
}

func TestConfigureMongoTransport(t *testing.T) {
	Convey("configureMongoTransport", t, func() {
		Convey("直连不设置 dialer", func() {
			cfg := &asset_entity.MongoDBConfig{Host: "h", Port: 27017}
			clientOpts := options.Client().ApplyURI(buildMongoURI(cfg, ""))
			tunnel, err := configureMongoTransport(clientOpts, &asset_entity.Asset{}, cfg, nil)
			So(err, ShouldBeNil)
			So(tunnel, ShouldBeNil)
			So(clientOpts.Dialer, ShouldBeNil)
			So(clientOpts.Direct, ShouldBeNil)
		})

		Convey("代理设置 dialer 且不强制直连", func() {
			// SOCKS5 按目标地址远端解析,副本集发现可正常工作,不能 SetDirect
			cfg := &asset_entity.MongoDBConfig{
				Host: "h", Port: 27017,
				Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
			}
			clientOpts := options.Client().ApplyURI(buildMongoURI(cfg, ""))
			tunnel, err := configureMongoTransport(clientOpts, &asset_entity.Asset{}, cfg, nil)
			So(err, ShouldBeNil)
			So(tunnel, ShouldBeNil)
			So(clientOpts.Dialer, ShouldNotBeNil)
			So(clientOpts.Direct, ShouldBeNil)
		})

		Convey("隧道优先于代理且强制直连", func() {
			cfg := &asset_entity.MongoDBConfig{
				Host: "h", Port: 27017,
				Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
			}
			clientOpts := options.Client().ApplyURI(buildMongoURI(cfg, ""))
			pool := sshpool.NewPool(nil, time.Minute)
			tunnel, err := configureMongoTransport(clientOpts, &asset_entity.Asset{SSHTunnelID: 5}, cfg, pool)
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
			clientOpts := options.Client().ApplyURI(cfg.ConnectionURI)
			tunnel, err := configureMongoTransport(clientOpts, &asset_entity.Asset{}, cfg, nil)
			So(err, ShouldBeNil)
			So(tunnel, ShouldBeNil)
			So(clientOpts.Dialer, ShouldNotBeNil)
			So(clientOpts.Direct, ShouldBeNil)
		})
	})
}
