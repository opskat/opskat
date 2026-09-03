// Command fixture-ext is the minimal WASM extension used by pkg/extension's
// end-to-end tests. It is built on demand by fixtureWasm (see fixture_test.go),
// never shipped, and deliberately exercises one host capability per handler so a
// broken host call fails one assertion instead of the whole suite.
//
// Handlers register from init(): the guest is a WASI reactor, so the host runs
// _initialize and func main() is never called. The registrations below are also
// the extension's entire functional declaration — manifest.json carries only the
// capability grants, and the host reads everything else back through describe().
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	opskat "github.com/opskat/opskat/pkg/extsdk"
)

func main() {}

var callSeq int

type noArgs struct{}

type echoArgs struct {
	Msg string `json:"msg,omitempty" desc:"Message to echo back"`
	N   int    `json:"n,omitempty" desc:"A number to echo back"`
}

type pathArgs struct {
	Path string `json:"path" desc:"Path to read"`
}

type writeArgs struct {
	Path    string `json:"path" desc:"Path to write"`
	Content string `json:"content" desc:"Content to write"`
}

type kvArgs struct {
	Key   string `json:"key" desc:"Key to store under"`
	Value string `json:"value" desc:"Value to store"`
}

type assetConfigArgs struct {
	AssetID int64 `json:"asset_id" desc:"Asset whose config to read"`
}

type logArgs struct {
	Level string `json:"level" desc:"Log level"`
	Msg   string `json:"msg" desc:"Message to log"`
}

type spinArgs struct {
	MS int `json:"ms" desc:"How long to busy-loop, in milliseconds"`
}

type fixtureConfig struct {
	Endpoint string `json:"endpoint" title:"Endpoint"`
}

func init() {
	opskat.Extension(opskat.Meta{
		DisplayName: "Fixture Extension",
		Description: "Minimal extension used by pkg/extension end-to-end tests",
		PolicyType:  "fixture",
	})
	opskat.AssetType[fixtureConfig]("fixture").Name("Fixture")
	opskat.PolicyGroup("ext:fixture:read").Name("Read").Description("Read-only").
		Allow("read").Default()

	opskat.Tool("echo", func(ctx *opskat.ToolContext, args echoArgs) (any, error) {
		return map[string]any{"tool": ctx.Tool, "args": args}, nil
	}).Policy("read").Doc("Echo the arguments back").
		Resource(func(echoArgs) string { return "fixture:echo" })

	// seq counts calls in a guest global. Because a reactor instance survives
	// between calls the counter keeps climbing, so the host-side test can see
	// both that instances are reused and when one is recycled.
	opskat.Tool("seq", func(*opskat.ToolContext, noArgs) (any, error) {
		callSeq++
		return map[string]any{"seq": callSeq}, nil
	}).Policy("read")

	opskat.Tool("fail", func(*opskat.ToolContext, noArgs) (any, error) {
		return nil, fmt.Errorf("deliberate failure")
	}).Policy("read")

	// panics must be contained: a reactor instance is reused by later calls.
	opskat.Tool("panic", func(*opskat.ToolContext, noArgs) (any, error) {
		panic("boom")
	}).Policy("read")

	// looks_like_error returns a payload that the old ABI would have mistaken
	// for a failure because it sniffed for a top-level "error" key.
	opskat.Tool("looks_like_error", func(*opskat.ToolContext, noArgs) (any, error) {
		return map[string]any{"error": "this is data, not a failure"}, nil
	}).Policy("read")

	opskat.Tool("read_file", func(_ *opskat.ToolContext, args pathArgs) (any, error) {
		h, err := opskat.IOOpen("file", map[string]any{"path": args.Path, "mode": "read"})
		if err != nil {
			return nil, err
		}
		defer h.Close()
		data, err := io.ReadAll(h)
		if err != nil {
			return nil, err
		}
		return map[string]any{"handle": h.ID(), "content": string(data)}, nil
	}).Policy("read")

	// open_file leaks a handle on purpose: the test asserts the host reclaims it
	// when the invocation ends instead of keeping it for the plugin's lifetime.
	opskat.Tool("open_file", func(_ *opskat.ToolContext, args pathArgs) (any, error) {
		h, err := opskat.IOOpen("file", map[string]any{"path": args.Path, "mode": "read"})
		if err != nil {
			return nil, err
		}
		return map[string]any{"handle": h.ID()}, nil
	}).Policy("read")

	opskat.Tool("write_file", func(_ *opskat.ToolContext, args writeArgs) (any, error) {
		h, err := opskat.IOOpen("file", map[string]any{"path": args.Path, "mode": "write"})
		if err != nil {
			return nil, err
		}
		defer h.Close()
		n, err := h.Write([]byte(args.Content))
		if err != nil {
			return nil, err
		}
		return map[string]any{"written": n}, nil
	}).Policy("write")

	opskat.Tool("kv_roundtrip", func(_ *opskat.ToolContext, args kvArgs) (any, error) {
		if err := opskat.KVSet(args.Key, []byte(args.Value)); err != nil {
			return nil, err
		}
		got, err := opskat.KVGet(args.Key)
		if err != nil {
			return nil, err
		}
		return map[string]any{"value": string(got)}, nil
	}).Policy("write")

	opskat.Tool("asset_config", func(_ *opskat.ToolContext, args assetConfigArgs) (any, error) {
		cfg, err := opskat.GetAssetConfig(args.AssetID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"config": json.RawMessage(cfg)}, nil
	}).Policy("read")

	opskat.Tool("log", func(_ *opskat.ToolContext, args logArgs) (any, error) {
		opskat.Log(args.Level, args.Msg)
		return map[string]any{"ok": true}, nil
	}).Policy("read")

	// spin busy-loops for the requested duration without ever yielding to a WASI
	// clock, so a host-side deadline has to interrupt the guest to stop it.
	opskat.Tool("spin", func(_ *opskat.ToolContext, args spinArgs) (any, error) {
		return spin(args.MS), nil
	}).Policy("read")
	opskat.RegisterAction("spin", func(ctx *opskat.ActionContext) (any, error) {
		var args spinArgs
		if err := json.Unmarshal(ctx.Args, &args); err != nil {
			return nil, err
		}
		return spin(args.MS), nil
	})

	// stream emits events then returns; it polls ShouldStop so the test can
	// observe cooperative cancellation.
	opskat.RegisterAction("stream", func(ctx *opskat.ActionContext) (any, error) {
		var args struct {
			Count int `json:"count"`
			Delay int `json:"delay_ms"`
		}
		if err := json.Unmarshal(ctx.Args, &args); err != nil {
			return nil, err
		}
		sent := 0
		for i := 0; i < args.Count; i++ {
			if ctx.ShouldStop() {
				return map[string]any{"sent": sent, "stopped": true}, nil
			}
			if err := ctx.Events.Send("progress", map[string]any{"i": i}); err != nil {
				return nil, err
			}
			sent++
			if args.Delay > 0 {
				time.Sleep(time.Duration(args.Delay) * time.Millisecond)
			}
		}
		return map[string]any{"sent": sent, "stopped": false}, nil
	})

	// should_stop reports the cancellation flag once, without looping. A fresh
	// invocation must never inherit a previous invocation's cancellation.
	opskat.RegisterAction("should_stop", func(ctx *opskat.ActionContext) (any, error) {
		return map[string]any{"stopped": ctx.ShouldStop()}, nil
	})

	opskat.RegisterConfigValidator(func(config json.RawMessage) []opskat.ValidationError {
		var cfg fixtureConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return []opskat.ValidationError{{Field: "", Message: err.Error()}}
		}
		if cfg.Endpoint == "" {
			return []opskat.ValidationError{{Field: "endpoint", Message: "endpoint is required"}}
		}
		return nil
	})
}

func spin(ms int) map[string]any {
	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	iterations := 0
	for time.Now().Before(deadline) {
		iterations++
	}
	return map[string]any{"iterations": iterations}
}
