// pkg/extension/runtime_test.go
package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestLoadPlugin(t *testing.T) {
	Convey("LoadPlugin", t, func() {
		ctx := context.Background()
		host := newRecordedHost()

		Convey("should reject invalid WASM bytes", func() {
			manifest := &Manifest{Name: "test", Version: "1.0.0"}
			_, err := LoadPlugin(ctx, manifest, []byte("not wasm"), host, nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "compile wasm")
		})

		Convey("a module without the reactor exports fails loudly on first call", func() {
			// Minimal valid WASM module (magic + version, empty)
			minimalWasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
			manifest := &Manifest{Name: "test", Version: "1.0.0"}
			p, err := LoadPlugin(ctx, manifest, minimalWasm, host, nil)
			So(err, ShouldBeNil)
			So(p.Manifest().Name, ShouldEqual, "test")

			_, err = p.CallTool(ctx, "anything", json.RawMessage(`{}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "does not export opskat_call")

			So(p.Close(ctx), ShouldBeNil)
		})
	})
}

// newFixturePlugin loads the fixture extension with capability enforcement, the
// same way Manager.LoadExtension does for a shipped extension.
func newFixturePlugin(t *testing.T, host HostProvider, extDir string, opts ...PluginOption) *Plugin {
	t.Helper()
	manifest := fixtureManifest(t)
	capped := NewCapabilityHost(host, manifest, extDir)
	p, err := LoadPlugin(context.Background(), manifest, fixtureWasm(t), capped, nil, opts...)
	if err != nil {
		t.Fatalf("load fixture plugin: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Errorf("close plugin: %v", err)
		}
	})
	return p
}

func callTool(t *testing.T, p *Plugin, tool string, args any) map[string]any {
	t.Helper()
	raw, err := p.CallTool(context.Background(), tool, mustJSON(t, args))
	if err != nil {
		t.Fatalf("call tool %s: %v", tool, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s result %q: %v", tool, raw, err)
	}
	return out
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPluginCallsFixture(t *testing.T) {
	Convey("Given the fixture extension loaded as a reactor", t, func() {
		host := newRecordedHost()
		p := newFixturePlugin(t, host, t.TempDir())
		ctx := context.Background()

		Convey("a tool round-trips its arguments", func() {
			out := callTool(t, p, "echo", map[string]any{"n": 1})
			So(out["tool"], ShouldEqual, "echo")
			So(out["args"], ShouldResemble, map[string]any{"n": float64(1)})
		})

		Convey("a handler error surfaces as a Go error", func() {
			_, err := p.CallTool(ctx, "fail", json.RawMessage(`{}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "deliberate failure")
		})

		Convey("a result that merely looks like an error is returned as data", func() {
			out := callTool(t, p, "looks_like_error", map[string]any{})
			So(out["error"], ShouldEqual, "this is data, not a failure")
		})

		Convey("a guest panic fails one call without killing the instance", func() {
			_, err := p.CallTool(ctx, "panic", json.RawMessage(`{}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "boom")

			out := callTool(t, p, "echo", map[string]any{"after": "panic"})
			So(out["tool"], ShouldEqual, "echo")
		})

		Convey("an unknown tool is reported by name", func() {
			_, err := p.CallTool(ctx, "nope", json.RawMessage(`{}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "unknown tool: nope")
		})

		Convey("host capabilities are reachable from the guest", func() {
			host.configs[7] = json.RawMessage(`{"endpoint":"x"}`)
			out := callTool(t, p, "asset_config", map[string]any{"asset_id": 7})
			So(out["config"], ShouldResemble, map[string]any{"endpoint": "x"})

			out = callTool(t, p, "kv_roundtrip", map[string]any{"key": "k", "value": "v"})
			So(out["value"], ShouldEqual, "v")

			callTool(t, p, "log", map[string]any{"level": "warn", "msg": "hi"})
			So(host.logs, ShouldContain, "warn:hi")
		})

		Convey("policy and config validation go through the same entry point", func() {
			action, resource, err := p.CheckPolicy(ctx, "echo", json.RawMessage(`{}`))
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "read")
			So(resource, ShouldEqual, "fixture:echo")

			errs, err := p.ValidateConfig(ctx, json.RawMessage(`{}`))
			So(err, ShouldBeNil)
			So(errs, ShouldHaveLength, 1)
			So(errs[0].Field, ShouldEqual, "endpoint")

			errs, err = p.ValidateConfig(ctx, json.RawMessage(`{"endpoint":"e"}`))
			So(err, ShouldBeNil)
			So(errs, ShouldHaveLength, 0)
		})
	})
}

// TestPluginReusesAndRecyclesInstances observes the reactor model directly: a
// guest global survives between calls (so the Go runtime was not restarted) and
// resets when the instance is recycled.
func TestPluginReusesAndRecyclesInstances(t *testing.T) {
	Convey("Given a plugin limited to one instance recycled every 3 calls", t, func() {
		p := newFixturePlugin(t, newRecordedHost(), t.TempDir(),
			WithMaxInstances(1), WithMaxInstanceCalls(3))

		var got []float64
		for i := 0; i < 7; i++ {
			got = append(got, callTool(t, p, "seq", map[string]any{})["seq"].(float64))
		}

		Convey("the guest counter climbs within an instance and restarts after recycling", func() {
			So(got, ShouldResemble, []float64{1, 2, 3, 1, 2, 3, 1})
		})
	})
}

// TestPluginConcurrentCallsAreIsolated is the core guarantee the instance pool
// has to provide: several calls into one extension run at the same time, and
// neither sees the other's IO handles.
func TestPluginConcurrentCallsAreIsolated(t *testing.T) {
	Convey("Given files readable by the extension", t, func() {
		extDir := t.TempDir()
		const n = 4
		for i := 0; i < n; i++ {
			path := filepath.Join(extDir, fmt.Sprintf("f%d.txt", i))
			So(os.WriteFile(path, []byte(fmt.Sprintf("content-%d", i)), 0o600), ShouldBeNil)
		}
		host := newRecordedHost()
		p := newFixturePlugin(t, host, extDir, WithMaxInstances(n))

		Convey("concurrent reads each get their own file and their own handle table", func() {
			var wg sync.WaitGroup
			results := make([]map[string]any, n)
			errs := make([]error, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					raw, err := p.CallTool(context.Background(), "read_file", mustJSON(t,
						map[string]any{"path": filepath.Join(extDir, fmt.Sprintf("f%d.txt", i))}))
					if err != nil {
						errs[i] = err
						return
					}
					errs[i] = json.Unmarshal(raw, &results[i])
				}(i)
			}
			wg.Wait()

			for i := 0; i < n; i++ {
				So(errs[i], ShouldBeNil)
				So(results[i]["content"], ShouldEqual, fmt.Sprintf("content-%d", i))
				// Handle IDs are per invocation, so every call sees the first one.
				So(results[i]["handle"], ShouldEqual, float64(1))
			}
		})
	})
}

// TestPluginCallsRunConcurrently proves the pool actually overlaps calls rather
// than queueing them: the host blocks every call until all of them have arrived,
// so the test can only finish if they ran at the same time.
func TestPluginCallsRunConcurrently(t *testing.T) {
	Convey("Given a host that releases KVSet only once 3 calls are in flight", t, func() {
		const n = 3
		host := newRecordedHost()
		host.arrivals = make(chan struct{})
		host.release = make(chan struct{})
		p := newFixturePlugin(t, host, t.TempDir(), WithMaxInstances(n))

		done := make(chan error, n)
		for i := 0; i < n; i++ {
			go func(i int) {
				_, err := p.CallTool(context.Background(), "kv_roundtrip", mustJSON(t,
					map[string]any{"key": fmt.Sprintf("k%d", i), "value": "v"}))
				done <- err
			}(i)
		}

		Convey("all three reach the host before any of them returns", func() {
			for i := 0; i < n; i++ {
				select {
				case <-host.arrivals:
				case <-time.After(30 * time.Second):
					t.Fatalf("only %d of %d calls reached the host — the pool is serializing", i, n)
				}
			}
			close(host.release)
			for i := 0; i < n; i++ {
				So(<-done, ShouldBeNil)
			}
		})
	})
}

// TestPluginClosesHandlesLeftOpen pins invocation-scoped cleanup: a guest that
// forgets to close a handle must not hold the file for the plugin's lifetime.
func TestPluginClosesHandlesLeftOpen(t *testing.T) {
	Convey("Given a tool that opens a file and never closes it", t, func() {
		extDir := t.TempDir()
		path := filepath.Join(extDir, "leak.txt")
		So(os.WriteFile(path, []byte("data"), 0o600), ShouldBeNil)
		host := newRecordedHost()
		p := newFixturePlugin(t, host, extDir)

		out := callTool(t, p, "open_file", map[string]any{"path": path})
		So(out["handle"], ShouldEqual, float64(1))

		Convey("the runtime closes it when the invocation ends", func() {
			So(host.closedCount(), ShouldEqual, 1)
		})
	})
}

// TestPluginWriteErrorReachesGuest guards a defect the two-function ABI fixed:
// host_io_write used to return 0 on failure, which the guest could not tell
// apart from a legitimate zero-byte write, so a failed upload looked like a
// stalled one.
func TestPluginWriteErrorReachesGuest(t *testing.T) {
	Convey("Given a handle whose writes fail", t, func() {
		extDir := t.TempDir()
		p := newFixturePlugin(t, newRecordedHost(), extDir)

		Convey("the guest sees the error instead of a short write", func() {
			_, err := p.CallTool(context.Background(), "write_file", mustJSON(t, map[string]any{
				"path": filepath.Join(extDir, "broken.fail"), "content": "payload",
			}))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "simulated write failure")
		})

		Convey("a working handle still reports the byte count", func() {
			out := callTool(t, p, "write_file", map[string]any{
				"path": filepath.Join(extDir, "ok.txt"), "content": "payload",
			})
			So(out["written"], ShouldEqual, float64(7))
			data, err := os.ReadFile(filepath.Join(extDir, "ok.txt")) //nolint:gosec // path is the test's own temp dir
			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, "payload")
		})
	})
}

// TestPluginCapabilityEnforcement checks that capHost still sits between the
// guest and the host after the reactor rewrite.
func TestPluginCapabilityEnforcement(t *testing.T) {
	Convey("Given an extension whose fs.read is scoped to its own directory", t, func() {
		extDir := t.TempDir()
		inside := filepath.Join(extDir, "ok.txt")
		So(os.WriteFile(inside, []byte("allowed"), 0o600), ShouldBeNil)
		outsideDir := t.TempDir()
		outside := filepath.Join(outsideDir, "secret.txt")
		So(os.WriteFile(outside, []byte("denied"), 0o600), ShouldBeNil)

		host := newRecordedHost()
		p := newFixturePlugin(t, host, extDir)

		Convey("a read inside the sandbox succeeds", func() {
			out := callTool(t, p, "read_file", map[string]any{"path": inside})
			So(out["content"], ShouldEqual, "allowed")
		})

		Convey("a read outside it is rejected before the host opens anything", func() {
			_, err := p.CallTool(context.Background(), "read_file", mustJSON(t, map[string]any{"path": outside}))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "fs read denied")
			So(host.resources, ShouldBeEmpty)
		})
	})
}

// TestPluginActionCancellation covers the cooperative stop path and, just as
// importantly, that a canceled run leaves no residue for the next one.
func TestPluginActionCancellation(t *testing.T) {
	Convey("Given a long-running action streaming events", t, func() {
		host := newRecordedHost()
		p := newFixturePlugin(t, host, t.TempDir())

		type result struct {
			out map[string]any
			err error
		}
		results := make(chan result, 1)
		go func() {
			raw, err := p.CallAction(context.Background(), "stream", mustJSON(t,
				map[string]any{"count": 100000, "delay_ms": 1}))
			var out map[string]any
			if err == nil {
				err = json.Unmarshal(raw, &out)
			}
			results <- result{out: out, err: err}
		}()

		Convey("canceling it makes the guest return early", func() {
			deadline := time.After(30 * time.Second)
			for len(host.snapshotEvents()) == 0 {
				select {
				case <-deadline:
					t.Fatal("action never emitted an event")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			p.CancelActiveAction()

			var got result
			select {
			case got = <-results:
			case <-time.After(30 * time.Second):
				t.Fatal("action did not stop after cancellation")
			}
			So(got.err, ShouldBeNil)
			So(got.out["stopped"], ShouldEqual, true)
			So(got.out["sent"], ShouldBeLessThan, float64(100000))
			So(len(host.snapshotEvents()), ShouldBeGreaterThan, 0)

			Convey("and the next action starts with a clean cancellation flag", func() {
				raw, err := p.CallAction(context.Background(), "should_stop", json.RawMessage(`{}`))
				So(err, ShouldBeNil)
				var out map[string]any
				So(json.Unmarshal(raw, &out), ShouldBeNil)
				So(out["stopped"], ShouldEqual, false)
			})
		})
	})
}

// TestToolTimeoutDoesNotBindActions is the reason tool and action calls stopped
// sharing one hard-coded deadline: an action is expected to outlive it.
func TestToolTimeoutDoesNotBindActions(t *testing.T) {
	Convey("Given a plugin whose tool timeout is 150ms", t, func() {
		p := newFixturePlugin(t, newRecordedHost(), t.TempDir(), WithToolTimeout(150*time.Millisecond))

		Convey("a tool that runs longer is interrupted", func() {
			_, err := p.CallTool(context.Background(), "spin", mustJSON(t, map[string]any{"ms": 3000}))
			So(err, ShouldNotBeNil)
		})

		Convey("the same work as an action is allowed to finish", func() {
			raw, err := p.CallAction(context.Background(), "spin", mustJSON(t, map[string]any{"ms": 400}))
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(raw, &out), ShouldBeNil)
			So(out["iterations"], ShouldBeGreaterThan, float64(0))
		})

		Convey("an action still obeys the deadline its caller set", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			_, err := p.CallAction(ctx, "spin", mustJSON(t, map[string]any{"ms": 3000}))
			So(err, ShouldNotBeNil)
		})
	})
}

// BenchmarkPluginCallTool and BenchmarkPluginCallToolColdInstance are a matched
// pair: the same tool on the same module, once on a pooled reactor instance and
// once with the instance thrown away after every call — which is what the old
// "WASI command, re-run _start per call" model did. Their ratio is the reason
// this rewrite exists.
func BenchmarkPluginCallTool(b *testing.B) {
	benchmarkFixtureTool(b, WithMaxInstanceCalls(1<<30))
}

func BenchmarkPluginCallToolColdInstance(b *testing.B) {
	benchmarkFixtureTool(b, WithMaxInstanceCalls(1))
}

func benchmarkFixtureTool(b *testing.B, opts ...PluginOption) {
	b.Helper()
	t := &testing.T{}
	wasm := fixtureWasm(t)
	if t.Failed() {
		b.Fatal("fixture build failed")
	}
	manifest := &Manifest{Name: "fixture-ext", Version: "1.0.0"}
	p, err := LoadPlugin(context.Background(), manifest, wasm, newRecordedHost(), nil, opts...)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = p.Close(context.Background()) }()

	ctx := context.Background()
	args := json.RawMessage(`{}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.CallTool(ctx, "seq", args); err != nil {
			b.Fatal(err)
		}
	}
}
