package extension

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// fakeAssetDialer stands in for the host's connection path (SSH tunnel, proxy
// chain, TLS): whatever address the guest asks for, it connects to the server
// that plays "the far side of the tunnel", and records what it was asked.
type fakeAssetDialer struct {
	farSide   string      // address actually connected to
	openErr   error       // DialContextFor fails: the path itself cannot be built
	dialErr   error       // the returned dial fails: the tunnel is up but unreachable
	direct    bool        // the asset has no connection settings: dial directly
	tlsConfig *tls.Config // asset's declared TLS settings, nil = none

	mu     sync.Mutex
	assets []int64
	addrs  []string
}

func (f *fakeAssetDialer) DialContextFor(_ context.Context, assetID int64) (DialContextFunc, *tls.Config, string, error) {
	f.mu.Lock()
	f.assets = append(f.assets, assetID)
	f.mu.Unlock()
	if f.openErr != nil {
		return nil, nil, "", f.openErr
	}
	if f.direct {
		return nil, nil, "", nil
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		f.mu.Lock()
		f.addrs = append(f.addrs, addr)
		f.mu.Unlock()
		if f.dialErr != nil {
			return nil, f.dialErr
		}
		return (&net.Dialer{}).DialContext(ctx, network, f.farSide)
	}
	return dial, f.tlsConfig, "fixed", nil
}

// selfSignedCert generates a self-signed cert/key pair valid for 127.0.0.1.
func selfSignedCert(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"es.internal.invalid"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, leaf
}

// tlsHTTPServer starts an HTTPS test server whose self-signed cert covers
// "es.internal.invalid" — the fixture's endpoint hostname — so http.Transport's
// own hostname verification (against the request's Host, not the dialed addr)
// has something real to check.
func tlsHTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	cert, _ := selfSignedCert(t)
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// tlsEchoListener starts a self-signed TLS echo server; returns the listener
// and a cert pool that trusts it.
func tlsEchoListener(t *testing.T) (net.Listener, *x509.CertPool) {
	t.Helper()
	cert, leaf := selfSignedCert(t)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 64)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				_, _ = c.Write(buf[:n])
			}()
		}
	}()
	return ln, pool
}

func (f *fakeAssetDialer) dialed() ([]int64, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.assets...), append([]string(nil), f.addrs...)
}

// newDialFixture loads the fixture extension (network.assetEndpoint declared)
// over a DefaultHostProvider whose asset connection path is dialer.
func newDialFixture(t *testing.T, dialer AssetDialer, config map[string]any) (*Plugin, *Manifest) {
	t.Helper()
	manifest := fixtureManifest(t)
	manifest.Capabilities.Network.AssetEndpoint = true
	inner := NewDefaultHostProvider(DefaultHostConfig{
		AssetConfigs: assetConfigs{fixtureAsset.ID: mustJSON(t, config)},
		AssetDialer:  dialer,
	})
	p, err := LoadPlugin(context.Background(), manifest, fixtureWasm(t), NewCapabilityHost(inner, manifest, t.TempDir()), nil)
	if err != nil {
		t.Fatalf("load fixture plugin: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Errorf("close plugin: %v", err)
		}
	})
	payload, err := p.Describe(context.Background())
	if err != nil {
		t.Fatalf("describe fixture: %v", err)
	}
	desc, err := ParseDescriptor(payload)
	if err != nil {
		t.Fatalf("parse fixture descriptor: %v", err)
	}
	manifest.apply(desc)
	return p, manifest
}

func TestAssetConnectionPath(t *testing.T) {
	Convey("Given an asset whose endpoint only resolves on the far side of its SSH tunnel", t, func() {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("through the tunnel"))
		}))
		defer server.Close()
		broker := echoListener(t)

		// .invalid never resolves locally (RFC 2606): a dial that reached the
		// local resolver or a direct connect would fail.
		const esURL = "http://es.internal.invalid:9200"
		const brokerAddr = "kafka.internal.invalid:9092"

		Convey("the fixture declares the SSH tunnel on its asset type", func() {
			_, m := newDialFixture(t, &fakeAssetDialer{farSide: server.Listener.Addr().String()}, map[string]any{"endpoint": esURL})
			So(m.AssetTypeDef("fixture").Connection, ShouldResemble, &ConnectionDef{SSHTunnel: true})
		})

		Convey("an HTTP request to the endpoint is dialed through the asset's connection path", func() {
			dialer := &fakeAssetDialer{farSide: server.Listener.Addr().String()}
			p, _ := newDialFixture(t, dialer, map[string]any{"endpoint": esURL})

			out := callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": esURL + "/_cluster/health"})

			So(out["body"], ShouldEqual, "through the tunnel")
			assets, addrs := dialer.dialed()
			So(assets, ShouldResemble, []int64{fixtureAsset.ID})
			So(addrs, ShouldResemble, []string{"es.internal.invalid:9200"})
		})

		Convey("a TCP connection to the endpoint is dialed through the asset's connection path", func() {
			dialer := &fakeAssetDialer{farSide: broker.Addr().String()}
			p, _ := newDialFixture(t, dialer, map[string]any{"endpoint": brokerAddr})

			out := callToolOn(t, p, fixtureAsset, "tcp_echo", map[string]any{"addr": brokerAddr})

			So(out["echo"], ShouldEqual, "ping")
			_, addrs := dialer.dialed()
			So(addrs, ShouldResemble, []string{brokerAddr})
		})

		Convey("an asset without connection settings is dialed directly", func() {
			dialer := &fakeAssetDialer{direct: true}
			p, _ := newDialFixture(t, dialer, map[string]any{"endpoint": server.URL})

			out := callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": server.URL + "/"})

			So(out["body"], ShouldEqual, "through the tunnel") // same server, reached directly
			assets, addrs := dialer.dialed()
			So(assets, ShouldResemble, []int64{fixtureAsset.ID})
			So(addrs, ShouldBeEmpty)
		})

		Convey("when the path cannot be built the host error reaches the caller, with no direct dial", func() {
			// The endpoint is the real, directly reachable server: a fallback to a
			// direct dial would succeed and hide the failure.
			dialer := &fakeAssetDialer{openErr: errors.New("ssh tunnel asset 7 unreachable: connection refused")}
			p, _ := newDialFixture(t, dialer, map[string]any{"endpoint": server.URL})

			_, err := p.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": server.URL + "/"}), fixtureAsset)

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ssh tunnel asset 7 unreachable: connection refused")
		})

		Convey("when the tunnel refuses the dial the host error reaches the caller, with no direct dial", func() {
			dialer := &fakeAssetDialer{dialErr: errors.New("dial through tunnel: administratively prohibited")}
			hp, _ := newDialFixture(t, dialer, map[string]any{"endpoint": server.URL})
			tp, _ := newDialFixture(t, dialer, map[string]any{"endpoint": broker.Addr().String()})

			_, err := hp.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": server.URL + "/"}), fixtureAsset)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "administratively prohibited")

			_, err = tp.CallTool(ctx, "tcp_echo", mustJSON(t, map[string]any{"addr": broker.Addr().String()}), fixtureAsset)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "administratively prohibited")
		})

		Convey("TLS: an HTTPS request to the endpoint is verified against the asset's declared CA", func() {
			tlsServer := tlsHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("through TLS"))
			}))
			pool := x509.NewCertPool()
			pool.AddCert(tlsServer.Certificate())

			const httpsURL = "https://es.internal.invalid:9200"
			dialer := &fakeAssetDialer{
				farSide:   tlsServer.Listener.Addr().String(),
				tlsConfig: &tls.Config{RootCAs: pool},
			}
			p, _ := newDialFixture(t, dialer, map[string]any{"endpoint": httpsURL})

			out := callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": httpsURL + "/_cluster/health"})
			So(out["body"], ShouldEqual, "through TLS")
		})

		Convey("TLS: without the asset's CA, an HTTPS request to the endpoint fails verbatim, no fallback to unverified", func() {
			tlsServer := tlsHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("through TLS"))
			}))

			const httpsURL = "https://es.internal.invalid:9200"
			// Declared TLS with no RootCAs: the server's self-signed cert must not verify.
			dialer := &fakeAssetDialer{farSide: tlsServer.Listener.Addr().String(), tlsConfig: &tls.Config{}}
			p, _ := newDialFixture(t, dialer, map[string]any{"endpoint": httpsURL})

			_, err := p.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": httpsURL + "/_cluster/health"}), fixtureAsset)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "certificate")
		})

		Convey("TLS: a TCP connection to the endpoint is wrapped in TLS using the asset's config", func() {
			ln, pool := tlsEchoListener(t)

			dialer := &fakeAssetDialer{farSide: ln.Addr().String(), tlsConfig: &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"}}
			p, _ := newDialFixture(t, dialer, map[string]any{"endpoint": brokerAddr})

			out := callToolOn(t, p, fixtureAsset, "tcp_echo", map[string]any{"addr": brokerAddr})
			So(out["echo"], ShouldEqual, "ping")
		})

		Convey("TLS: a handshake failure on a TCP connection surfaces verbatim, no fallback to plaintext", func() {
			ln, _ := tlsEchoListener(t)

			// No RootCAs and an unrelated ServerName: the self-signed cert must not verify.
			dialer := &fakeAssetDialer{farSide: ln.Addr().String(), tlsConfig: &tls.Config{ServerName: "127.0.0.1"}}
			p, _ := newDialFixture(t, dialer, map[string]any{"endpoint": brokerAddr})

			_, err := p.CallTool(ctx, "tcp_echo", mustJSON(t, map[string]any{"addr": brokerAddr}), fixtureAsset)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "certificate")
		})
	})
}
