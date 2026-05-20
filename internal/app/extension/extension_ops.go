package extension

import (
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// AssetTypeInfo combines built-in and extension asset types for the frontend.
type AssetTypeInfo struct {
	Type          string `json:"type"`
	ExtensionName string `json:"extensionName,omitempty"`
	DisplayName   string `json:"displayName"`
	SSHTunnel     bool   `json:"sshTunnel"`
}

// ListInstalledExtensions returns all loaded extensions.
func (e *Extension) ListInstalledExtensions() []extension_svc.ExtensionInfo {
	if e.service == nil {
		return nil
	}
	return e.service.ListInstalled(e.lang.Lang())
}

// GetExtensionManifest returns a single extension's manifest.
func (e *Extension) GetExtensionManifest(name string) (*extension.Manifest, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	ext := e.service.Manager().GetExtension(name)
	if ext == nil {
		return nil, fmt.Errorf("extension %q not found", name)
	}
	return ext.Manifest, nil
}

// GetAvailableAssetTypes returns built-in + extension asset types.
func (e *Extension) GetAvailableAssetTypes() []AssetTypeInfo {
	types := []AssetTypeInfo{
		{Type: asset_entity.AssetTypeSSH, DisplayName: "SSH"},
		{Type: asset_entity.AssetTypeDatabase, DisplayName: "Database"},
		{Type: asset_entity.AssetTypeRedis, DisplayName: "Redis"},
		{Type: asset_entity.AssetTypeMongoDB, DisplayName: "MongoDB", SSHTunnel: true},
		{Type: asset_entity.AssetTypeKafka, DisplayName: "Kafka", SSHTunnel: true},
		{Type: asset_entity.AssetTypeK8s, DisplayName: "K8S"},
		{Type: asset_entity.AssetTypeSerial, DisplayName: "Serial"},
	}
	if e.service != nil {
		bridge := e.service.Bridge()
		lang := e.lang.Lang()
		for _, at := range bridge.GetAssetTypes() {
			displayName := at.I18n.Name
			if ext := e.service.Manager().GetExtension(at.ExtensionName); ext != nil {
				displayName = ext.Translate(lang, at.I18n.Name)
			}
			types = append(types, AssetTypeInfo{
				Type:          at.Type,
				ExtensionName: at.ExtensionName,
				DisplayName:   displayName,
				SSHTunnel:     true,
			})
		}
	}
	return types
}

// CallExtensionAction calls an extension action and streams events via Wails Events.
func (e *Extension) CallExtensionAction(extName, action string, argsJSON string) (string, error) {
	if e.service == nil {
		return "", fmt.Errorf("extension system not initialized")
	}
	ext := e.service.Manager().GetExtension(extName)
	if ext == nil {
		return "", fmt.Errorf("extension %q not loaded", extName)
	}
	if ext.Plugin == nil {
		return "", fmt.Errorf("extension %q has no backend plugin", extName)
	}

	var args json.RawMessage
	if argsJSON != "" {
		args = json.RawMessage(argsJSON)
	} else {
		args = json.RawMessage("{}")
	}

	result, err := ext.Plugin.CallAction(i18n.Ctx(e.ctx, e.lang.Lang()), action, args)
	if err != nil {
		return "", fmt.Errorf("call action %s/%s: %w", extName, action, err)
	}
	return string(result), nil
}

// CancelExtensionAction triggers cancellation of the currently running action.
func (e *Extension) CancelExtensionAction(extName string) error {
	if e.service == nil {
		return fmt.Errorf("extension system not initialized")
	}
	ext := e.service.Manager().GetExtension(extName)
	if ext == nil {
		return fmt.Errorf("extension %q not loaded", extName)
	}
	if ext.Plugin == nil {
		return fmt.Errorf("extension %q has no backend plugin", extName)
	}
	ext.Plugin.CancelActiveAction()
	return nil
}

// CallExtensionTool calls an extension tool (for frontend config testing etc.)
func (e *Extension) CallExtensionTool(extName, tool string, argsJSON string) (string, error) {
	if e.service == nil {
		return "", fmt.Errorf("extension system not initialized")
	}
	ext := e.service.Manager().GetExtension(extName)
	if ext == nil {
		return "", fmt.Errorf("extension %q not loaded", extName)
	}
	if ext.Plugin == nil {
		return "", fmt.Errorf("extension %q has no backend plugin", extName)
	}

	var args json.RawMessage
	if argsJSON != "" {
		args = json.RawMessage(argsJSON)
	} else {
		args = json.RawMessage("{}")
	}

	result, err := ext.Plugin.CallTool(i18n.Ctx(e.ctx, e.lang.Lang()), tool, args)
	if err != nil {
		return "", fmt.Errorf("call tool %s/%s: %w", extName, tool, err)
	}
	return string(result), nil
}

// GetDecryptedExtensionConfig returns the asset config with password fields decrypted.
func (e *Extension) GetDecryptedExtensionConfig(assetID int64, extName string) (string, error) {
	if e.service == nil {
		return "", fmt.Errorf("extension system not initialized")
	}
	return getDecryptedExtConfig(assetID, e.service.Bridge())
}

// InstallExtension opens a file dialog and installs an extension from a zip file.
func (e *Extension) InstallExtension() (*extension_svc.ExtensionInfo, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}

	selected, err := wailsRuntime.OpenFileDialog(e.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Extension Package",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "Extension Package (*.zip)", Pattern: "*.zip"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("file dialog: %w", err)
	}
	if selected == "" {
		return nil, nil // user canceled
	}

	return e.installExtensionFromPath(selected)
}

// InstallExtensionFromDirectory opens a directory dialog and installs a local extension.
func (e *Extension) InstallExtensionFromDirectory() (*extension_svc.ExtensionInfo, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}

	selected, err := wailsRuntime.OpenDirectoryDialog(e.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Extension Directory",
	})
	if err != nil {
		return nil, fmt.Errorf("directory dialog: %w", err)
	}
	if selected == "" {
		return nil, nil
	}

	return e.installExtensionFromPath(selected)
}

func (e *Extension) installExtensionFromPath(sourcePath string) (*extension_svc.ExtensionInfo, error) {
	manifest, err := e.service.Install(i18n.Ctx(e.ctx, e.lang.Lang()), sourcePath)
	if err != nil {
		return nil, err
	}

	ext := e.service.Manager().GetExtension(manifest.Name)
	lm := manifest
	if ext != nil {
		lm = manifest.Localized(func(key string) string { return ext.Translate(e.lang.Lang(), key) })
	}

	return &extension_svc.ExtensionInfo{
		Name:        lm.Name,
		Version:     lm.Version,
		Icon:        lm.Icon,
		DisplayName: lm.I18n.DisplayName,
		Description: lm.I18n.Description,
		Enabled:     true,
		Manifest:    lm,
	}, nil
}

// UninstallExtension removes an extension and optionally cleans up its data.
func (e *Extension) UninstallExtension(name string, cleanData bool) error {
	if e.service == nil {
		return fmt.Errorf("extension system not initialized")
	}
	return e.service.Uninstall(i18n.Ctx(e.ctx, e.lang.Lang()), name, cleanData, false)
}

// ForceUninstallExtension removes an extension and optionally cleans up its data, bypassing the orphan-asset check.
func (e *Extension) ForceUninstallExtension(name string, cleanData bool) error {
	if e.service == nil {
		return fmt.Errorf("extension system not initialized")
	}
	return e.service.Uninstall(i18n.Ctx(e.ctx, e.lang.Lang()), name, cleanData, true)
}

// EnableExtension loads a disabled extension and registers it.
func (e *Extension) EnableExtension(name string) error {
	if e.service == nil {
		return fmt.Errorf("extension system not initialized")
	}
	return e.service.Enable(i18n.Ctx(e.ctx, e.lang.Lang()), name)
}

// DisableExtension unloads a running extension without removing files.
func (e *Extension) DisableExtension(name string) error {
	if e.service == nil {
		return fmt.Errorf("extension system not initialized")
	}
	return e.service.Disable(i18n.Ctx(e.ctx, e.lang.Lang()), name)
}

// GetExtensionDetail returns the full manifest and state for a single extension.
func (e *Extension) GetExtensionDetail(name string) (*extension_svc.ExtensionInfo, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	return e.service.GetDetail(name, e.lang.Lang())
}

// ReloadExtensions re-scans extensions directory and updates the bridge.
func (e *Extension) ReloadExtensions() error {
	if e.service == nil {
		return fmt.Errorf("extension system not initialized")
	}
	return e.service.Reload(i18n.Ctx(e.ctx, e.lang.Lang()))
}
