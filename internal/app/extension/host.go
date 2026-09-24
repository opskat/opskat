package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// assetConfigGetter implements extension.AssetConfigGetter for one extension.
//
// The asset id it receives is the one the host scoped the call to (the guest
// cannot name one — see pkg/extension opAssetGetConfig). It still refuses any
// asset whose type the extension does not register: a tool or action reaching
// this with a builtin or another extension's asset is a host-side mis-scoping,
// and serving it would hand that asset's config — credentials included — to an
// extension that has no claim on it.
type assetConfigGetter struct {
	ext     *Extension
	extName string
}

func (g *assetConfigGetter) GetAssetConfig(assetID int64) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	caller, asset, err := ownedAsset(ctx, g.ext.service, g.extName, assetID)
	if err != nil {
		return nil, err
	}
	if asset.Config == "" {
		return json.RawMessage("{}"), nil
	}

	logger.Ctx(ctx).Info("extension accessed asset config",
		zap.String("extension", caller.Name),
		zap.Int64("asset_id", assetID),
		zap.String("asset_type", asset.Type),
		zap.Bool("plaintext_allowed", caller.Manifest.CheckCredentialRead() == nil),
	)

	return decryptConfigPasswordFields(json.RawMessage(asset.Config), asset.Type, caller)
}

// ownedAsset loads assetID on behalf of extName and confirms the asset's type is
// one extName itself registers. Every path that hands an asset — or its config —
// to an extension goes through here, so "which extension may see this asset" is
// answered once, by the asset's stored type rather than by whoever asked.
func ownedAsset(ctx context.Context, svc *extension_svc.Service, extName string, assetID int64) (*extension.Extension, *extension_svc.HostAssetConfig, error) {
	caller := svc.Bridge().Get(extName)
	if caller == nil {
		return nil, nil, fmt.Errorf("extension %q not loaded", extName)
	}
	asset, err := svc.GetHostAssetConfig(ctx, assetID)
	if err != nil {
		return nil, nil, fmt.Errorf("asset %d not found: %w", assetID, err)
	}
	if caller.Manifest.AssetTypeDef(asset.Type) == nil {
		return nil, nil, fmt.Errorf("asset %d (type %q) does not belong to extension %q", assetID, asset.Type, extName)
	}
	return caller, asset, nil
}

// fileDialogOpener implements extension.FileDialogOpener
type fileDialogOpener struct {
	ctx context.Context // Wails app context
}

func (o *fileDialogOpener) FileDialog(dialogType string, opts extension.DialogOptions) (string, error) {
	switch dialogType {
	case "open":
		return wailsRuntime.OpenFileDialog(o.ctx, wailsRuntime.OpenDialogOptions{
			Title:   opts.Title,
			Filters: toWailsFilters(opts.Filters),
		})
	case "save":
		return wailsRuntime.SaveFileDialog(o.ctx, wailsRuntime.SaveDialogOptions{
			Title:           opts.Title,
			DefaultFilename: opts.DefaultName,
			Filters:         toWailsFilters(opts.Filters),
		})
	default:
		return "", fmt.Errorf("unknown dialog type: %q", dialogType)
	}
}

func toWailsFilters(filters []string) []wailsRuntime.FileFilter {
	if len(filters) == 0 {
		return nil
	}
	result := make([]wailsRuntime.FileFilter, 0, len(filters))
	for _, f := range filters {
		result = append(result, wailsRuntime.FileFilter{
			DisplayName: f,
			Pattern:     f,
		})
	}
	return result
}

// kvStore implements extension.KVStore, scoped to one extension
type kvStore struct {
	ext     *Extension
	extName string
}

func (s *kvStore) Get(key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.ext.service.GetHostKV(ctx, s.extName, key)
}

func (s *kvStore) Set(key string, value []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.ext.service.SetHostKV(ctx, s.extName, key, value)
}

// actionEventHandler implements extension.ActionEventHandler
type actionEventHandler struct {
	ctx     context.Context // Wails app context
	extName string
}

// OnActionEvent forwards one action event to the frontend.
//
// invocationId rides along because one handler serves every concurrent run of
// this extension: without it a listener sees the extension's events merged into
// one stream and attributes another upload's progress to its own.
func (h *actionEventHandler) OnActionEvent(invocationID, eventType string, data json.RawMessage) error {
	wailsRuntime.EventsEmit(h.ctx, "ext:action:event", map[string]any{
		"extension":    h.extName,
		"invocationId": invocationID,
		"eventType":    eventType,
		"data":         json.RawMessage(data),
	})
	return nil
}

// getDecryptedExtConfig returns the config of an asset extName owns, with
// password fields decrypted per extName's credentials capability.
func getDecryptedExtConfig(svc *extension_svc.Service, extName string, assetID int64) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner, asset, err := ownedAsset(ctx, svc, extName, assetID)
	if err != nil {
		return "", err
	}
	if asset.Config == "" {
		return "{}", nil
	}
	decrypted, err := decryptConfigPasswordFields(json.RawMessage(asset.Config), asset.Type, owner)
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

// decryptConfigPasswordFields decrypts the fields assetType's configSchema marks
// format:"password". Whether the plaintext or an opaque handle comes back is
// decided by ext — the extension the config is being handed to, which
// ownedAsset has already confirmed registers assetType.
func decryptConfigPasswordFields(raw json.RawMessage, assetType string, ext *extension.Extension) (json.RawMessage, error) {
	def := ext.Manifest.AssetTypeDef(assetType)
	if def == nil || len(def.ConfigSchema) == 0 {
		return raw, nil
	}
	passwordFields := extension.PasswordFieldsFromSchema(def.ConfigSchema)
	if len(passwordFields) == 0 {
		return raw, nil
	}

	allowPlaintext := ext.Manifest.CheckCredentialRead() == nil

	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s config: %w", assetType, err)
	}

	for _, field := range passwordFields {
		val, ok := cfg[field]
		if !ok {
			continue
		}
		var encrypted string
		if err := json.Unmarshal(val, &encrypted); err != nil || encrypted == "" {
			continue
		}
		// A field that will not decrypt fails the whole read: passing the stored
		// value through would hand the guest ciphertext and skip the credentials
		// decision entirely.
		decrypted, err := credential_svc.Default().Decrypt(encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt password field %q of %s config: %w", field, assetType, err)
		}
		if allowPlaintext {
			b, _ := json.Marshal(decrypted)
			cfg[field] = b
		} else {
			handle := credentialHandleFor(ext.Name, assetType, field, encrypted)
			handleJSON, _ := json.Marshal(map[string]string{
				"__credential_handle": handle,
			})
			cfg[field] = handleJSON
		}
	}
	return json.Marshal(cfg)
}

// credentialHandleFor creates an opaque handle for a credential field.
func credentialHandleFor(extName, assetType, field, encrypted string) string {
	h := sha256.Sum256([]byte(extName + ":" + assetType + ":" + field + ":" + encrypted))
	return "cred_" + hex.EncodeToString(h[:8])
}

// assetDialer implements extension.AssetDialer for one extension.
type assetDialer struct {
	ext     *Extension
	extName string
}

// DialContextFor resolves the connection path of an asset the extension owns.
// Only the settings its asset type declares take effect: an SSH tunnel set on an
// asset whose type does not declare connection.sshTunnel is ignored. The tunnel
// is dialed through the same proxy-chain machinery built-in types use.
func (d *assetDialer) DialContextFor(ctx context.Context, assetID int64) (extension.DialContextFunc, error) {
	owner, asset, err := ownedAsset(ctx, d.ext.service, d.extName, assetID)
	if err != nil {
		return nil, err
	}
	conn := owner.Manifest.AssetTypeDef(asset.Type).Connection
	if conn == nil || !conn.SSHTunnel || asset.SSHTunnelID == 0 {
		return nil, nil
	}
	chain := asset_entity.EffectiveProxyChain(nil, asset.SSHTunnelID, nil)
	dial, err := connpool.ProxyChainDialContext(ctx, chain)
	if err != nil {
		return nil, fmt.Errorf("resolve SSH tunnel (asset %d): %w", asset.SSHTunnelID, err)
	}
	logger.Ctx(ctx).Info("extension asset connects through SSH tunnel",
		zap.String("extension", d.extName),
		zap.Int64("assetID", assetID),
		zap.Int64("sshTunnelID", asset.SSHTunnelID),
	)
	return dial, nil
}
