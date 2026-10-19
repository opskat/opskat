package extension

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	t.Helper()
	root := t.TempDir()
	dir := extensiontest.InstallFixture(t, root)

	credential_svc.SetDefault(credential_svc.New("page-call-key", []byte("0123456789abcdef")))
	e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
	var svc *extension_svc.Service
	mgr := extension.NewManager(root, func(name string) extension.HostProvider {
		return extension.NewDefaultHostProvider(svc.HostConfig(name))
	}, zap.NewNop())
	t.Cleanup(func() { mgr.Close(context.Background()) })

	assets := mock_asset_repo.NewMockAssetRepo(gomock.NewController(t))
	svc = extension_svc.New(mgr, nil, nil, assets, zap.NewNop(), nil, nil)
	_, err := mgr.LoadExtension(context.Background(), dir)
	require.NoError(t, err)
	svc.Bridge().Register(mgr.GetExtension(extensiontest.FixtureName))
	e.SetService(svc)
	return e, assets
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
