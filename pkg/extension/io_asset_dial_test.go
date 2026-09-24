package extension

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// fakeAssetDialer stands in for the host's connection path (SSH tunnel, later
// proxy chain / TLS): whatever address the guest asks for, it connects to the
// server that plays "the far side of the tunnel", and records what it was asked.
type fakeAssetDialer struct {
	farSide string // address actually connected to
	openErr error  // DialContextFor fails: the path itself cannot be built
	dialErr error  // the returned dial fails: the tunnel is up but unreachable
	direct  bool   // the asset has no connection settings: dial directly

	mu     sync.Mutex
	assets []int64
	addrs  []string
}

func (f *fakeAssetDialer) DialContextFor(_ context.Context, assetID int64) (DialContextFunc, error) {
	f.mu.Lock()
	f.assets = append(f.assets, assetID)
	f.mu.Unlock()
	if f.openErr != nil {
		return nil, f.openErr
	}
	if f.direct {
		return nil, nil
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		f.mu.Lock()
		f.addrs = append(f.addrs, addr)
		f.mu.Unlock()
		if f.dialErr != nil {
			return nil, f.dialErr
		}
		return (&net.Dialer{}).DialContext(ctx, network, f.farSide)
	}, nil
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
	})
}
