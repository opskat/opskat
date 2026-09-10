package extension

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

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

// CallExtensionAction calls an extension action and streams events via Wails Events.
//
// invocationID is the caller's correlation token for this one run: it comes back
// on every "ext:action:event" this action emits, and CancelExtensionAction takes
// it to stop this run and no other. The frontend mints it because the frontend is
// the party that has to correlate — it needs the token before the call it is
// about to make returns.
func (e *Extension) CallExtensionAction(extName, action, argsJSON, invocationID string, assetID int64) (string, error) {
	plugin, err := e.actionPlugin(extName)
	if err != nil {
		return "", err
	}
	if invocationID == "" {
		return "", fmt.Errorf("invocation id is required to run an action")
	}
	asset, err := e.assetRef(assetID)
	if err != nil {
		return "", err
	}

	var args json.RawMessage
	if argsJSON != "" {
		args = json.RawMessage(argsJSON)
	} else {
		args = json.RawMessage("{}")
	}

	log := logger.Ctx(e.ctx).With(
		zap.String("extension", extName),
		zap.String("action", action),
		zap.String("invocationID", invocationID),
	)
	log.Info("extension action started")

	result, err := plugin.CallAction(i18n.Ctx(e.ctx, e.lang.Lang()), invocationID, action, args, asset)
	if err != nil {
		log.Error("extension action failed", zap.Error(err))
		return "", fmt.Errorf("call action %s/%s: %w", extName, action, err)
	}
	log.Info("extension action completed")
	return string(result), nil
}

// CancelExtensionAction stops the one action run identified by invocationID.
//
// It used to take only the extension name, which stopped every action that
// extension had in flight — harmless while a plugin-wide lock made "in flight"
// mean one, wrong once the instance pool let several uploads run at once.
func (e *Extension) CancelExtensionAction(extName, invocationID string) error {
	plugin, err := e.actionPlugin(extName)
	if err != nil {
		return err
	}
	if invocationID == "" {
		return fmt.Errorf("invocation id is required to cancel an action")
	}
	log := logger.Ctx(e.ctx).With(
		zap.String("extension", extName),
		zap.String("invocationID", invocationID),
	)
	if !plugin.CancelAction(invocationID) {
		log.Warn("extension action cancel found nothing running")
		return fmt.Errorf("extension %q has no running action %q", extName, invocationID)
	}
	log.Info("extension action cancel requested")
	return nil
}

// actionPlugin resolves the loaded plugin behind an extension name, which is the
// same three-step check every action entry point needs.
func (e *Extension) actionPlugin(extName string) (*extension.Plugin, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	ext := e.service.Manager().GetExtension(extName)
	if ext == nil {
		return nil, fmt.Errorf("extension %q not loaded", extName)
	}
	if ext.Plugin == nil {
		return nil, fmt.Errorf("extension %q has no backend plugin", extName)
	}
	return ext.Plugin, nil
}

// CallExtensionTool calls an extension tool from the extension's own frontend
// page, against the asset that page was opened on.
//
// assetID is how the asset reaches the guest: a tool takes no asset argument, so
// a page that leaves this 0 gets a call the guest reports as unscoped rather than
// one that silently reads whatever asset the arguments happened to name.
func (e *Extension) CallExtensionTool(extName, tool string, argsJSON string, assetID int64) (string, error) {
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

	asset, err := e.assetRef(assetID)
	if err != nil {
		return "", err
	}

	result, err := ext.Plugin.CallTool(i18n.Ctx(e.ctx, e.lang.Lang()), tool, args, asset)
	if err != nil {
		return "", fmt.Errorf("call tool %s/%s: %w", extName, tool, err)
	}
	return string(result), nil
}

// assetRef names the asset a frontend-initiated call runs against. A 0 id is the
// frontend saying it has none — the asset configuration form runs `test_connection`
// on a configuration that has not been saved yet — and the guest is told so.
func (e *Extension) assetRef(assetID int64) (*extension.AssetRef, error) {
	if assetID == 0 {
		return nil, nil
	}
	asset, err := e.service.GetHostAssetConfig(i18n.Ctx(e.ctx, e.lang.Lang()), assetID)
	if err != nil {
		return nil, err
	}
	return &extension.AssetRef{ID: assetID, Name: asset.Name, Type: asset.Type}, nil
}

// GetDecryptedExtensionConfig returns the asset config with password fields decrypted.
func (e *Extension) GetDecryptedExtensionConfig(assetID int64, extName string) (string, error) {
	if e.service == nil {
		return "", fmt.Errorf("extension system not initialized")
	}
	return getDecryptedExtConfig(assetID, e.service, e.service.Bridge())
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

// InstallExtensionDir installs an unpacked extension directory and returns what
// landed. It backs `opsctl ext dev`, which is why it is a package-level function
// rather than a method: every exported method on this binder becomes a Wails
// binding, and "install whatever directory I name, no dialog" is not something
// the frontend should be able to ask for.
//
// The path itself is deliberately the same one the "install from directory"
// button takes — one install implementation, so a dev build cannot load through
// a laxer route than a shipped one.
func InstallExtensionDir(e *Extension, ctx context.Context, sourceDir string) (string, string, error) {
	if e.service == nil {
		return "", "", fmt.Errorf("extension system not initialized")
	}
	manifest, err := e.service.Install(i18n.Ctx(ctx, e.lang.Lang()), sourceDir)
	if err != nil {
		return "", "", err
	}
	return manifest.Name, manifest.Version, nil
}
