package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/extension_data_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/sshpool"
	"github.com/opskat/opskat/pkg/extension"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// assetConfigGetter implements extension.AssetConfigGetter.
type assetConfigGetter struct {
	ext *Extension
}

func (g *assetConfigGetter) GetAssetConfig(assetID int64) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	asset, err := asset_repo.Asset().Find(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("asset %d not found: %w", assetID, err)
	}
	if asset.Config == "" {
		return json.RawMessage("{}"), nil
	}

	bridge := g.ext.service.Bridge()
	ext := bridge.GetExtensionByAssetType(asset.Type)
	if ext != nil {
		zap.L().Info("extension accessed asset config",
			zap.String("extension", ext.Name),
			zap.Int64("asset_id", assetID),
			zap.String("asset_type", asset.Type),
			zap.Bool("plaintext_allowed", ext.Manifest.CheckCredentialRead() == nil),
		)
	}

	raw := json.RawMessage(asset.Config)
	return decryptConfigPasswordFields(raw, asset.Type, bridge)
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
	extName string
}

func (s *kvStore) Get(key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	val, err := extension_data_repo.ExtensionData().Get(ctx, s.extName, key)
	if err != nil {
		return nil, nil // KV miss returns nil, not error
	}
	return val, nil
}

func (s *kvStore) Set(key string, value []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return extension_data_repo.ExtensionData().Set(ctx, s.extName, key, value)
}

// actionEventHandler implements extension.ActionEventHandler
type actionEventHandler struct {
	ctx     context.Context // Wails app context
	extName string
}

func (h *actionEventHandler) OnActionEvent(eventType string, data json.RawMessage) error {
	wailsRuntime.EventsEmit(h.ctx, "ext:action:event", map[string]any{
		"extension": h.extName,
		"eventType": eventType,
		"data":      json.RawMessage(data),
	})
	return nil
}

// getDecryptedExtConfig returns the asset config with password fields decrypted.
func getDecryptedExtConfig(assetID int64, bridge *extension.Bridge) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	asset, err := asset_repo.Asset().Find(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("asset %d not found: %w", assetID, err)
	}
	if asset.Config == "" {
		return "{}", nil
	}
	raw := json.RawMessage(asset.Config)
	decrypted, err := decryptConfigPasswordFields(raw, asset.Type, bridge)
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

// decryptConfigPasswordFields decrypts fields marked as format:"password" in the configSchema.
func decryptConfigPasswordFields(raw json.RawMessage, assetType string, bridge *extension.Bridge) (json.RawMessage, error) {
	if bridge == nil {
		return raw, nil
	}
	ext := bridge.GetExtensionByAssetType(assetType)
	if ext == nil {
		return raw, nil
	}
	schema := ext.Manifest.AssetTypes[0].ConfigSchema
	for _, at := range ext.Manifest.AssetTypes {
		if at.Type == assetType {
			schema = at.ConfigSchema
			break
		}
	}
	if len(schema) == 0 {
		return raw, nil
	}
	passwordFields := extension.PasswordFieldsFromSchema(schema)
	if len(passwordFields) == 0 {
		return raw, nil
	}

	allowPlaintext := ext.Manifest.CheckCredentialRead() == nil

	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return raw, err
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
		decrypted, err := credential_svc.Default().Decrypt(encrypted)
		if err != nil {
			continue
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

// tunnelDialer implements extension.TunnelDialer using the SSH pool
type tunnelDialer struct {
	pool *sshpool.Pool
}

func (d *tunnelDialer) Dial(tunnelAssetID int64, addr string) (net.Conn, error) {
	if d.pool == nil {
		return nil, fmt.Errorf("SSH pool not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := d.pool.Get(ctx, tunnelAssetID)
	if err != nil {
		return nil, fmt.Errorf("get SSH tunnel: %w", err)
	}
	conn, err := client.Dial("tcp", addr)
	if err != nil {
		d.pool.Release(tunnelAssetID)
		return nil, fmt.Errorf("dial through tunnel: %w", err)
	}
	return conn, nil
}
