// Command fixture-ext is the minimal WASM extension used by pkg/extension's
// end-to-end tests. It is built on demand by buildFixtureWASM (see
// fixture_test.go), never shipped, and deliberately exercises one host
// capability per handler so a broken host call fails one assertion instead of
// the whole suite.
//
// Handlers register from init(): the guest is a WASI reactor, so the host runs
// _initialize and func main() is never called.
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

func init() {
	opskat.RegisterTool("echo", func(ctx *opskat.ToolContext) (any, error) {
		var args map[string]any
		if err := json.Unmarshal(ctx.Args, &args); err != nil {
			return nil, err
		}
		return map[string]any{"tool": ctx.Tool, "args": args}, nil
	})

	// seq counts calls in a guest global. Because a reactor instance survives
	// between calls the counter keeps climbing, so the host-side test can see
	// both that instances are reused and when one is recycled.
	opskat.RegisterTool("seq", func(ctx *opskat.ToolContext) (any, error) {
		callSeq++
		return map[string]any{"seq": callSeq}, nil
	})

	opskat.RegisterTool("fail", func(ctx *opskat.ToolContext) (any, error) {
		return nil, fmt.Errorf("deliberate failure")
	})

	// panics must be contained: a reactor instance is reused by later calls.
	opskat.RegisterTool("panic", func(ctx *opskat.ToolContext) (any, error) {
		panic("boom")
	})

	// looks_like_error returns a payload that the old ABI would have mistaken
	// for a failure because it sniffed for a top-level "error" key.
	opskat.RegisterTool("looks_like_error", func(ctx *opskat.ToolContext) (any, error) {
		return map[string]any{"error": "this is data, not a failure"}, nil
	})

	opskat.RegisterTool("read_file", func(ctx *opskat.ToolContext) (any, error) {
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(ctx.Args, &args); err != nil {
			return nil, err
		}
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
	})

	// open_file leaks a handle on purpose: the test asserts the host reclaims it
	// when the invocation ends instead of keeping it for the plugin's lifetime.
	opskat.RegisterTool("open_file", func(ctx *opskat.ToolContext) (any, error) {
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(ctx.Args, &args); err != nil {
			return nil, err
		}
		h, err := opskat.IOOpen("file", map[string]any{"path": args.Path, "mode": "read"})
		if err != nil {
			return nil, err
		}
		return map[string]any{"handle": h.ID()}, nil
	})

	opskat.RegisterTool("write_file", func(ctx *opskat.ToolContext) (any, error) {
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(ctx.Args, &args); err != nil {
			return nil, err
		}
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
	})

	opskat.RegisterTool("kv_roundtrip", func(ctx *opskat.ToolContext) (any, error) {
		var args struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(ctx.Args, &args); err != nil {
			return nil, err
		}
		if err := opskat.KVSet(args.Key, []byte(args.Value)); err != nil {
			return nil, err
		}
		got, err := opskat.KVGet(args.Key)
		if err != nil {
			return nil, err
		}
		return map[string]any{"value": string(got)}, nil
	})

	opskat.RegisterTool("asset_config", func(ctx *opskat.ToolContext) (any, error) {
		var args struct {
			AssetID int64 `json:"asset_id"`
		}
		if err := json.Unmarshal(ctx.Args, &args); err != nil {
			return nil, err
		}
		cfg, err := opskat.GetAssetConfig(args.AssetID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"config": json.RawMessage(cfg)}, nil
	})

	opskat.RegisterTool("log", func(ctx *opskat.ToolContext) (any, error) {
		var args struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		if err := json.Unmarshal(ctx.Args, &args); err != nil {
			return nil, err
		}
		opskat.Log(args.Level, args.Msg)
		return map[string]any{"ok": true}, nil
	})

	// spin busy-loops for the requested duration without ever yielding to a WASI
	// clock, so a host-side deadline has to interrupt the guest to stop it.
	opskat.RegisterTool("spin", spin)
	opskat.RegisterAction("spin", func(ctx *opskat.ActionContext) (any, error) {
		return spin(&opskat.ToolContext{Tool: ctx.Action, Args: ctx.Args})
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

	opskat.RegisterPolicy(func(tool string, args json.RawMessage) (string, string) {
		return "read", "fixture:" + tool
	})

	opskat.RegisterConfigValidator(func(config json.RawMessage) []opskat.ValidationError {
		var cfg struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.Unmarshal(config, &cfg); err != nil {
			return []opskat.ValidationError{{Field: "", Message: err.Error()}}
		}
		if cfg.Endpoint == "" {
			return []opskat.ValidationError{{Field: "endpoint", Message: "endpoint is required"}}
		}
		return nil
	})
}

func spin(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		MS int `json:"ms"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(time.Duration(args.MS) * time.Millisecond)
	iterations := 0
	for time.Now().Before(deadline) {
		iterations++
	}
	return map[string]any{"iterations": iterations}, nil
}
