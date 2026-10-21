// pkg/extension/runtime_test.go
package extension

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	cagologger "github.com/cago-frame/cago/pkg/logger"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension/driverwire"
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

			_, err = p.CallTool(ctx, "anything", json.RawMessage(`{}`), nil)
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
	return callToolOn(t, p, nil, tool, args)
}

// callToolOn is callTool for a call the host scoped to an asset, which is what
// every exec-driven call is.
func callToolOn(t *testing.T, p *Plugin, asset *AssetRef, tool string, args any) map[string]any {
	t.Helper()
	raw, err := p.CallTool(context.Background(), tool, mustJSON(t, args), asset)
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
			_, err := p.CallTool(ctx, "fail", json.RawMessage(`{}`), nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "deliberate failure")
		})

		Convey("a result that merely looks like an error is returned as data", func() {
			out := callTool(t, p, "looks_like_error", map[string]any{})
			So(out["error"], ShouldEqual, "this is data, not a failure")
		})

		Convey("a guest panic fails one call without killing the instance", func() {
			_, err := p.CallTool(ctx, "panic", json.RawMessage(`{}`), nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "boom")

			out := callTool(t, p, "echo", map[string]any{"after": "panic"})
			So(out["tool"], ShouldEqual, "echo")
		})

		Convey("an unknown tool is reported by name", func() {
			_, err := p.CallTool(ctx, "nope", json.RawMessage(`{}`), nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "unknown tool: nope")
		})

		Convey("host capabilities are reachable from the guest", func() {
			host.configs[7] = json.RawMessage(`{"endpoint":"x"}`)
			out := callToolOn(t, p, &AssetRef{ID: 7, Name: "notes", Type: "fixture"}, "asset_config", map[string]any{})
			So(out["config"], ShouldResemble, map[string]any{"endpoint": "x"})
			// The asset came from the call envelope, not from the arguments: the
			// guest was told which asset it is running against.
			So(out["asset"], ShouldResemble, map[string]any{"id": float64(7), "name": "notes", "type": "fixture"})

			out = callTool(t, p, "kv_roundtrip", map[string]any{"key": "k", "value": "v"})
			So(out["value"], ShouldEqual, "v")

			callTool(t, p, "log", map[string]any{"level": "warn", "msg": "hi"})
			So(host.logs, ShouldContain, "warn:hi")
		})

		Convey("a call the host did not scope to an asset says so", func() {
			_, err := p.CallTool(ctx, "asset_config", json.RawMessage(`{}`), nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not scoped to an asset")
		})

		Convey("policy and config validation go through the same entry point", func() {
			action, resources, err := p.CheckPolicy(ctx, "echo", json.RawMessage(`{}`))
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "read")
			So(resources, ShouldResemble, []string{"fixture:echo"})

			errs, err := p.ValidateConfig(ctx, json.RawMessage(`{}`))
			So(err, ShouldBeNil)
			So(errs, ShouldHaveLength, 1)
			So(errs[0].Field, ShouldEqual, "endpoint")

			errs, err = p.ValidateConfig(ctx, json.RawMessage(`{"endpoint":"e"}`))
			So(err, ShouldBeNil)
			So(errs, ShouldHaveLength, 0)
		})

		// The host judges every resource as a glob. A 2.0/2.1 single-resource reply
		// names one literal resource, so every glob character in it is quoted; a
		// PolicyResources reply keeps '*' / '?' as wildcards and quotes the rest.
		Convey("check_policy's resources cross into the host as globs", func() {
			action, resources, err := p.CheckPolicy(ctx, "classify_one", json.RawMessage(`{"resource":"prod-*?[1]\\x"}`))
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "read")
			So(resources, ShouldResemble, []string{`prod-\*\?\[1]\\x`})

			_, resources, err = p.CheckPolicy(ctx, "classify_one", json.RawMessage(`{}`))
			So(err, ShouldBeNil)
			So(resources, ShouldBeEmpty)

			action, resources, err = p.CheckPolicy(ctx, "classify_many",
				json.RawMessage(`{"resources":["a","logs-*","b?","we[ird]\\"]}`))
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "write")
			So(resources, ShouldResemble, []string{"a", "logs-*", "b?", `we\[ird\]\\`})

			_, resources, err = p.CheckPolicy(ctx, "classify_many", json.RawMessage(`{}`))
			So(err, ShouldBeNil)
			So(resources, ShouldBeEmpty)
		})

		// RejectArgs crosses the boundary as its own answer: the host can tell the
		// tool refusing the arguments from the guest failing to classify them.
		Convey("a tool's argument rejection crosses into the host as an ArgsRejectedError", func() {
			_, _, err := p.CheckPolicy(ctx, "reject_host", json.RawMessage(`{"resource":"http://evil/x"}`))
			var rejected *ArgsRejectedError
			So(errors.As(err, &rejected), ShouldBeTrue)
			So(rejected.Reason, ShouldEqual, `resource "http://evil/x" must not name a host`)

			action, resources, err := p.CheckPolicy(ctx, "reject_host", json.RawMessage(`{"resource":"a"}`))
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "write")
			So(resources, ShouldResemble, []string{"a"})

			_, _, err = p.CheckPolicy(ctx, "reject_host", json.RawMessage(`{"resource":7}`))
			So(err, ShouldNotBeNil)
			So(errors.As(err, &rejected), ShouldBeFalse)

			// execute_tool holds the same line without any policy check before it.
			_, err = p.CallTool(ctx, "reject_host", json.RawMessage(`{"resource":"http://evil/x"}`), nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "must not name a host")
		})

		Convey("validate_config never sees the host's reserved connection key", func() {
			errs, err := p.ValidateConfig(ctx, json.RawMessage(
				`{"endpoint":"e","`+HostConnectionConfigKey+`":{"tls":{"caCert":"ca-pem"}}}`,
			))
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
						map[string]any{"path": filepath.Join(extDir, fmt.Sprintf("f%d.txt", i))}), nil)
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
					map[string]any{"key": fmt.Sprintf("k%d", i), "value": "v"}), nil)
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
			}), nil)
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
			_, err := p.CallTool(context.Background(), "read_file", mustJSON(t, map[string]any{"path": outside}), nil)
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

		results := make(chan actionResult, 1)
		go func() { results <- runStream(t, p, "upload-1", 100000, 1) }()

		Convey("canceling it makes the guest return early", func() {
			waitForEvents(t, host, "upload-1", 1)
			So(p.CancelAction("upload-1"), ShouldBeTrue)

			var got actionResult
			select {
			case got = <-results:
			case <-time.After(30 * time.Second):
				t.Fatal("action did not stop after cancellation")
			}
			So(got.err, ShouldBeNil)
			So(got.out["stopped"], ShouldEqual, true)
			So(got.out["sent"], ShouldBeLessThan, float64(100000))
			So(len(host.eventsFor("upload-1")), ShouldBeGreaterThan, 0)

			Convey("and the next action starts with a clean cancellation flag", func() {
				raw, err := p.CallAction(context.Background(), "upload-2", "should_stop", json.RawMessage(`{}`), nil)
				So(err, ShouldBeNil)
				var out map[string]any
				So(json.Unmarshal(raw, &out), ShouldBeNil)
				So(out["stopped"], ShouldEqual, false)
			})

			Convey("and canceling an invocation that is not running says so", func() {
				So(p.CancelAction("upload-1"), ShouldBeFalse)
			})
		})
	})
}

// TestPluginCancelsOnlyTheNamedAction is the regression guard for the bug the
// reactor instance pool introduced. While Plugin.mu serialized calls, "cancel
// this extension" could only ever mean the single action in flight; once several
// run at once the same request stopped every one of them — cancel one file in an
// OSS multi-file upload and the whole queue died. The same root cause showed up
// a second way in the event stream, so both are asserted here.
func TestPluginCancelsOnlyTheNamedAction(t *testing.T) {
	Convey("Given two actions of one extension running at the same time", t, func() {
		host := newRecordedHost()
		p := newFixturePlugin(t, host, t.TempDir())

		doomed := make(chan actionResult, 1)
		survivor := make(chan actionResult, 1)
		go func() { doomed <- runStream(t, p, "upload-doomed", 100000, 1) }()
		go func() { survivor <- runStream(t, p, "upload-survivor", 100000, 1) }()

		waitForEvents(t, host, "upload-doomed", 1)
		waitForEvents(t, host, "upload-survivor", 1)

		Convey("canceling one stops it and leaves the other running", func() {
			So(p.CancelAction("upload-doomed"), ShouldBeTrue)

			var stopped actionResult
			select {
			case stopped = <-doomed:
			case <-time.After(30 * time.Second):
				t.Fatal("the canceled action never returned")
			}
			So(stopped.err, ShouldBeNil)
			So(stopped.out["stopped"], ShouldEqual, true)

			// The survivor is still making progress after the other one is gone.
			before := len(host.eventsFor("upload-survivor"))
			waitForEvents(t, host, "upload-survivor", before+3)
			select {
			case r := <-survivor:
				t.Fatalf("the untouched action stopped too: %+v (err=%v)", r.out, r.err)
			default:
			}

			Convey("and its events keep flowing under its own invocation id", func() {
				// Nothing new may appear under the canceled id after it returned.
				doomedCount := len(host.eventsFor("upload-doomed"))
				waitForEvents(t, host, "upload-survivor", len(host.eventsFor("upload-survivor"))+3)
				So(len(host.eventsFor("upload-doomed")), ShouldEqual, doomedCount)

				So(p.CancelAction("upload-survivor"), ShouldBeTrue)
				select {
				case r := <-survivor:
					So(r.err, ShouldBeNil)
					So(r.out["stopped"], ShouldEqual, true)
				case <-time.After(30 * time.Second):
					t.Fatal("the second action did not stop after cancellation")
				}
			})
		})
	})
}

type actionResult struct {
	out map[string]any
	err error
}

// runStream drives the fixture's "stream" action under an explicit invocation id.
func runStream(t *testing.T, p *Plugin, invocationID string, count, delayMS int) actionResult {
	t.Helper()
	raw, err := p.CallAction(context.Background(), invocationID, "stream",
		mustJSON(t, map[string]any{"count": count, "delay_ms": delayMS}), nil)
	var out map[string]any
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return actionResult{out: out, err: err}
}

// waitForEvents blocks until one invocation has emitted at least n events.
func waitForEvents(t *testing.T, host *recordedHost, invocationID string, n int) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for len(host.eventsFor(invocationID)) < n {
		select {
		case <-deadline:
			t.Fatalf("invocation %s emitted %d events, wanted %d",
				invocationID, len(host.eventsFor(invocationID)), n)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// TestToolTimeoutDoesNotBindActions is the reason tool and action calls stopped
// sharing one hard-coded deadline: an action is expected to outlive it.
func TestToolTimeoutDoesNotBindActions(t *testing.T) {
	Convey("Given a plugin whose tool timeout is 150ms", t, func() {
		p := newFixturePlugin(t, newRecordedHost(), t.TempDir(), WithToolTimeout(150*time.Millisecond))

		Convey("a tool that runs longer is interrupted", func() {
			_, err := p.CallTool(context.Background(), "spin", mustJSON(t, map[string]any{"ms": 3000}), nil)
			So(err, ShouldNotBeNil)
		})

		Convey("the same work as an action is allowed to finish", func() {
			raw, err := p.CallAction(context.Background(), "slow-1", "spin", mustJSON(t, map[string]any{"ms": 400}), nil)
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(raw, &out), ShouldBeNil)
			So(out["iterations"], ShouldBeGreaterThan, float64(0))
		})

		Convey("an action still obeys the deadline its caller set", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			_, err := p.CallAction(ctx, "slow-2", "spin", mustJSON(t, map[string]any{"ms": 3000}), nil)
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
		if _, err := p.CallTool(ctx, "seq", args, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// --- database driver sessions ---------------------------------------------
//
// The database driver fixture (testdata/fixture-dbdriver) logs in to, and
// announces every statement to, whatever its reach path leads to. FixtureDBServer
// is that far end. It is exported so the external driver tests
// (driver_session_test.go) use the same one.

var (
	dbDriverFixtureOnce sync.Once
	dbDriverFixtureWASM []byte
	dbDriverFixtureErr  error
	// dbDriverCompiled spares each test compiling the module again, which takes
	// tens of seconds under -race.
	dbDriverCompiled = wazero.NewCompilationCache()
)

// dbDriverFixtureWasm compiles testdata/fixture-dbdriver, once per test binary.
func dbDriverFixtureWasm(t *testing.T) []byte {
	t.Helper()
	dbDriverFixtureOnce.Do(func() {
		dir, err := os.MkdirTemp("", "opskat-fixture-dbdriver-*")
		if err != nil {
			dbDriverFixtureErr = err
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		out := filepath.Join(dir, "main.wasm")
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "./testdata/fixture-dbdriver") //nolint:gosec // fixed argv, only the temp output path varies
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if output, err := cmd.CombinedOutput(); err != nil {
			dbDriverFixtureErr = fmt.Errorf("build database driver fixture: %w\n%s", err, output)
			return
		}
		dbDriverFixtureWASM, dbDriverFixtureErr = os.ReadFile(out) //nolint:gosec // path built above
	})
	if dbDriverFixtureErr != nil {
		t.Fatal(dbDriverFixtureErr)
	}
	return dbDriverFixtureWASM
}

// newDBDriverPlugin loads the database driver fixture the way Manager.LoadExtension does.
func newDBDriverPlugin(t *testing.T, opts ...PluginOption) *Plugin {
	t.Helper()
	data, err := os.ReadFile("testdata/fixture-dbdriver/manifest.json")
	require.NoError(t, err)
	manifest, err := ParseManifest(data)
	require.NoError(t, err)
	host := NewCapabilityHost(NewDefaultHostProvider(DefaultHostConfig{}), manifest, t.TempDir())
	p, err := LoadPlugin(context.Background(), manifest, dbDriverFixtureWasm(t), host, dbDriverCompiled, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, p.Close(context.Background())) })
	return p
}

// openFixtureSession opens a fixturedb session whose reach path leads to srv.
func openFixtureSession(t *testing.T, p *Plugin, srv *FixtureDBServer) dbdriver.Session {
	t.Helper()
	s, err := p.OpenDriverSession(context.Background(), "fixturedb", dbdriver.OpenRequest{
		Config:    &dbdriver.Config{Driver: "fixturedb", Host: "db.internal", Port: 7357, Username: "alice", Database: "app"},
		Password:  "s3cret",
		Transport: dbdriver.Transport{Dial: srv.Dial},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// FixtureStatement is one line the fixture sent after logging in, and the
// connection (numbered from 1 in dial order) it came on.
type FixtureStatement struct {
	Conn int
	Line string
}

// HeldStatement is a statement whose answer the server holds back until the test
// releases it, keeping the driver call blocked in a host read.
type HeldStatement struct {
	FixtureStatement
	release chan struct{}
	once    sync.Once
}

// Release lets the server answer the statement.
func (h *HeldStatement) Release() { h.once.Do(func() { close(h.release) }) }

// FixtureDBServer answers the fixture driver's logins and statement announcements
// and records them.
type FixtureDBServer struct {
	// Refuse, when set, is the error every login gets.
	Refuse string
	// Hold, when set, is a line (e.g. "QUERY SHOW DATABASES") whose answer is
	// held: each one is handed to the test (WaitHeld) and answered once released.
	Hold string

	mu         sync.Mutex
	addrs      []string
	passwords  []string
	statements []FixtureStatement
	conns      int
	open       int
	held       chan *HeldStatement
	done       chan struct{}
}

// NewFixtureDBServer starts a server; whatever it still holds is dropped when the
// test ends.
func NewFixtureDBServer(t testing.TB) *FixtureDBServer {
	s := &FixtureDBServer{held: make(chan *HeldStatement, 16), done: make(chan struct{})}
	t.Cleanup(func() { close(s.done) })
	return s
}

// Dial is the reach path's dial.
func (s *FixtureDBServer) Dial(_ context.Context, _, addr string) (net.Conn, error) {
	s.mu.Lock()
	s.addrs = append(s.addrs, addr)
	s.conns++
	s.open++
	id := s.conns
	s.mu.Unlock()
	client, server := net.Pipe()
	go s.serve(id, server)
	return client, nil
}

func (s *FixtureDBServer) serve(id int, c net.Conn) {
	defer func() {
		_ = c.Close()
		s.mu.Lock()
		s.open--
		s.mu.Unlock()
	}()
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		reply := "OK\n"
		s.mu.Lock()
		if login, ok := strings.CutPrefix(line, "LOGIN "); ok {
			if s.Refuse != "" {
				reply = "ERR " + s.Refuse + "\n"
			} else {
				_, password, _ := strings.Cut(login, " ")
				s.passwords = append(s.passwords, password)
			}
		} else {
			s.statements = append(s.statements, FixtureStatement{Conn: id, Line: line})
		}
		hold := s.Hold != "" && line == s.Hold
		s.mu.Unlock()
		if hold {
			h := &HeldStatement{FixtureStatement: FixtureStatement{Conn: id, Line: line}, release: make(chan struct{})}
			s.held <- h
			select {
			case <-h.release:
			case <-s.done:
				return
			}
		}
		if _, err := c.Write([]byte(reply)); err != nil {
			return
		}
	}
}

// WaitHeld returns the next held statement.
func (s *FixtureDBServer) WaitHeld(t testing.TB) *HeldStatement {
	t.Helper()
	select {
	case h := <-s.held:
		return h
	case <-time.After(callBound):
		t.Fatalf("no held statement reached the database within %s", callBound)
		return nil
	}
}

// Logins are the passwords of the successful logins, in order.
func (s *FixtureDBServer) Logins() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.passwords...)
}

// Dialed are the addresses dialed, in order.
func (s *FixtureDBServer) Dialed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.addrs...)
}

// Seen are the statement lines received, in order.
func (s *FixtureDBServer) Seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	lines := make([]string, len(s.statements))
	for i, st := range s.statements {
		lines[i] = st.Line
	}
	return lines
}

// SeenOn are the statements received, with the connection each came on.
func (s *FixtureDBServer) SeenOn() []FixtureStatement {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]FixtureStatement(nil), s.statements...)
}

// OpenConns is how many connections are open. One whose statement is held counts
// until the test ends: the server is not reading it.
func (s *FixtureDBServer) OpenConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// driverOp runs op in the background; the channel yields its error.
func driverOp(op func() error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- op() }()
	return done
}

// waitOp waits for an operation to come back, within bound.
func waitOp(t *testing.T, done <-chan error, bound time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(bound):
		t.Fatalf("the driver call did not come back within %s", bound)
		return nil
	}
}

// assertPending checks an operation has not come back.
func assertPending(t *testing.T, done <-chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s came back (err=%v) while it should still be running", what, err)
	default:
	}
}

func listDatabases(ctx context.Context, s dbdriver.Session) func() error {
	return func() error {
		_, err := s.ListDatabases(ctx)
		return err
	}
}

func execute(ctx context.Context, s dbdriver.Session, statement string) func() error {
	return func() error {
		_, err := s.Execute(ctx, statement)
		return err
	}
}

func TestDriverSessionRunsUpToThreeInstances(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	require.Equal(t, []string{"s3cret"}, srv.Logins(), "opening logs in once")

	ctx := context.Background()
	var ops []<-chan error
	var held []*HeldStatement
	for i := 0; i < 3; i++ {
		ops = append(ops, driverOp(listDatabases(ctx, s)))
		held = append(held, srv.WaitHeld(t))
	}
	conns := map[int]bool{}
	for _, h := range held {
		conns[h.Conn] = true
	}
	assert.Len(t, conns, 3, "three operations run at once, each on a connection of its own")
	assert.Len(t, srv.Logins(), 3, "the two extra instances are created on demand, each one login")

	for i, h := range held {
		h.Release()
		require.NoError(t, waitOp(t, ops[i], callBound))
	}
	for i := 0; i < 5; i++ {
		_, err := s.Execute(ctx, "SELECT 1")
		require.NoError(t, err)
	}
	assert.Len(t, srv.Logins(), 3, "later operations reuse the instances already logged in")
}

func TestDriverCursorIsPinnedToOneInstance(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	ctx := context.Background()

	stream, err := s.StreamTableRows(ctx, "app", "series", dbdriver.TableQuery{})
	require.NoError(t, err)
	require.True(t, stream.Next())
	read := 1

	_, err = s.Execute(ctx, "SELECT 1")
	require.NoError(t, err)
	assert.Len(t, srv.Logins(), 2, "the open cursor keeps its instance: another operation needs one of its own")

	// Bring up the third instance and leave the other two idle, then go on
	// reading: every read must land on the instance holding the cursor.
	a := driverOp(listDatabases(ctx, s))
	ha := srv.WaitHeld(t)
	b := driverOp(listDatabases(ctx, s))
	hb := srv.WaitHeld(t)
	ha.Release()
	hb.Release()
	require.NoError(t, waitOp(t, a, callBound))
	require.NoError(t, waitOp(t, b, callBound))
	require.Len(t, srv.Logins(), 3)

	batch, err := s.ExecuteBatch(ctx, dbdriver.BatchRequest{Statements: []string{"DELETE 1", "DELETE 2", "DELETE 3"}, ContinueOnError: true})
	require.NoError(t, err, "a batch that goes on after errors reports them per statement")
	require.Len(t, batch.Executed, 3)
	batchConns := map[int]bool{}
	for _, st := range srv.SeenOn() {
		if strings.HasPrefix(st.Line, "EXEC DELETE") {
			batchConns[st.Conn] = true
		}
	}
	assert.Len(t, batchConns, 1, "a batch runs from start to end on one instance")

	for stream.Next() {
		read++
		require.Equal(t, fmt.Sprint(read), fmt.Sprint(stream.Row()[0]))
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	assert.Equal(t, 1000, read)
}

func TestDriverFaultFailsOnlyTheOperation(t *testing.T) {
	for _, trigger := range []string{"SELECT PANIC", "SELECT TRAP", "SELECT OOM"} {
		t.Run(trigger, func(t *testing.T) {
			core, logs := observer.New(zap.ErrorLevel)
			prev := cagologger.Default()
			cagologger.SetLogger(zap.New(core))
			defer cagologger.SetLogger(prev)

			p := newDBDriverPlugin(t)
			srv := NewFixtureDBServer(t)
			s := openFixtureSession(t, p, srv)
			ctx := context.Background()

			_, err := s.Execute(ctx, trigger)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "extension database driver fault", "the error says the extension's driver failed")
			assert.NotEmpty(t, logs.FilterMessage("extension database driver fault").All(), "the fault is in the app log")

			res, err := s.Execute(ctx, "SELECT 1")
			require.NoError(t, err, "the session stays usable")
			assert.Equal(t, "1", fmt.Sprint(res.Rows.Rows[0][0]))
			assert.Len(t, srv.Logins(), 2, "the failed instance is discarded; the next operation logs in again")
		})
	}
}

// waitQueued waits until n operations wait for an instance of s: the point a test
// can act on knowing they arrived in that order.
func waitQueued(t *testing.T, s dbdriver.Session, n int) {
	t.Helper()
	ds := s.(*driverSession)
	require.Eventually(t, func() bool {
		ds.mu.Lock()
		defer ds.mu.Unlock()
		return len(ds.queue) == n
	}, callBound, time.Millisecond, "waiting for %d queued operation(s)", n)
}

// holdAll starts n held operations and waits until each reaches the database.
func holdAll(t *testing.T, s dbdriver.Session, srv *FixtureDBServer, n int) ([]<-chan error, []*HeldStatement) {
	t.Helper()
	var ops []<-chan error
	var held []*HeldStatement
	for i := 0; i < n; i++ {
		ops = append(ops, driverOp(listDatabases(context.Background(), s)))
		held = append(held, srv.WaitHeld(t))
	}
	return ops, held
}

func TestDriverSessionQueuesInArrivalOrder(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	ops, held := holdAll(t, s, srv, 3)

	ctx := context.Background()
	statements := []string{"SELECT 1", "SHOW STREAM PROGRESS", "SHOW OPEN CONFIG"}
	queued := make([]<-chan error, 0, len(statements))
	for i, statement := range statements {
		queued = append(queued, driverOp(execute(ctx, s, statement)))
		waitQueued(t, s, i+1)
	}
	assert.Len(t, srv.Logins(), 3, "a fourth operation waits instead of logging in a fourth instance")

	held[0].Release()
	require.NoError(t, waitOp(t, ops[0], callBound))
	for _, op := range queued {
		require.NoError(t, waitOp(t, op, callBound))
	}
	var order []string
	for _, st := range srv.SeenOn() {
		for _, statement := range statements {
			if st.Line == "QUERY "+statement {
				order = append(order, statement)
				assert.Equal(t, held[0].Conn, st.Conn, "the waiting operations run on the instance that was released")
			}
		}
	}
	assert.Equal(t, statements, order, "waiting operations run in arrival order")
	assert.Len(t, srv.Logins(), 3)

	for i := 1; i < 3; i++ {
		held[i].Release()
		require.NoError(t, waitOp(t, ops[i], callBound))
	}
}

func TestDriverCancelingAQueuedOperationOnlyDequeuesIt(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	ops, held := holdAll(t, s, srv, 3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := driverOp(execute(ctx, s, "SHOW OPEN CONFIG"))
	waitQueued(t, s, 1)
	next := driverOp(execute(context.Background(), s, "SELECT 1"))
	waitQueued(t, s, 2)

	cancel()
	require.ErrorIs(t, waitOp(t, canceled, driverInterruptBound), context.Canceled)
	assert.Equal(t, 3, srv.OpenConns(), "no instance was touched")
	for _, op := range ops {
		assertPending(t, op, "a running operation")
	}

	held[0].Release()
	require.NoError(t, waitOp(t, ops[0], callBound))
	require.NoError(t, waitOp(t, next, callBound), "the operation behind it gets the released instance")
	assert.NotContains(t, srv.Seen(), "QUERY SHOW OPEN CONFIG", "the canceled operation never ran")
	assert.Len(t, srv.Logins(), 3)

	for i := 1; i < 3; i++ {
		held[i].Release()
		require.NoError(t, waitOp(t, ops[i], callBound))
	}
}

func TestDriverIdleExtraInstancesClose(t *testing.T) {
	p := newDBDriverPlugin(t)
	p.opts.driverIdleTimeout = 50 * time.Millisecond
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	ops, held := holdAll(t, s, srv, 3)
	for i, h := range held {
		h.Release()
		require.NoError(t, waitOp(t, ops[i], callBound))
	}

	require.Eventually(t, func() bool { return srv.OpenConns() == 1 }, callBound, 5*time.Millisecond,
		"the extra instances close once idle, and with them their connections")
	assert.Never(t, func() bool { return srv.OpenConns() == 0 }, 10*p.opts.driverIdleTimeout, 5*time.Millisecond,
		"the session keeps its last instance")

	_, err := s.Execute(context.Background(), "SELECT 1")
	require.NoError(t, err)
	assert.Len(t, srv.Logins(), 3, "the instance kept is still logged in")
}

// A row statement writer the host no longer holds is forgotten in the guest too,
// with the next call on its instance: one writer per "copy as SQL" does not grow
// the memory of an instance that lives as long as its session.
func TestDriverRowWriterIsReleasedInTheGuest(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	s := openFixtureSession(t, p, srv)
	ctx := context.Background()

	inst, id := prepareAndDropWriter(t, s)
	forgotten := func() bool {
		raw, _ := json.Marshal(dbdriver.RowStatementsRequest{})
		err := inst.call(ctx, driverwire.Call{Op: driverwire.OpRowStatements, Args: driverwire.Args{Writer: id, Request: raw}}, nil)
		return err != nil && strings.Contains(err.Error(), "no database driver row statement writer")
	}
	require.Eventually(t, func() bool {
		runtime.GC()
		if _, err := s.Execute(ctx, "SELECT 1"); err != nil {
			return false
		}
		return forgotten()
	}, callBound, 10*time.Millisecond, "the writer is gone from the guest once the host let go of it")
}

// prepareAndDropWriter prepares a writer on s and lets go of it, returning the
// instance it lives on and its id in the guest.
func prepareAndDropWriter(t *testing.T, s dbdriver.Session) (*driverInstance, uint64) {
	t.Helper()
	w, err := s.PrepareRowStatements(context.Background(), "app", "users")
	require.NoError(t, err)
	dw := w.(*driverRowWriter)
	return dw.inst, dw.id
}
