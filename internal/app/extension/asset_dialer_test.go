package extension

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

var endpointSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"endpoint": map[string]any{"type": "string", "format": "endpoint"}},
}

func connectionExt(name, assetType string, conn *extension.ConnectionDef) *extension.Extension {
	return &extension.Extension{
		Name: name,
		Manifest: &extension.Manifest{
			Name:       name,
			AssetTypes: []extension.AssetTypeDef{{Type: assetType, ConfigSchema: endpointSchema, Connection: conn}},
		},
	}
}

// countingListener stands in for a host: it counts the connections it accepts
// and closes each at once.
func countingListener(t *testing.T) (net.Listener, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var n atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			_ = c.Close()
		}
	}()
	return ln, &n
}

func TestAssetDialerAppliesDeclaredSSHTunnel(t *testing.T) {
	Convey("an extension asset's SSH tunnel is applied only when its type declares it", t, func() {
		ctrl := gomock.NewController(t)
		assets := mock_asset_repo.NewMockAssetRepo(ctrl)
		// The tunnel's own SSH asset is resolved by the shared proxy-chain resolver
		// through the global asset repo.
		prev := asset_repo.Asset()
		asset_repo.RegisterAsset(assets)
		Reset(func() { asset_repo.RegisterAsset(prev) })

		svc := extension_svc.New(nil, nil, nil, assets, zap.NewNop(), nil, nil)
		svc.Bridge().Register(connectionExt("es", "es-cluster", &extension.ConnectionDef{SSHTunnel: true}))
		svc.Bridge().Register(connectionExt("plain", "plain-store", nil))
		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		e.SetService(svc)
		ctx := context.Background()

		const tunnelID int64 = 7
		assetOf := func(id int64, typ string, tunnel int64) *asset_entity.Asset {
			return &asset_entity.Asset{ID: id, Name: typ, Type: typ, Config: `{"endpoint":"es.internal:9200"}`, SSHTunnelID: tunnel}
		}

		Convey("an undeclared tunnel has no effect: the asset dials directly", func() {
			assets.EXPECT().Find(gomock.Any(), int64(1)).Return(assetOf(1, "plain-store", tunnelID), nil)
			dial, _, _, err := e.NewAssetDialer("plain").DialContextFor(ctx, 1)
			So(err, ShouldBeNil)
			So(dial, ShouldBeNil)
		})

		Convey("a declared tunnel the asset leaves unset dials directly", func() {
			assets.EXPECT().Find(gomock.Any(), int64(2)).Return(assetOf(2, "es-cluster", 0), nil)
			dial, _, _, err := e.NewAssetDialer("es").DialContextFor(ctx, 2)
			So(err, ShouldBeNil)
			So(dial, ShouldBeNil)
		})

		Convey("a tunnel asset that cannot be resolved is an error, not a direct dial", func() {
			assets.EXPECT().Find(gomock.Any(), int64(3)).Return(assetOf(3, "es-cluster", tunnelID), nil)
			assets.EXPECT().Find(gomock.Any(), tunnelID).Return(nil, errors.New("record not found"))
			dial, _, _, err := e.NewAssetDialer("es").DialContextFor(ctx, 3)
			So(dial, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "record not found")
		})

		Convey("a set tunnel routes the dial to the SSH asset, never to the target", func() {
			sshd, sshdHits := countingListener(t)
			target, targetHits := countingListener(t)
			host, port, _ := net.SplitHostPort(sshd.Addr().String())
			portN, _ := strconv.Atoi(port)
			sshCfg, _ := json.Marshal(asset_entity.SSHConfig{Host: host, Port: portN, Username: "root", AuthType: "password"})
			assets.EXPECT().Find(gomock.Any(), int64(4)).Return(assetOf(4, "es-cluster", tunnelID), nil)
			assets.EXPECT().Find(gomock.Any(), tunnelID).
				Return(&asset_entity.Asset{ID: tunnelID, Name: "bastion", Type: asset_entity.AssetTypeSSH, Config: string(sshCfg)}, nil)

			dial, _, _, err := e.NewAssetDialer("es").DialContextFor(ctx, 4)
			So(err, ShouldBeNil)
			So(dial, ShouldNotBeNil)

			dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, err = dial(dialCtx, "tcp", target.Addr().String())
			So(err, ShouldNotBeNil) // the stand-in sshd hangs up before the handshake
			So(sshdHits.Load(), ShouldEqual, int32(1))
			So(targetHits.Load(), ShouldEqual, int32(0))
		})

		Convey("another extension's asset is refused", func() {
			assets.EXPECT().Find(gomock.Any(), int64(5)).Return(assetOf(5, "plain-store", 0), nil)
			_, _, _, err := e.NewAssetDialer("es").DialContextFor(ctx, 5)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "does not belong to extension")
		})
	})
}

func TestAssetDialerAppliesDeclaredProxyChain(t *testing.T) {
	Convey("an extension asset's proxy chain is applied only when its type declares it", t, func() {
		ctrl := gomock.NewController(t)
		assets := mock_asset_repo.NewMockAssetRepo(ctrl)
		prev := asset_repo.Asset()
		asset_repo.RegisterAsset(assets)
		Reset(func() { asset_repo.RegisterAsset(prev) })

		svc := extension_svc.New(nil, nil, nil, assets, zap.NewNop(), nil, nil)
		svc.Bridge().Register(connectionExt("es", "es-cluster", &extension.ConnectionDef{ProxyChain: true}))
		svc.Bridge().Register(connectionExt("plain", "plain-store", &extension.ConnectionDef{SSHTunnel: true}))
		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		e.SetService(svc)
		ctx := context.Background()

		chainConfig := func(proxyHost string, proxyPort int) string {
			cfg, _ := json.Marshal(map[string]any{
				"endpoint": "es.internal:9200",
				extension.HostConnectionConfigKey: map[string]any{
					"proxyChain": map[string]any{
						"layers": []map[string]any{
							{"id": "hop1", "type": "socks5", "enabled": true, "host": proxyHost, "port": proxyPort},
						},
					},
				},
			})
			return string(cfg)
		}

		Convey("a declared chain routes the dial to the proxy hop, never to the target", func() {
			proxy, proxyHits := countingListener(t)
			target, targetHits := countingListener(t)
			host, port, _ := net.SplitHostPort(proxy.Addr().String())
			portN, _ := strconv.Atoi(port)
			assets.EXPECT().Find(gomock.Any(), int64(10)).
				Return(&asset_entity.Asset{ID: 10, Name: "es-cluster", Type: "es-cluster", Config: chainConfig(host, portN)}, nil)

			dial, tlsConfig, _, err := e.NewAssetDialer("es").DialContextFor(ctx, 10)
			So(err, ShouldBeNil)
			So(dial, ShouldNotBeNil)
			So(tlsConfig, ShouldBeNil)

			dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, err = dial(dialCtx, "tcp", target.Addr().String())
			So(err, ShouldNotBeNil) // the stand-in proxy hangs up before completing the SOCKS5 handshake
			So(proxyHits.Load(), ShouldEqual, int32(1))
			So(targetHits.Load(), ShouldEqual, int32(0))
		})

		// The shared resolver lets a non-empty chain win over the tunnel column; a
		// type declaring both would silently skip the SSH hop the user picked.
		Convey("an asset setting both an SSH tunnel and a proxy chain is refused, not dialed without the tunnel", func() {
			svc.Bridge().Register(connectionExt("both", "both-store", &extension.ConnectionDef{SSHTunnel: true, ProxyChain: true}))
			assets.EXPECT().Find(gomock.Any(), int64(12)).
				Return(&asset_entity.Asset{ID: 12, Name: "both-store", Type: "both-store", Config: chainConfig("127.0.0.1", 1080), SSHTunnelID: 7}, nil)

			dial, _, _, err := e.NewAssetDialer("both").DialContextFor(ctx, 12)
			So(dial, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "SSH tunnel")
		})

		Convey("an undeclared chain has no effect: the asset dials directly", func() {
			proxy, proxyHits := countingListener(t)
			host, port, _ := net.SplitHostPort(proxy.Addr().String())
			portN, _ := strconv.Atoi(port)
			assets.EXPECT().Find(gomock.Any(), int64(11)).
				Return(&asset_entity.Asset{ID: 11, Name: "plain-store", Type: "plain-store", Config: chainConfig(host, portN)}, nil)

			dial, tlsConfig, _, err := e.NewAssetDialer("plain").DialContextFor(ctx, 11)
			So(err, ShouldBeNil)
			So(dial, ShouldBeNil)
			So(tlsConfig, ShouldBeNil)
			So(proxyHits.Load(), ShouldEqual, int32(0))
		})
	})
}

func TestAssetDialerAppliesDeclaredTLS(t *testing.T) {
	Convey("an extension asset's TLS settings are applied only when its type declares and enables them", t, func() {
		ctrl := gomock.NewController(t)
		assets := mock_asset_repo.NewMockAssetRepo(ctrl)
		prev := asset_repo.Asset()
		asset_repo.RegisterAsset(assets)
		Reset(func() { asset_repo.RegisterAsset(prev) })

		svc := extension_svc.New(nil, nil, nil, assets, zap.NewNop(), nil, nil)
		svc.Bridge().Register(connectionExt("es", "es-cluster", &extension.ConnectionDef{TLS: true}))
		svc.Bridge().Register(connectionExt("plain", "plain-store", nil))
		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		e.SetService(svc)
		ctx := context.Background()

		tlsConfigJSON := func(tlsSettings map[string]any) string {
			cfg, _ := json.Marshal(map[string]any{
				"endpoint": "es.internal:9200",
				extension.HostConnectionConfigKey: map[string]any{
					"tls": tlsSettings,
				},
			})
			return string(cfg)
		}

		Convey("a declared, enabled TLS config is built for the dial", func() {
			assets.EXPECT().Find(gomock.Any(), int64(20)).Return(&asset_entity.Asset{
				ID: 20, Name: "es-cluster", Type: "es-cluster",
				Config: tlsConfigJSON(map[string]any{"enabled": true, "insecure": true, "serverName": "es.example.com"}),
			}, nil)

			dial, tlsConfig, _, err := e.NewAssetDialer("es").DialContextFor(ctx, 20)
			So(err, ShouldBeNil)
			So(dial, ShouldBeNil) // no tunnel/chain declared, TLS only
			So(tlsConfig, ShouldNotBeNil)
			So(tlsConfig.InsecureSkipVerify, ShouldBeTrue)
			So(tlsConfig.ServerName, ShouldEqual, "es.example.com")
		})

		Convey("a declared TLS config left disabled has no effect", func() {
			assets.EXPECT().Find(gomock.Any(), int64(21)).Return(&asset_entity.Asset{
				ID: 21, Name: "es-cluster", Type: "es-cluster",
				Config: tlsConfigJSON(map[string]any{"enabled": false, "insecure": true}),
			}, nil)

			_, tlsConfig, _, err := e.NewAssetDialer("es").DialContextFor(ctx, 21)
			So(err, ShouldBeNil)
			So(tlsConfig, ShouldBeNil)
		})

		Convey("an undeclared TLS setting has no effect even if present in the stored config", func() {
			assets.EXPECT().Find(gomock.Any(), int64(22)).Return(&asset_entity.Asset{
				ID: 22, Name: "plain-store", Type: "plain-store",
				Config: tlsConfigJSON(map[string]any{"enabled": true, "insecure": true}),
			}, nil)

			_, tlsConfig, _, err := e.NewAssetDialer("plain").DialContextFor(ctx, 22)
			So(err, ShouldBeNil)
			So(tlsConfig, ShouldBeNil)
		})

		Convey("a TLS CA file that cannot be read fails the dial, with no fallback to unverified", func() {
			assets.EXPECT().Find(gomock.Any(), int64(23)).Return(&asset_entity.Asset{
				ID: 23, Name: "es-cluster", Type: "es-cluster",
				Config: tlsConfigJSON(map[string]any{"enabled": true, "caFile": "/nonexistent/ca.pem"}),
			}, nil)

			dial, tlsConfig, _, err := e.NewAssetDialer("es").DialContextFor(ctx, 23)
			So(dial, ShouldBeNil)
			So(tlsConfig, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "23")
		})
	})
}
