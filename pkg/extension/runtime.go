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
	// are request-response. Actions are long-running by design and take their
	// deadline from the caller's context instead.
	defaultToolTimeout = 30 * time.Second
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

	// actions maps an in-flight action's invocation id to its cancellation flag.
	// Keyed by id rather than held as a single field because several actions of
	// one extension run at the same time and each is canceled on its own.
	actionsMu sync.Mutex
	actions   map[string]*ActionCancellation

	// callSeq names invocations that nobody cancels — tools, policy and config
	// calls. They still need an id because the guest may emit events from them.
	callSeq atomic.Uint64
}

type pluginOptions struct {
	maxInstances     int
	maxInstanceCalls int
	toolTimeout      time.Duration
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

// WithToolTimeout sets the ceiling for tool / policy / config calls.
func WithToolTimeout(d time.Duration) PluginOption {
	return func(o *pluginOptions) { o.toolTimeout = d }
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
		actions:  make(map[string]*ActionCancellation),
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

// CallTool calls execute_tool on the extension.
func (p *Plugin) CallTool(ctx context.Context, toolName string, args json.RawMessage) (json.RawMessage, error) {
	input, err := json.Marshal(map[string]any{
		"tool": toolName,
		"args": json.RawMessage(args),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal %s input: %w", "execute_tool", err)
	}
	return p.call(ctx, newInvocation(p.nextInvocationID(), nil), "execute_tool", input, p.opts.toolTimeout)
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
func (p *Plugin) CallAction(ctx context.Context, invocationID, actionName string, args json.RawMessage) (json.RawMessage, error) {
	input, err := json.Marshal(map[string]any{
		"action": actionName,
		"args":   json.RawMessage(args),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal %s input: %w", "execute_action", err)
	}

	cancel := NewActionCancellation()
	if err := p.trackAction(invocationID, cancel); err != nil {
		return nil, err
	}
	defer p.untrackAction(invocationID)

	return p.call(ctx, newInvocation(invocationID, cancel), "execute_action", input, 0)
}

// CancelAction requests cancellation of the one action running under
// invocationID, and reports whether such an action was running. Actions are
// concurrent, so a caller that means "stop this upload" has to be able to say
// which one.
func (p *Plugin) CancelAction(invocationID string) bool {
	p.actionsMu.Lock()
	cancel, ok := p.actions[invocationID]
	p.actionsMu.Unlock()
	if !ok {
		return false
	}
	cancel.Cancel()
	return true
}

// trackAction registers a run under its invocation id. A duplicate id is
// rejected rather than overwritten: two runs sharing one id would make both
// cancellation and event routing ambiguous, which is the bug this id exists to
// remove.
func (p *Plugin) trackAction(invocationID string, cancel *ActionCancellation) error {
	p.actionsMu.Lock()
	defer p.actionsMu.Unlock()
	if _, exists := p.actions[invocationID]; exists {
		return fmt.Errorf("action invocation %q is already running", invocationID)
	}
	p.actions[invocationID] = cancel
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
	for _, c := range p.actions {
		c.Cancel()
	}
}

// nextInvocationID names a call that has no caller-supplied id. The plugin name
// keeps it readable in a log line next to an action's id.
func (p *Plugin) nextInvocationID() string {
	return fmt.Sprintf("%s#%d", p.manifest.Name, p.callSeq.Add(1))
}

// CheckPolicy calls check_policy on the extension.
func (p *Plugin) CheckPolicy(ctx context.Context, toolName string, args json.RawMessage) (action, resource string, err error) {
	input, err := json.Marshal(map[string]any{
		"tool": toolName,
		"args": json.RawMessage(args),
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal %s input: %w", "check_policy", err)
	}
	result, err := p.call(ctx, newInvocation(p.nextInvocationID(), nil), "check_policy", input, p.opts.toolTimeout)
	if err != nil {
		return "", "", err
	}
	var decision struct {
		Action   string `json:"action"`
		Resource string `json:"resource"`
	}
	if err := json.Unmarshal(result, &decision); err != nil {
		return "", "", fmt.Errorf("unmarshal policy decision: %w", err)
	}
	return decision.Action, decision.Resource, nil
}

// ValidateConfig calls validate_config on the extension.
func (p *Plugin) ValidateConfig(ctx context.Context, config json.RawMessage) ([]ValidationError, error) {
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

	out, callErr := inst.invoke(guestCtx, req)
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
		return nil, fmt.Errorf("extension does not export %s/%s/%s — rebuild it against the reactor SDK (GOOS=wasip1 go build -buildmode=c-shared)",
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
