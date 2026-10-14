package extension

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// recordingHost captures OpenIO calls to verify delegation through capHost.
// Non-OpenIO methods embed HostProvider (nil) and panic if called; the tests
// below only exercise OpenIO.
type recordingHost struct {
	HostProvider
	lastParams IOOpenParams
	opened     int
}

func (r *recordingHost) OpenIO(_ context.Context, _ *AssetRef, params IOOpenParams) (*IOResource, error) {
	r.opened++
	r.lastParams = params
	return &IOResource{}, nil
}

func (r *recordingHost) SettingsGet() (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *recordingHost) SettingSecrets([]string) (map[string]string, error) {
	return map[string]string{}, nil
}

// TestCapHostTCPPassthrough pins that an extension which does not declare
// network.assetEndpoint keeps today's ungated TCP (first-party Kafka builds on
// it); declaring the capability puts TCP under the endpoint gate below.
func TestCapHostTCPPassthrough(t *testing.T) {
	Convey("Given a capHost wrapping a manifest with no capabilities", t, func() {
		inner := &recordingHost{}
		manifest := &Manifest{Name: "no-caps", Version: "1.0.0"}
		ch := NewCapabilityHost(inner, manifest, "/tmp/ext")

		Convey("OpenIO(type=tcp) is not blocked and is delegated to the inner host", func() {
			res, err := ch.OpenIO(context.Background(), nil, IOOpenParams{Type: "tcp", Addr: "example.com:9092"})
			So(err, ShouldBeNil)
			So(res, ShouldNotBeNil)
			So(inner.opened, ShouldEqual, 1)
			So(inner.lastParams.Type, ShouldEqual, "tcp")
			So(inner.lastParams.Addr, ShouldEqual, "example.com:9092")
		})

		Convey("OpenIO(type=http) is still gated by the allowlist", func() {
			_, err := ch.OpenIO(context.Background(), nil, IOOpenParams{Type: "http", URL: "https://example.com/foo"})
			So(err, ShouldNotBeNil) // allowlist is empty, so reject
		})
	})
}

// Compile-time assertion that recordingHost satisfies HostProvider.
var _ HostProvider = (*recordingHost)(nil)

// configHost is an inner provider that serves one asset config and records what
// capHost finally hands to OpenIO.
type configHost struct {
	recordingHost
	config json.RawMessage
}

func (h *configHost) GetAssetConfig(int64) (json.RawMessage, error) { return h.config, nil }

func endpointManifest(schemaProps map[string]any) *Manifest {
	return &Manifest{
		Name: "ep", Version: "1.0.0",
		Capabilities: Capabilities{Network: NetworkCapability{AssetEndpoint: true}},
		AssetTypes: []AssetTypeDef{{
			Type:         "ep",
			ConfigSchema: map[string]any{"type": "object", "properties": schemaProps},
		}},
	}
}

func TestCapHostAssetEndpointGate(t *testing.T) {
	Convey("Given an extension declaring network.assetEndpoint", t, func() {
		ctx := context.Background()
		asset := &AssetRef{ID: 7, Name: "a", Type: "ep"}
		inner := &configHost{config: json.RawMessage(`{
			"url": "https://ES.internal:9200/base",
			"plain": "https://api.example.com",
			"broker": "10.0.0.5:9092",
			"v6": "[fd00::1]:9300",
			"empty": "",
			"port": 9200,
			"other": "10.0.0.9:22"
		}`)}
		manifest := endpointManifest(map[string]any{
			"url":    map[string]any{"type": "string", "format": "endpoint"},
			"plain":  map[string]any{"type": "string", "format": "endpoint"},
			"broker": map[string]any{"type": "string", "format": "endpoint"},
			"v6":     map[string]any{"type": "string", "format": "endpoint"},
			"empty":  map[string]any{"type": "string", "format": "endpoint"},
			"port":   map[string]any{"type": "number", "format": "endpoint"},
			"other":  map[string]any{"type": "string"},
		})
		ch := NewCapabilityHost(inner, manifest, "/tmp/ext")
		open := func(p IOOpenParams) error {
			_, err := ch.OpenIO(ctx, asset, p)
			return err
		}

		Convey("HTTP to an endpoint matches on scheme, case-insensitive host and port", func() {
			for _, u := range []string{
				"https://es.internal:9200/_search",
				"https://es.internal:9200",
				"https://api.example.com/v1",
				"https://api.example.com:443/v1",
				"http://10.0.0.5:9092/",
				"https://[fd00::1]:9300/",
			} {
				So(open(IOOpenParams{Type: "http", URL: u}), ShouldBeNil)
				So(inner.lastParams.AllowPrivate, ShouldBeTrue)
				So(inner.lastParams.RedirectGuard, ShouldNotBeNil)
			}
		})

		Convey("HTTP to anything else is rejected as not an endpoint", func() {
			for _, u := range []string{
				"http://es.internal:9200/",  // scheme differs
				"https://es.internal:9201/", // port differs
				"https://es.internal/",      // default port differs
				"http://api.example.com/",   // scheme differs
				"https://10.0.0.9:22/",      // a non-endpoint field's value
				"https://evil.example.com/",
			} {
				err := open(IOOpenParams{Type: "http", URL: u})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			}
			So(inner.opened, ShouldEqual, 0)
		})

		Convey("the redirect guard admits endpoints only", func() {
			So(open(IOOpenParams{Type: "http", URL: "https://es.internal:9200/"}), ShouldBeNil)
			guard := inner.lastParams.RedirectGuard
			ok, _ := url.Parse("https://es.internal:9200/other")
			bad, _ := url.Parse("http://169.254.169.254/latest")
			So(guard(ok), ShouldBeNil)
			So(guard(bad).Error(), ShouldContainSubstring, "not an endpoint of the asset")
		})

		Convey("a guest cannot pre-set allowPrivate for a non-endpoint target", func() {
			err := open(IOOpenParams{Type: "http", URL: "http://10.0.0.9:22/", AllowPrivate: true})
			So(err, ShouldNotBeNil)
		})

		Convey("TCP to an endpoint's host and port is allowed, anything else rejected", func() {
			So(open(IOOpenParams{Type: "tcp", Addr: "10.0.0.5:9092"}), ShouldBeNil)
			So(open(IOOpenParams{Type: "tcp", Addr: "es.internal:9200"}), ShouldBeNil)
			So(open(IOOpenParams{Type: "tcp", Addr: "api.example.com:443"}), ShouldBeNil)
			So(open(IOOpenParams{Type: "tcp", Addr: "[fd00::1]:9300"}), ShouldBeNil)
			for _, addr := range []string{"10.0.0.5:9093", "10.0.0.9:22", "es.internal:9201", "garbage"} {
				err := open(IOOpenParams{Type: "tcp", Addr: addr})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			}
		})

		Convey("a call not scoped to an asset has no endpoints", func() {
			_, err := ch.OpenIO(ctx, nil, IOOpenParams{Type: "tcp", Addr: "10.0.0.5:9092"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not scoped to an asset")
			_, err = ch.OpenIO(ctx, nil, IOOpenParams{Type: "http", URL: "https://es.internal:9200/"})
			So(err, ShouldNotBeNil)
		})

		Convey("the static allowlist still admits its URLs, without private reach", func() {
			manifest.Capabilities.HTTP.Allowlist = []string{"https://public.example.com/"}
			So(open(IOOpenParams{Type: "http", URL: "https://public.example.com/x", AllowPrivate: true}), ShouldBeNil)
			So(inner.lastParams.AllowPrivate, ShouldBeFalse)
			So(inner.lastParams.RedirectGuard, ShouldBeNil)
		})
	})
}

func TestEndpointFieldsFromSchema(t *testing.T) {
	Convey("EndpointFieldsFromSchema lists format:endpoint properties, sorted", t, func() {
		schema := map[string]any{"properties": map[string]any{
			"z":    map[string]any{"format": "endpoint"},
			"a":    map[string]any{"format": "endpoint"},
			"pass": map[string]any{"format": "password"},
			"name": map[string]any{"type": "string"},
		}}
		So(EndpointFieldsFromSchema(schema), ShouldResemble, []string{"a", "z"})
		So(EndpointFieldsFromSchema(nil), ShouldBeEmpty)
	})
}

// settingsHost serves extension settings and records what capHost hands to OpenIO.
type settingsHost struct {
	recordingHost
	public  map[string]string
	secrets map[string]string
}

func (h *settingsHost) SettingsGet() (map[string]string, error) { return h.public, nil }
func (h *settingsHost) SettingSecrets([]string) (map[string]string, error) {
	return h.secrets, nil
}

// TestCapHostSettingSecretStaysOnDeclaredEndpoint pins that a request carrying a
// substituted secret header may not be redirected off the declared endpoint:
// net/http forwards custom headers such as X-API-Key across hosts on redirect.
func TestCapHostSettingSecretStaysOnDeclaredEndpoint(t *testing.T) {
	Convey("Given a reviewer whose settings declare an endpoint and a secret", t, func() {
		inner := &settingsHost{
			public:  map[string]string{"endpoint": "https://review.example.com"},
			secrets: map[string]string{"token": "s3cret"},
		}
		manifest := &Manifest{Name: "rev", Version: "1.0.0", SettingsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"endpoint": map[string]any{"type": "string", "format": "endpoint"},
				"token":    map[string]any{"type": "string", "format": "password"},
			},
		}}
		ch := NewCapabilityHost(inner, manifest, "/tmp/ext")

		Convey("a request with the secret only follows redirects within the endpoint", func() {
			_, err := ch.OpenIO(context.Background(), nil, IOOpenParams{
				Type: "http", URL: "https://review.example.com/v1",
				Headers: map[string]string{"X-API-Key": "{{setting.token}}"},
			})
			So(err, ShouldBeNil)
			So(inner.lastParams.Headers["X-API-Key"], ShouldEqual, "s3cret")
			guard := inner.lastParams.RedirectGuard
			So(guard, ShouldNotBeNil)
			So(guard(&url.URL{Scheme: "https", Host: "review.example.com", Path: "/v2"}), ShouldBeNil)
			So(guard(&url.URL{Scheme: "https", Host: "evil.example.com"}), ShouldNotBeNil)
		})
	})
}
