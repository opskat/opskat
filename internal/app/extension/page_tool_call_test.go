package extension

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// pageCallFixture loads pkg/extension's fixture extension through the real
// Manager, so describe() (tool schemas, timeouts) reaches the manifest exactly
// as in the app, and wires the binder the way main.go does.
func pageCallFixture(t *testing.T) (*Extension, *mock_asset_repo.MockAssetRepo) {
	e, assets, _ := pageCallFixtureWithEvents(t)
	return e, assets
}

// pageCallFixtureWithEvents is pageCallFixture that also records every event the
// binder sends to the frontend.
func pageCallFixtureWithEvents(t *testing.T) (*Extension, *mock_asset_repo.MockAssetRepo, *toolEventRecorder) {
	t.Helper()
	root := t.TempDir()
	dir := extensiontest.InstallFixture(t, root)

	credential_svc.SetDefault(credential_svc.New("page-call-key", []byte("0123456789abcdef")))
	rec := &toolEventRecorder{}
	e := &Extension{ctx: context.Background(), lang: fixedLang("en"), emit: rec.emit}
	var svc *extension_svc.Service
	mgr := extension.NewManager(root, func(name string) extension.HostProvider {
		cfg := svc.HostConfig(name)
		cfg.ActionEvents = e.NewActionEventHandler(name)
		return extension.NewDefaultHostProvider(cfg)
	}, extensiontest.AcceptingContributions(), zap.NewNop())
	t.Cleanup(func() { mgr.Close(context.Background()) })

	assets := mock_asset_repo.NewMockAssetRepo(gomock.NewController(t))
	svc = extension_svc.New(mgr, nil, nil, assets, zap.NewNop(), nil, nil)
	_, err := mgr.LoadExtension(context.Background(), dir)
	require.NoError(t, err)
	svc.Bridge().Register(mgr.GetExtension(extensiontest.FixtureName))
	e.SetService(svc)
	return e, assets, rec
}

// toolEventRecorder stands in for Wails events. It never blocks: a streaming
// guest emits far more events than a test reads.
type toolEventRecorder struct {
	mu     sync.Mutex
	events []map[string]any
	others []string
}

func (r *toolEventRecorder) emit(name string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name != "ext:tool:event" {
		r.others = append(r.others, name)
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		panic(err)
	}
	r.events = append(r.events, m)
}

// forInvocation returns the tool events stamped with invocationID, in order.
func (r *toolEventRecorder) forInvocation(invocationID string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, ev := range r.events {
		if ev["invocationId"] == invocationID {
			out = append(out, ev)
		}
	}
	return out
}

func (r *toolEventRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events) + len(r.others)
}

func fixtureAsset(t *testing.T, endpoint string) *asset_entity.Asset {
	t.Helper()
	enc, err := credential_svc.Default().Encrypt("s3cret")
	require.NoError(t, err)
	cfg, err := json.Marshal(map[string]any{"endpoint": endpoint, "authType": "basic", "username": "u", "password": enc})
	require.NoError(t, err)
	return &asset_entity.Asset{ID: 1, Name: "fx", Type: "fixture", Config: string(cfg)}
}

func TestPageToolCallRunsDirectlyScopedToItsAsset(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(srv.Close)
	e, assets := pageCallFixture(t)
	assets.EXPECT().Find(gomock.Any(), int64(1)).Return(fixtureAsset(t, srv.URL), nil).AnyTimes()

	t.Run("runs directly and sees its asset config", func(t *testing.T) {
		res, err := e.CallExtensionTool("fixture-ext", "asset_config", "{}", "inv-cfg", 1)
		require.NoError(t, err)
		assert.Contains(t, res, srv.URL)
		assert.Contains(t, res, `"name":"fx"`)
	})

	t.Run("reaches only the asset endpoint, with credentials injected", func(t *testing.T) {
		res, err := e.CallExtensionTool("fixture-ext", "http_get", `{"url":"`+srv.URL+`"}`, "inv-http", 1)
		require.NoError(t, err)
		assert.Contains(t, res, "pong")
		assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("u:s3cret")), gotAuth)

		_, err = e.CallExtensionTool("fixture-ext", "http_get", `{"url":"http://127.0.0.1:1/"}`, "inv-other", 1)
		require.Error(t, err, "an address that is not the asset's endpoint stays gated")
	})

	t.Run("guest error reaches the page as-is", func(t *testing.T) {
		_, err := e.CallExtensionTool("fixture-ext", "fail", "{}", "inv-fail", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deliberate failure")
	})
}

func TestPageToolCallValidatesArgsAgainstTheToolSchema(t *testing.T) {
	e, assets := pageCallFixture(t)
	assets.EXPECT().Find(gomock.Any(), int64(1)).Return(fixtureAsset(t, "http://127.0.0.1:9"), nil).AnyTimes()

	for name, args := range map[string]string{
		"unknown key":      `{"msg":"x","bogus":1}`,
		"-file style key":  `{"msg-file":"/etc/passwd"}`,
		"wrong value type": `{"msg":5}`,
	} {
		for _, assetID := range []int64{0, 1} {
			_, err := e.CallExtensionTool("fixture-ext", "echo", args, "inv-"+name, assetID)
			require.Error(t, err, "%s (asset %d) must be rejected before reaching the guest", name, assetID)
		}
	}

	res, err := e.CallExtensionTool("fixture-ext", "echo", `{"msg":"hi"}`, "inv-ok", 1)
	require.NoError(t, err)
	assert.Contains(t, res, "hi")
}

func TestPageToolCallCancelAndTimeoutStillApply(t *testing.T) {
	e, assets := pageCallFixture(t)
	assets.EXPECT().Find(gomock.Any(), int64(1)).Return(fixtureAsset(t, "http://127.0.0.1:9"), nil).AnyTimes()

	t.Run("cancel interrupts a running call", func(t *testing.T) {
		done := make(chan error, 1)
		go func() {
			_, err := e.CallExtensionTool("fixture-ext", "spin_long", `{"ms":20000}`, "inv-spin", 1)
			done <- err
		}()
		require.Eventually(t, func() bool {
			_ = e.CancelExtensionTool("inv-spin")
			select {
			case err := <-done:
				done <- err
				return true
			default:
				return false
			}
		}, 10*time.Second, 50*time.Millisecond)
		require.Error(t, <-done)
	})

	t.Run("the tool's declared timeout ends the call", func(t *testing.T) {
		start := time.Now()
		_, err := e.CallExtensionTool("fixture-ext", "spin_short", `{"ms":20000}`, "inv-short", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deadline exceeded")
		assert.Less(t, time.Since(start), 5*time.Second)
	})
}

// A page tool's events reach the page that called it, as ext:tool:event stamped
// with the extension and the page's invocation id.
func TestPageToolCallStreamsEventsToThePage(t *testing.T) {
	e, assets, rec := pageCallFixtureWithEvents(t)
	assets.EXPECT().Find(gomock.Any(), int64(1)).Return(fixtureAsset(t, "http://127.0.0.1:9"), nil).AnyTimes()

	res, err := e.CallExtensionTool("fixture-ext", "stream", `{"count":3,"delay_ms":0}`, "inv-stream", 1)
	require.NoError(t, err)
	assert.Contains(t, res, `"sent":3`)

	events := rec.forInvocation("inv-stream")
	require.Len(t, events, 3)
	for i, ev := range events {
		assert.Equal(t, "fixture-ext", ev["extension"])
		assert.Equal(t, "progress", ev["eventType"])
		assert.Equal(t, map[string]any{"i": float64(i)}, ev["data"])
	}
	assert.Empty(t, rec.others)
}

// AI exec and opsctl call the same tools through Plugin.CallTool with no page
// behind them: the events their calls emit go nowhere.
func TestToolEventsOfANonPageCallerAreDropped(t *testing.T) {
	e, _, rec := pageCallFixtureWithEvents(t)
	ext := e.service.Manager().GetExtension("fixture-ext")

	_, err := ext.Plugin.CallTool(context.Background(), extension.ToolCall{
		InvocationID: "ai-exec-1", Name: "stream", Args: json.RawMessage(`{"count":3,"delay_ms":0}`)})
	require.NoError(t, err)
	assert.Zero(t, rec.count())
}

// Canceling a page tool that has no timeout makes its ShouldStop true — the
// fixture reports noticing in a last "stopped" event — and the call ends as
// canceled, not with the guest's result.
func TestPageToolCallCancelStopsANoTimeoutTool(t *testing.T) {
	e, assets, rec := pageCallFixtureWithEvents(t)
	assets.EXPECT().Find(gomock.Any(), int64(1)).Return(fixtureAsset(t, "http://127.0.0.1:9"), nil).AnyTimes()

	done := make(chan error, 1)
	go func() {
		_, err := e.CallExtensionTool("fixture-ext", "stream", `{"count":100000000,"delay_ms":1}`, "inv-long", 1)
		done <- err
	}()
	require.Eventually(t, func() bool { return len(rec.forInvocation("inv-long")) > 0 }, 10*time.Second, 5*time.Millisecond)
	require.NoError(t, e.CancelExtensionTool("inv-long"))

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("canceled page tool never returned")
	}
	events := rec.forInvocation("inv-long")
	assert.Equal(t, "stopped", events[len(events)-1]["eventType"])
}
