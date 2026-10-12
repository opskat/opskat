package extension

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/connpool/connpooltest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

// TestAssetDialerForConfig covers DialContextForConfig — the ad-hoc dial
// path a "test connection" call resolves through instead of DialContextFor's
// database row, using the submitted form's own connection settings
// (extension.AdHocAssetConfig) rather than anything stored on an asset.
func TestAssetDialerForConfig(t *testing.T) {
	Convey("Given an extension with a plain and a fully connection-capable asset type", t, func() {
		ctrl := gomock.NewController(t)
		assets := mock_asset_repo.NewMockAssetRepo(ctrl)
		prev := asset_repo.Asset()
		asset_repo.RegisterAsset(assets)
		Reset(func() { asset_repo.RegisterAsset(prev) })

		svc := extension_svc.New(nil, nil, nil, assets, zap.NewNop(), nil, nil)
		svc.Bridge().Register(connectionExt("es", "es-cluster", &extension.ConnectionDef{SSHTunnel: true, ProxyChain: true, TLS: true}))
		svc.Bridge().Register(connectionExt("plain", "plain-store", nil))
		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		e.SetService(svc)
		ctx := context.Background()

		Convey("an asset type with no declared connection support dials directly", func() {
			dial, tlsConfig, err := e.NewAssetDialer("plain").DialContextForConfig(ctx, "plain-store", &extension.AdHocAssetConfig{
				Config:      mustJSONAdHoc(t, map[string]any{}),
				SSHTunnelID: 99,
			})
			So(err, ShouldBeNil)
			So(dial, ShouldBeNil)
			So(tlsConfig, ShouldBeNil)
		})

		Convey("an unknown asset type is refused", func() {
			_, _, err := e.NewAssetDialer("es").DialContextForConfig(ctx, "nope", &extension.AdHocAssetConfig{})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "nope")
		})

		Convey("an unloaded extension is refused", func() {
			_, _, err := e.NewAssetDialer("ghost").DialContextForConfig(ctx, "es-cluster", &extension.AdHocAssetConfig{})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not loaded")
		})

		Convey("no SSH tunnel id, no proxy chain, no TLS: dials directly", func() {
			dial, tlsConfig, err := e.NewAssetDialer("es").DialContextForConfig(ctx, "es-cluster", &extension.AdHocAssetConfig{
				Config: mustJSONAdHoc(t, map[string]any{"endpoint": "es.internal:9200"}),
			})
			So(err, ShouldBeNil)
			So(dial, ShouldBeNil)
			So(tlsConfig, ShouldBeNil)
		})

		Convey("the submitted SSH tunnel id routes the dial to that SSH asset, never to the target", func() {
			sshd, sshdHits := countingListener(t)
			target, targetHits := countingListener(t)
			host, port, _ := net.SplitHostPort(sshd.Addr().String())
			portN, _ := strconv.Atoi(port)
			sshCfg, _ := json.Marshal(asset_entity.SSHConfig{Host: host, Port: portN, Username: "root", AuthType: "password"})
			const tunnelID int64 = 42
			assets.EXPECT().Find(gomock.Any(), tunnelID).
				Return(&asset_entity.Asset{ID: tunnelID, Name: "bastion", Type: asset_entity.AssetTypeSSH, Config: string(sshCfg)}, nil)

			dial, _, err := e.NewAssetDialer("es").DialContextForConfig(ctx, "es-cluster", &extension.AdHocAssetConfig{
				SSHTunnelID: tunnelID,
			})
			So(err, ShouldBeNil)
			So(dial, ShouldNotBeNil)

			dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, err = dial(dialCtx, "tcp", target.Addr().String())
			So(err, ShouldNotBeNil) // the stand-in sshd hangs up before the handshake
			So(sshdHits.Load(), ShouldEqual, int32(1))
			So(targetHits.Load(), ShouldEqual, int32(0))
		})

		Convey("the submitted proxy chain routes the dial to the proxy hop, never to the target", func() {
			proxy, proxyHits := countingListener(t)
			target, targetHits := countingListener(t)
			host, port, _ := net.SplitHostPort(proxy.Addr().String())
			portN, _ := strconv.Atoi(port)
			chain, _ := json.Marshal(map[string]any{
				"layers": []map[string]any{{"id": "hop1", "type": "socks5", "enabled": true, "host": host, "port": portN}},
			})

			dial, tlsConfig, err := e.NewAssetDialer("es").DialContextForConfig(ctx, "es-cluster", &extension.AdHocAssetConfig{
				ProxyChain: chain,
			})
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

		Convey("submitted TLS settings, enabled, are built for the dial", func() {
			tls, _ := json.Marshal(map[string]any{"enabled": true, "insecure": true, "serverName": "es.example.com"})
			dial, tlsConfig, err := e.NewAssetDialer("es").DialContextForConfig(ctx, "es-cluster", &extension.AdHocAssetConfig{
				TLS: tls,
			})
			So(err, ShouldBeNil)
			So(dial, ShouldBeNil) // no tunnel/chain submitted, TLS only
			So(tlsConfig, ShouldNotBeNil)
			So(tlsConfig.InsecureSkipVerify, ShouldBeTrue)
			So(tlsConfig.ServerName, ShouldEqual, "es.example.com")
		})

		Convey("submitted TLS settings left disabled have no effect", func() {
			tls, _ := json.Marshal(map[string]any{"enabled": false, "insecure": true})
			_, tlsConfig, err := e.NewAssetDialer("es").DialContextForConfig(ctx, "es-cluster", &extension.AdHocAssetConfig{
				TLS: tls,
			})
			So(err, ShouldBeNil)
			So(tlsConfig, ShouldBeNil)
		})

		Convey("a CA certificate that does not parse fails, with no fallback to unverified", func() {
			tls, _ := json.Marshal(map[string]any{"enabled": true, "caCert": "not a certificate"})
			dial, tlsConfig, err := e.NewAssetDialer("es").DialContextForConfig(ctx, "es-cluster", &extension.AdHocAssetConfig{
				TLS: tls,
			})
			So(dial, ShouldBeNil)
			So(tlsConfig, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		Convey("a TLS CA file that cannot be read fails, with no fallback to unverified", func() {
			tls, _ := json.Marshal(map[string]any{"enabled": true, "caFile": "/nonexistent/ca.pem"})
			_, tlsConfig, err := e.NewAssetDialer("es").DialContextForConfig(ctx, "es-cluster", &extension.AdHocAssetConfig{
				TLS: tls,
			})
			So(tlsConfig, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		// The form encrypts a client key before it leaves, for a test as for a save.
		Convey("submitted certificate content is used as it is, the client key decrypted for the dial", func() {
			credential_svc.SetDefault(credential_svc.New("dialer-test-key", []byte("0123456789abcdef")))
			certPEM, keyPEM := connpooltest.SelfSignedPEM(t)
			encryptedKey, err := credential_svc.Default().Encrypt(string(keyPEM))
			So(err, ShouldBeNil)
			tls, _ := json.Marshal(map[string]any{
				"enabled": true, "caCert": string(certPEM), "clientCert": string(certPEM), "clientKey": encryptedKey,
			})
			_, tlsConfig, err := e.NewAssetDialer("es").DialContextForConfig(ctx, "es-cluster", &extension.AdHocAssetConfig{
				TLS: tls,
			})
			So(err, ShouldBeNil)
			So(tlsConfig.RootCAs, ShouldNotBeNil)
			So(tlsConfig.Certificates, ShouldHaveLength, 1)
		})
	})
}

func mustJSONAdHoc(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
