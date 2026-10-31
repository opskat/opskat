// Command fixture-settings is the WASM extension tests use to observe an
// extension's own settings end to end: it declares settings — a service URL
// (format:"endpoint"), an API key (format:"password") injected as a bearer token,
// and a model — and two host-only tools that run as a call without an asset does:
// call_service reaches the network through host IO, settings reads what the host
// lets the extension read of them. Built on demand by
// extensiontest.InstallSettingsFixture, never shipped.
package main

import (
	"io"

	opskat "github.com/opskat/opskat/pkg/extsdk"
)

func main() {}

type settings struct {
	BaseURL string `json:"baseURL" title:"Service URL" format:"endpoint"`
	APIKey  string `json:"apiKey" title:"API key" format:"password"`
	Model   string `json:"model,omitempty" title:"Model"`
}

type urlArgs struct {
	URL string `json:"url" desc:"URL to GET"`
}

func init() {
	opskat.Extension(opskat.Meta{
		DisplayName: "Settings Fixture",
		Description: "Extension settings fixture used by host tests",
	})
	opskat.Settings[settings]().Auth(opskat.Auth{Groups: []opskat.AuthGroup{{
		Bindings: []opskat.AuthBinding{{In: "header", Name: "Authorization", Value: "Bearer {{apiKey}}"}},
	}}})

	opskat.Tool("call_service", func(_ *opskat.ToolContext, args urlArgs) (any, error) {
		h, err := opskat.IOOpen("http", map[string]any{"method": "GET", "url": args.URL})
		if err != nil {
			return nil, err
		}
		defer h.Close()
		meta, err := h.Flush()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(h)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": meta.Status, "body": string(body)}, nil
	}).HostOnly()

	opskat.Tool("settings", func(ctx *opskat.ToolContext, _ struct{}) (any, error) {
		return ctx.Settings()
	}).HostOnly()
}
