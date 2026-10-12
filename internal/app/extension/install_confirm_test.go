package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/repository/extension_state_repo/mock_extension_state_repo"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"
)

// emitted is one event the binder sent to the frontend.
type emitted struct {
	name    string
	payload map[string]any
}

// eventRecorder stands in for Wails events: it keeps every event, decoded the way
// the frontend receives it (JSON), and lets a test wait for the next one.
type eventRecorder struct {
	mu     sync.Mutex
	events []emitted
	ch     chan emitted
}

func newEventRecorder() *eventRecorder { return &eventRecorder{ch: make(chan emitted, 16)} }

func (r *eventRecorder) emit(name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		panic(err)
	}
	ev := emitted{name: name, payload: m}
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	r.ch <- ev
}

func (r *eventRecorder) next(t *testing.T) emitted {
	t.Helper()
	select {
	case ev := <-r.ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event emitted")
		return emitted{}
	}
}

func newConfirmBinder(appCtx context.Context) (*Extension, *eventRecorder) {
	rec := newEventRecorder()
	return &Extension{appCtx: appCtx, ctx: context.Background(), lang: fixedLang("en"), emit: rec.emit}, rec
}

var sampleConfirm = extension_svc.InstallConfirm{
	Name: "acme", DisplayName: "Acme", Icon: "cloud", From: "1.0.0", To: "2.0.0",
	Source: extension_svc.InstallSourceFile, Size: 1234,
	Capabilities: []extension_svc.CapabilityGrant{{Kind: extension_svc.CapCredentials, Value: "read"}},
	Added:        []extension_svc.CapabilityGrant{{Kind: extension_svc.CapCredentials, Value: "read"}},
}

// startConfirm runs confirmInstall in the background and returns the event it
// emitted plus the channel its result arrives on.
func startConfirm(t *testing.T, e *Extension, rec *eventRecorder, ctx context.Context) (string, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- e.confirmInstall(ctx, sampleConfirm) }()
	ev := rec.next(t)
	require.Equal(t, "ext:install-confirm", ev.name)
	id, _ := ev.payload["id"].(string)
	require.NotEmpty(t, id)
	return id, done
}

func result(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("confirmInstall did not return")
		return nil
	}
}

func TestConfirmInstall_EventCarriesTheConfirm(t *testing.T) {
	e, rec := newConfirmBinder(context.Background())
	id, done := startConfirm(t, e, rec, context.Background())

	ev := rec.events[0].payload
	assert.Equal(t, "acme", ev["name"])
	assert.Equal(t, "Acme", ev["displayName"])
	assert.Equal(t, "cloud", ev["icon"])
	assert.Equal(t, "1.0.0", ev["from"])
	assert.Equal(t, "2.0.0", ev["to"])
	assert.Equal(t, false, ev["downgrade"])
	assert.Equal(t, "file", ev["source"])
	assert.EqualValues(t, 1234, ev["size"])
	assert.Equal(t, []any{map[string]any{"kind": "credentials", "value": "read"}}, ev["capabilities"])
	assert.Equal(t, []any{map[string]any{"kind": "credentials", "value": "read"}}, ev["added"])

	require.NoError(t, e.RespondExtensionInstallConfirm(id, true))
	assert.NoError(t, result(t, done))
}

func TestConfirmInstall_DeclineIsCanceled(t *testing.T) {
	e, rec := newConfirmBinder(context.Background())
	id, done := startConfirm(t, e, rec, context.Background())
	require.NoError(t, e.RespondExtensionInstallConfirm(id, false))
	assert.ErrorIs(t, result(t, done), extension_svc.ErrInstallCanceled)

	// The confirm is settled: a second answer, or one for an id never issued, is refused.
	assert.Error(t, e.RespondExtensionInstallConfirm(id, true))
	assert.Error(t, e.RespondExtensionInstallConfirm("no-such-confirm", true))
}

// Two installs can wait at once; each answer settles only its own confirm.
func TestConfirmInstall_ConcurrentConfirmsAreIndependent(t *testing.T) {
	e, rec := newConfirmBinder(context.Background())
	first, firstDone := startConfirm(t, e, rec, context.Background())
	second, secondDone := startConfirm(t, e, rec, context.Background())
	require.NotEqual(t, first, second)

	require.NoError(t, e.RespondExtensionInstallConfirm(second, false))
	assert.ErrorIs(t, result(t, secondDone), extension_svc.ErrInstallCanceled)
	select {
	case err := <-firstDone:
		t.Fatalf("first confirm settled by the second's answer: %v", err)
	default:
	}
	require.NoError(t, e.RespondExtensionInstallConfirm(first, true))
	assert.NoError(t, result(t, firstDone))
}

// The wait ends without an answer when the caller goes away, the app shuts down
// or nobody answers in time. The dialog is told to close, and an answer that
// arrives afterwards finds nothing to settle.
func TestConfirmInstall_EndsWithoutAnswer(t *testing.T) {
	cases := map[string]func(t *testing.T) (context.Context, context.Context, func()){
		"caller canceled": func(t *testing.T) (context.Context, context.Context, func()) {
			ctx, cancel := context.WithCancel(context.Background())
			return context.Background(), ctx, cancel
		},
		"app shutdown": func(t *testing.T) (context.Context, context.Context, func()) {
			app, cancel := context.WithCancel(context.Background())
			return app, context.Background(), cancel
		},
		"timeout": func(t *testing.T) (context.Context, context.Context, func()) {
			prev := installConfirmTimeout
			installConfirmTimeout = 20 * time.Millisecond
			t.Cleanup(func() { installConfirmTimeout = prev })
			return context.Background(), context.Background(), func() {}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			appCtx, callCtx, end := setup(t)
			e, rec := newConfirmBinder(appCtx)
			id, done := startConfirm(t, e, rec, callCtx)
			end()

			err := result(t, done)
			require.Error(t, err)
			assert.False(t, errors.Is(err, extension_svc.ErrInstallCanceled), "not a user decision: %v", err)
			closed := rec.next(t)
			assert.Equal(t, "ext:install-confirm-closed", closed.name)
			assert.Equal(t, id, closed.payload["id"])
			assert.Error(t, e.RespondExtensionInstallConfirm(id, true))
		})
	}
}

// describeStub answers describe() from memory so the stub WASM never runs.
type describeStub struct{ payload map[string][]byte }

func (s describeStub) LoadDescriptor(name string) (string, []byte, error) {
	return extension.WasmHash(stubWASM), s.payload[name], nil
}
func (describeStub) StoreDescriptor(string, string, []byte) error { return nil }
func (describeStub) DeleteDescriptor(string) error                { return nil }

var stubWASM = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func writeStubExtension(t *testing.T, name string) (string, describeStub) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	manifest := fmt.Sprintf(`{"name":%q,"version":"1.0.0","hostABI":%q,"backend":{"runtime":"wasm","binary":"main.wasm"}}`,
		name, extension.HostABIVersion)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), stubWASM, 0o600))
	desc := fmt.Sprintf(`{"i18n":{"displayName":%q},"assetTypes":[{"type":%q,"i18n":{"name":%q},`+
		`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],"policies":{"type":%q}}`,
		name, name, name, name)
	return dir, describeStub{payload: map[string][]byte{name: []byte(desc)}}
}

// The "install from ZIP / directory" buttons go through the confirm: declining
// is the same quiet no-op as closing the file dialog and lands nothing;
// accepting installs.
func TestInstallFromPath_AsksFirst(t *testing.T) {
	src, stub := writeStubExtension(t, "ext-ask")
	extension.SetDescribeCache(stub)
	t.Cleanup(func() { extension.SetDescribeCache(nil) })

	extDir := t.TempDir()
	ctrl := gomock.NewController(t)
	stateRepo := mock_extension_state_repo.NewMockExtensionStateRepo(ctrl)
	stateRepo.EXPECT().Find(gomock.Any(), "ext-ask").Return(nil, fmt.Errorf("not found")).AnyTimes()
	stateRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	manager := extension.NewManager(extDir, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{Logger: zap.NewNop()})
	}, zap.NewNop())
	svc := extension_svc.New(manager, stateRepo, nil, nil, zap.NewNop(), nil, nil)
	t.Cleanup(func() {
		extreg.Unregister("ext-ask")
		svc.Close(context.Background())
	})
	e, rec := newConfirmBinder(context.Background())
	e.SetService(svc)

	install := func(answer bool) (*extension_svc.ExtensionInfo, error) {
		type out struct {
			info *extension_svc.ExtensionInfo
			err  error
		}
		done := make(chan out, 1)
		go func() {
			info, err := e.installExtensionFromPath(src)
			done <- out{info, err}
		}()
		ev := rec.next(t)
		require.Equal(t, "ext:install-confirm", ev.name)
		assert.Equal(t, "ext-ask", ev.payload["name"])
		assert.Equal(t, "dir", ev.payload["source"])
		require.NoError(t, e.RespondExtensionInstallConfirm(ev.payload["id"].(string), answer))
		select {
		case o := <-done:
			return o.info, o.err
		case <-time.After(5 * time.Second):
			t.Fatal("install did not return")
			return nil, nil
		}
	}

	info, err := install(false)
	require.NoError(t, err)
	assert.Nil(t, info)
	assert.Nil(t, svc.Manager().GetExtension("ext-ask"))
	_, statErr := os.Stat(filepath.Join(extDir, "ext-ask"))
	assert.True(t, os.IsNotExist(statErr), "a declined install lands nothing")

	info, err = install(true)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "ext-ask", info.Name)
	assert.NotNil(t, svc.Manager().GetExtension("ext-ask"))
}
