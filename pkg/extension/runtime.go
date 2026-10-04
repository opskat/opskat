// pkg/extension/runtime.go
package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.uber.org/zap"
)

// Guest ABI. The module is a WASI reactor built with
// `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared`: the host runs
// _initialize once per instance and then calls guestEntry for every invocation,
// instead of re-running a whole Go runtime startup per call.
const (
	guestStartFunction = "_initialize"
	guestEntry         = "opskat_call"
	guestMalloc        = "malloc"
	guestFree          = "free"
)

// Response framing: one tag byte, then the payload.
const (
	responseTagOK  = 0
	responseTagErr = 1
)

const (
	// defaultMaxInstances caps how many module instances of one extension may
	// exist. Calls beyond that queue rather than allocating unbounded memory —
	// each instance owns a full Go heap.
	defaultMaxInstances = 4
	// defaultMaxInstanceCalls recycles an instance after this many calls. A
	// reactor instance keeps guest globals between calls, so bounding its life
	// bounds how far state can drift; at ~1.5ms to re-instantiate the amortized
	// cost is negligible.
	defaultMaxInstanceCalls = 512
	// defaultToolTimeout is the ceiling for tool / policy / config calls, which
	// are request-response. A tool may declare its own in describe() (up to
	// MaxToolTimeout). Actions are long-running by design and take their
	// deadline from the caller's context instead.
	defaultToolTimeout = 30 * time.Second
	// defaultMaxResultBytes caps a tool result handed back to the caller. A
	// result over it fails the call: a truncated result would be read as a
	// complete one by the model or page that asked.
	defaultMaxResultBytes = 16 << 20
)

// Plugin represents a loaded WASM extension.
type Plugin struct {
	manifest *Manifest
	compiled wazero.CompiledModule
	runtime  wazero.Runtime
	host     HostProvider
	opts     pluginOptions

	// pool holds up to opts.maxInstances slots. A slot is either a live instance
	// or nil, meaning "you may create one". Taking a slot is what limits
	// concurrency; there is no lock around the call itself.
	pool   chan *instance
	closed atomic.Bool

	// actions maps an in-flight action's invocation id to its invocation.
	// Keyed by id rather than held as a single field because several actions of
	// one extension run at the same time and each is canceled on its own.
	actionsMu sync.Mutex
	actions   map[string]*invocation

	// callSeq names invocations the caller supplies no id for — tools, policy
	// and config calls, which are canceled through their context rather than by
	// id. They still need an id because the guest may emit events from them.
	callSeq atomic.Uint64
}

type pluginOptions struct {
	maxInstances     int
	maxInstanceCalls int
	toolTimeout      time.Duration
	maxResultBytes   int
}

// PluginOption customizes plugin execution.
type PluginOption func(*pluginOptions)

// WithMaxInstances sets how many module instances may run concurrently.
func WithMaxInstances(n int) PluginOption {
	return func(o *pluginOptions) { o.maxInstances = n }
}

// WithMaxInstanceCalls sets how many calls an instance serves before it is recycled.
func WithMaxInstanceCalls(n int) PluginOption {
	return func(o *pluginOptions) { o.maxInstanceCalls = n }
}

// WithToolTimeout sets the ceiling for tool / policy / config calls; a tool's
// own timeout from describe() replaces it for that tool.
func WithToolTimeout(d time.Duration) PluginOption {
	return func(o *pluginOptions) { o.toolTimeout = d }
}

// WithMaxResultBytes sets the largest tool result returned to a caller.
func WithMaxResultBytes(n int) PluginOption {
	return func(o *pluginOptions) { o.maxResultBytes = n }
}

// instance is one reactor module instance plus its exported entry points.
type instance struct {
	mod    api.Module
	entry  api.Function
	malloc api.Function
	free   api.Function
	calls  int
}

// LoadPlugin compiles a WASM binary and prepares it for execution.
// If cache is non-nil, compiled modules are cached to disk for faster subsequent loads.
func LoadPlugin(ctx context.Context, manifest *Manifest, wasmBytes []byte, host HostProvider, cache wazero.CompilationCache, opts ...PluginOption) (*Plugin, error) {
	o := pluginOptions{
		maxInstances:     defaultMaxInstances,
		maxInstanceCalls: defaultMaxInstanceCalls,
		toolTimeout:      defaultToolTimeout,
		maxResultBytes:   defaultMaxResultBytes,
	}
	for _, opt := range opts {
		opt(&o)
	}

	// CloseOnContextDone is what makes a deadline mean anything: without it
	// wazero never checks the context once the guest is running, so a spinning
	// extension would hold its pool slot until it decided to return.
	cfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(1024).
		WithCloseOnContextDone(true)
	if cache != nil {
		cfg = cfg.WithCompilationCache(cache)
	}
	r := wazero.NewRuntimeWithConfig(ctx, cfg)

	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	// Register host functions module
	if err := registerHostModule(ctx, r, host); err != nil {
		if closeErr := r.Close(ctx); closeErr != nil {
			logger.Default().Warn("close wasm runtime after host module error", zap.Error(closeErr))
		}
		return nil, fmt.Errorf("register host functions: %w", err)
	}

	compiled, err := r.CompileModule(ctx, wasmBytes)
	if err != nil {
		if closeErr := r.Close(ctx); closeErr != nil {
			logger.Default().Warn("close wasm runtime after compile error", zap.Error(closeErr))
		}
		return nil, fmt.Errorf("compile wasm: %w", err)
	}

	p := &Plugin{
		manifest: manifest,
		compiled: compiled,
		runtime:  r,
		host:     host,
		opts:     o,
		pool:     make(chan *instance, o.maxInstances),
		actions:  make(map[string]*invocation),
	}
	for i := 0; i < o.maxInstances; i++ {
		p.pool <- nil
	}
	return p, nil
}

// Describe asks the guest to report what it can do: its tools and their parameter
// schemas, asset types, policy groups, pages and snippets. See descriptor.go for
// why those declarations live in the guest rather than in manifest.json.
func (p *Plugin) Describe(ctx context.Context) (json.RawMessage, error) {
	return p.call(ctx, newInvocation(p.nextInvocationID(), nil), "describe", nil, p.opts.toolTimeout)
}

// AssetRef names the asset a call runs against.
//
// It travels in the call envelope rather than in the arguments because the asset
// is not something the caller's arguments get to choose: for a tool it is the
// `exec` target the host resolved and checked the policy against, and a second
// asset id hidden in the arguments would reach an asset the user never granted.
// Tools therefore may not declare one (descriptor.go refuses it), and the guest
// reads it off its ToolContext.
type AssetRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	// AdHoc, when set, means this call is not scoped to ID's row in the
	// database — a "test connection" call, always ad-hoc (see
	// AdHocAssetConfig) — and every host function that would otherwise read
	// the asset's config, endpoints or dial path from storage reads them
	// from here instead. It never crosses the WASM boundary (json:"-"): the
	// guest sees only id/name/type, exactly as for a saved asset.
	AdHoc *AdHocAssetConfig `json:"-"`
}

// callEnvelope is the input of execute_tool and execute_action: what to run, its
// arguments, and the asset it runs against. asset is omitted when the caller has
// none — the frontend testing a configuration form has no saved asset yet — and
// the guest reports that rather than guessing.
type callEnvelope struct {
	Tool   string          `json:"tool,omitempty"`
	Action string          `json:"action,omitempty"`
	Args   json.RawMessage `json:"args"`
	Asset  *AssetRef       `json:"asset,omitempty"`
}

// CallTool calls execute_tool on the extension, scoped to asset (nil when the
// caller has no asset).
//
// Every caller of a tool — AI exec, opsctl, the extension's own page — ends up
// here, so this is where the tool's timeout and the result size limit hold for
// all of them. Canceling ctx interrupts the call, host IO included.
func (p *Plugin) CallTool(ctx context.Context, toolName string, args json.RawMessage, asset *AssetRef) (json.RawMessage, error) {
	input, err := json.Marshal(callEnvelope{Tool: toolName, Args: args, Asset: asset})
	if err != nil {
		return nil, fmt.Errorf("marshal %s input: %w", "execute_tool", err)
	}
	out, err := p.call(ctx, newInvocation(p.nextInvocationID(), nil).scopedTo(asset), "execute_tool", input, p.toolTimeout(toolName))
	if err != nil {
		return nil, err
	}
	if len(out) > p.opts.maxResultBytes {
		return nil, fmt.Errorf("tool %s returned %d bytes, over the host's %d-byte result limit — narrow the request (filter, page, limit) instead of fetching it all at once",
			toolName, len(out), p.opts.maxResultBytes)
	}
	return out, nil
}

// testConnectionEnvelope is the input of test_connection: the asset type to
// test (an extension may register several) and its guest-visible config.
type testConnectionEnvelope struct {
	AssetType string          `json:"assetType"`
	Config    json.RawMessage `json:"config"`
}

// TestConnection runs assetType's describe()-declared test-connection
// handler against adhoc — the asset form's submitted values, never a row read
// from the database (see AdHocAssetConfig): a new asset has no row yet, and a
// saved one being tested must use its unsaved edits, not what is on disk.
//
// It shares CallTool's instance pool and call framing (host IO the handler
// opens is gated and dialed exactly like a tool's, scoped to adhoc instead of
// a stored asset) but is dispatched to "test_connection", not "execute_tool",
// and — unlike every tool call — never goes through policy: testing a
// connection is not an operation on the asset (see docs/specs 测试连接).
func (p *Plugin) TestConnection(ctx context.Context, assetType string, adhoc *AdHocAssetConfig) error {
	input, err := json.Marshal(testConnectionEnvelope{AssetType: assetType, Config: adhoc.Config})
	if err != nil {
		return fmt.Errorf("marshal test_connection input: %w", err)
	}
	// Name carries assetType rather than a real asset name: there may be no
	// saved asset at all, and every error/log site that reads AssetRef.Name
	// (e.g. DefaultHostProvider.resolveAuth's failure message) still needs
	// something to identify the call by.
	ref := &AssetRef{Name: assetType, Type: assetType, AdHoc: adhoc}
	_, err = p.call(ctx, newInvocation(p.nextInvocationID(), nil).scopedTo(ref), "test_connection", input, p.opts.toolTimeout)
	return err
}

// toolTimeout is the deadline for one call of toolName: its own declaration from
// describe(), or the plugin default. An unknown name gets the default; the guest
// reports it as unknown.
func (p *Plugin) toolTimeout(toolName string) time.Duration {
	for _, t := range p.manifest.Tools {
		if t.Name == toolName && t.TimeoutMs > 0 {
			return t.Timeout()
		}
	}
	return p.opts.toolTimeout
}

// CallAction calls execute_action on the extension.
//
// invocationID is the caller's handle on this one run: CancelAction takes it,
// and every event the action emits carries it, so a caller with several actions
// of the same extension in flight can stop one and route the rest. It is opaque
// to the runtime and must be unique among this plugin's running actions.
//
// Unlike a tool call, an action gets no host-imposed deadline: uploads, batch
// copies and event streams are expected to run for minutes, and the caller's
// context is the only party that knows how long is too long.
func (p *Plugin) CallAction(ctx context.Context, invocationID, actionName string, args json.RawMessage, asset *AssetRef) (json.RawMessage, error) {
	input, err := json.Marshal(callEnvelope{Action: actionName, Args: args, Asset: asset})
	if err != nil {
		return nil, fmt.Errorf("marshal %s input: %w", "execute_action", err)
	}

	inv := newInvocation(invocationID, NewActionCancellation()).scopedTo(asset)
	if err := p.trackAction(inv); err != nil {
		return nil, err
	}
	defer p.untrackAction(invocationID)

	return p.call(ctx, inv, "execute_action", input, 0)
}

// CancelAction requests cancellation of the one action running under
// invocationID, and reports whether such an action was running. Actions are
// concurrent, so a caller that means "stop this upload" has to be able to say
// which one.
func (p *Plugin) CancelAction(invocationID string) bool {
	p.actionsMu.Lock()
	inv, ok := p.actions[invocationID]
	p.actionsMu.Unlock()
	if !ok {
		return false
	}
	inv.stop()
	return true
}

// trackAction registers a run under its invocation id. A duplicate id is
// rejected rather than overwritten: two runs sharing one id would make both
// cancellation and event routing ambiguous, which is the bug this id exists to
// remove.
func (p *Plugin) trackAction(inv *invocation) error {
	p.actionsMu.Lock()
	defer p.actionsMu.Unlock()
	if _, exists := p.actions[inv.id]; exists {
		return fmt.Errorf("action invocation %q is already running", inv.id)
	}
	p.actions[inv.id] = inv
	return nil
}

func (p *Plugin) untrackAction(invocationID string) {
	p.actionsMu.Lock()
	defer p.actionsMu.Unlock()
	delete(p.actions, invocationID)
}

// cancelAllActions stops every running action. Only shutdown means this.
func (p *Plugin) cancelAllActions() {
	p.actionsMu.Lock()
	defer p.actionsMu.Unlock()
	for _, inv := range p.actions {
		inv.stop()
	}
}

// nextInvocationID names a call that has no caller-supplied id. The plugin name
// keeps it readable in a log line next to an action's id.
func (p *Plugin) nextInvocationID() string {
	return fmt.Sprintf("%s#%d", p.manifest.Name, p.callSeq.Add(1))
}

// CheckPolicy calls check_policy on the extension.
//
// No asset travels with it: the guest answers from the tool's own registration
// (its policy action and the resources derived from the arguments), and the asset
// side of the decision — which permission groups are granted on it — is the
// host's own (internal/extreg).
//
// resources are path.Match globs (see decodePolicyDecision); empty means the call
// touches no resource. A tool refusing the call's arguments returns an
// *ArgsRejectedError — a decision to deny, not a failure to classify.
func (p *Plugin) CheckPolicy(ctx context.Context, toolName string, args json.RawMessage) (action string, resources []string, err error) {
	input, err := json.Marshal(map[string]any{
		"tool": toolName,
		"args": json.RawMessage(args),
	})
	if err != nil {
		return "", nil, fmt.Errorf("marshal %s input: %w", "check_policy", err)
	}
	result, err := p.call(ctx, newInvocation(p.nextInvocationID(), nil), "check_policy", input, p.opts.toolTimeout)
	if err != nil {
		return "", nil, err
	}
	return decodePolicyDecision(result)
}

// ValidateConfig calls validate_config on the extension. config is the asset's
// config exactly as about to be persisted, which may carry the host's reserved
// connection-settings key (proxy chain, TLS) — stripped here, before it ever
// crosses into guest code, the same as ctx.AssetConfig() strips it.
func (p *Plugin) ValidateConfig(ctx context.Context, config json.RawMessage) ([]ValidationError, error) {
	config, err := StripHostConnectionConfig(config)
	if err != nil {
		return nil, fmt.Errorf("strip host connection config: %w", err)
	}
	result, err := p.call(ctx, newInvocation(p.nextInvocationID(), nil), "validate_config", config, p.opts.toolTimeout)
	if err != nil {
		return nil, err
	}
	var errors []ValidationError
	if err := json.Unmarshal(result, &errors); err != nil {
		return nil, fmt.Errorf("unmarshal validation errors: %w", err)
	}
	return errors, nil
}

// ValidationError represents a config validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Close releases the WASM runtime resources.
func (p *Plugin) Close(ctx context.Context) error {
	p.closed.Store(true)
	// Unblock actions that are polling ShouldStop so they can return before the
	// runtime is torn out from under them.
	p.cancelAllActions()
	return p.runtime.Close(ctx)
}

// Manifest returns the plugin's manifest.
func (p *Plugin) Manifest() *Manifest {
	return p.manifest
}

// call runs one guest invocation on an instance borrowed from the pool.
// maxDuration of 0 means "no host-imposed deadline".
func (p *Plugin) call(ctx context.Context, inv *invocation, fnName string, input []byte, maxDuration time.Duration) (json.RawMessage, error) {
	if p.closed.Load() {
		return nil, fmt.Errorf("plugin closed")
	}

	req, err := json.Marshal(map[string]any{
		"fn":    fnName,
		"input": json.RawMessage(input),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal %s envelope: %w", fnName, err)
	}

	callCtx := ctx
	if maxDuration > 0 {
		var stop context.CancelFunc
		callCtx, stop = context.WithTimeout(ctx, maxDuration)
		defer stop()
	}

	inst, err := p.acquire(callCtx)
	if err != nil {
		return nil, err
	}

	// wazero hands this context to every host function the guest calls, which is
	// how a host call finds the invocation it belongs to.
	guestCtx := withInvocation(callCtx, inv)

	// CloseOnContextDone only stops a guest that is running bytecode. One blocked
	// inside a host function — a TCP read with no deadline, an HTTP round trip or
	// body read — never gets back to it, so it would ignore both the deadline and
	// the caller's cancellation and hold its pool slot for good. Closing the
	// invocation's handles when the context ends fails that host call instead;
	// the guest returns into bytecode, wazero sees the context and ends the call,
	// and release discards the instance as poisoned.
	stopInterrupt := context.AfterFunc(callCtx, inv.close)
	out, callErr := inst.invoke(guestCtx, req)
	stopInterrupt()
	inv.close()
	p.release(callCtx, inst, callErr != nil)

	if callErr != nil {
		return nil, fmt.Errorf("call %s: %w", fnName, callErr)
	}
	return out, nil
}

// acquire takes a pool slot, instantiating a module if the slot is empty.
func (p *Plugin) acquire(ctx context.Context) (*instance, error) {
	var slot *instance
	select {
	case slot = <-p.pool:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if slot != nil {
		return slot, nil
	}
	inst, err := p.newInstance(ctx)
	if err != nil {
		p.pool <- nil // give the slot back, otherwise one failure shrinks the pool forever
		return nil, err
	}
	return inst, nil
}

// release returns an instance to the pool, discarding it when it is spent or
// when the call left it in an unknown state.
func (p *Plugin) release(ctx context.Context, inst *instance, poisoned bool) {
	if poisoned || p.closed.Load() || inst.calls >= p.opts.maxInstanceCalls {
		if err := inst.mod.Close(ctx); err != nil {
			logger.Default().Warn("close wasm instance", zap.Error(err))
		}
		p.pool <- nil
		return
	}
	p.pool <- inst
}

func (p *Plugin) newInstance(ctx context.Context) (*instance, error) {
	cfg := wazero.NewModuleConfig().
		// Anonymous, so several instances of one compiled module can coexist.
		WithName("").
		WithStartFunctions(guestStartFunction).
		WithStdout(&guestLogWriter{name: p.manifest.Name}).
		WithStderr(&guestLogWriter{name: p.manifest.Name, warn: true}).
		WithSysWalltime().
		WithSysNanotime()

	mod, err := p.runtime.InstantiateModule(ctx, p.compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("instantiate module: %w", err)
	}
	inst := &instance{
		mod:    mod,
		entry:  mod.ExportedFunction(guestEntry),
		malloc: mod.ExportedFunction(guestMalloc),
		free:   mod.ExportedFunction(guestFree),
	}
	if inst.entry == nil || inst.malloc == nil || inst.free == nil {
		if closeErr := mod.Close(ctx); closeErr != nil {
			logger.Default().Warn("close wasm instance after export check", zap.Error(closeErr))
		}
		return nil, fmt.Errorf("extension does not export %s/%s/%s — build it with pkg/extsdk (GOOS=wasip1 go build -buildmode=c-shared)",
			guestEntry, guestMalloc, guestFree)
	}
	return inst, nil
}

// invoke copies the request into guest memory, calls the entry point, and
// returns a copy of the reply.
func (i *instance) invoke(ctx context.Context, req []byte) (json.RawMessage, error) {
	i.calls++

	ptr, err := i.alloc(ctx, req)
	if err != nil {
		return nil, err
	}
	// The guest frees the request buffer as soon as it has copied it, so there is
	// no host-side free here.

	results, err := i.entry.Call(ctx, uint64(ptr), uint64(len(req)))
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("%s returned no value", guestEntry)
	}
	return i.readResponse(results[0])
}

func (i *instance) alloc(ctx context.Context, data []byte) (uint32, error) {
	if len(data) == 0 {
		return 0, nil
	}
	res, err := i.malloc.Call(ctx, uint64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("guest malloc: %w", err)
	}
	if len(res) == 0 || res[0] == 0 {
		return 0, fmt.Errorf("guest malloc returned null for %d bytes", len(data))
	}
	ptr := uint32(res[0])
	if !i.mod.Memory().Write(ptr, data) {
		return 0, fmt.Errorf("write %d bytes to guest memory at %d", len(data), ptr)
	}
	return ptr, nil
}

func (i *instance) readResponse(packed uint64) (json.RawMessage, error) {
	ptr := uint32(packed >> 32)
	size := uint32(packed)
	if size == 0 {
		return nil, fmt.Errorf("%s returned an empty response", guestEntry)
	}
	raw, ok := i.mod.Memory().Read(ptr, size)
	if !ok {
		return nil, fmt.Errorf("read %d bytes of guest response at %d", size, ptr)
	}
	tag, payload := raw[0], raw[1:]
	out := make([]byte, len(payload))
	copy(out, payload)
	if tag == responseTagErr {
		return nil, fmt.Errorf("%s", out)
	}
	if tag != responseTagOK {
		return nil, fmt.Errorf("%s returned unknown response tag %d", guestEntry, tag)
	}
	return out, nil
}

// guestLogWriter forwards whatever the guest writes to stdout/stderr — panic
// traces, stray fmt.Println debugging — into the app log instead of dropping it.
type guestLogWriter struct {
	name string
	warn bool
}

func (w *guestLogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg == "" {
		return len(p), nil
	}
	l := logger.Default().With(zap.String("extension", w.name), zap.String("output", msg))
	if w.warn {
		l.Warn("extension guest output")
	} else {
		l.Debug("extension guest output")
	}
	return len(p), nil
}

var _ io.Writer = (*guestLogWriter)(nil)
