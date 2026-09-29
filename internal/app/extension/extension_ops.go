package extension

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/extreg"
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
	asset, err := e.assetRef(extName, assetID)
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

// CancelExtensionAction stops the one action run identified by invocationID. The
// instance pool runs several actions of one extension at once, so cancellation is
// per run, never per extension.
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
// one that silently reads whatever asset the arguments happened to name. Any other
// id must be an asset of extName's own types (see assetRef).
//
// The call runs directly: it is the user's own action in the page, so there is no
// policy check, approval, grant or audit — those belong to AI exec and opsctl. It
// is still scoped to its asset (ctx.AssetConfig, endpoint gating, connection
// settings and credential injection all follow the AssetRef) and its arguments are
// held to the tool's declared schema, as Plugin.CallTool does not check them.
//
// invocationID is the frontend's per-call correlation token (the same convention
// CallExtensionAction already uses): CancelExtensionTool takes it to stop this
// call while it runs. A failure, the guest's included, is returned as-is.
func (e *Extension) CallExtensionTool(extName, tool, argsJSON, invocationID string, assetID int64) (string, error) {
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

	args := []byte(argsJSON)
	if argsJSON == "" {
		args = []byte("{}")
	}
	args, err := extreg.ValidateToolArgs(ext.Manifest, tool, args)
	if err != nil {
		return "", err
	}

	asset, err := e.assetRef(extName, assetID)
	if err != nil {
		return "", err
	}
	if invocationID == "" {
		return "", fmt.Errorf("invocation id is required to call a tool")
	}

	ctx, end, err := e.toolCalls.begin(i18n.Ctx(e.ctx, e.lang.Lang()), invocationID)
	if err != nil {
		return "", err
	}
	defer end()

	result, err := ext.Plugin.CallTool(ctx, tool, args, asset)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// CancelExtensionTool stops the page tool call running under invocationID: the
// guest is interrupted, host IO it is blocked in fails, and the call returns an
// error to the page. A call that has already returned is not an error to cancel
// — the page cannot know whether its abort raced the result — and a cancel that
// overtakes its call keeps that call from starting (see toolCalls).
func (e *Extension) CancelExtensionTool(invocationID string) error {
	if invocationID == "" {
		return fmt.Errorf("invocation id is required to cancel a tool call")
	}
	log := logger.Ctx(e.ctx).With(zap.String("invocationID", invocationID))
	if !e.toolCalls.cancel(invocationID) {
		log.Debug("extension tool cancel found nothing running; held against a late start")
		return nil
	}
	log.Info("extension tool cancel requested")
	return nil
}

// assetRef names the asset a frontend-initiated call runs against. A 0 id is the
// frontend saying it has none — the asset configuration form runs `test_connection`
// on a configuration that has not been saved yet — and the guest is told so.
// Any other id must be an asset of a type extName registers: the page belongs to
// that extension, and letting it name a builtin or another extension's asset would
// scope the call — and its asset config — to something it has no claim on.
func (e *Extension) assetRef(extName string, assetID int64) (*extension.AssetRef, error) {
	if assetID == 0 {
		return nil, nil
	}
	_, asset, err := ownedAsset(i18n.Ctx(e.ctx, e.lang.Lang()), e.service, extName, assetID)
	if err != nil {
		return nil, err
	}
	return &extension.AssetRef{ID: assetID, Name: asset.Name, Type: asset.Type}, nil
}

// GetDecryptedExtensionConfig returns the config of an asset extName owns for
// that extension's configuration form: password fields decrypted when extName
// declares credentials:read, withheld otherwise (see getDecryptedExtConfig). An
// asset of a type extName does not register is refused.
func (e *Extension) GetDecryptedExtensionConfig(assetID int64, extName string) (string, error) {
	if e.service == nil {
		return "", fmt.Errorf("extension system not initialized")
	}
	return getDecryptedExtConfig(e.service, extName, assetID)
}

// ValidateExtensionConfig runs the guest's config validator over configJSON, the
// configuration the asset form is about to save for one of extName's asset types,
// and returns its errors with their field names so the form can show each on its
// field. Saving still runs the same validator (extreg.validateConfig) — this is
// the structured view of that check, not a second rule set. An empty result means
// the configuration is valid.
func (e *Extension) ValidateExtensionConfig(extName, assetType, configJSON string) ([]extension.ValidationError, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	ext := e.service.Bridge().Get(extName)
	if ext == nil {
		return nil, fmt.Errorf("extension %q not loaded", extName)
	}
	if ext.Manifest.AssetTypeDef(assetType) == nil {
		return nil, fmt.Errorf("asset type %q does not belong to extension %q", assetType, extName)
	}
	if ext.Plugin == nil {
		return nil, fmt.Errorf("extension %q has no backend plugin", extName)
	}
	return ext.Plugin.ValidateConfig(i18n.Ctx(e.ctx, e.lang.Lang()), json.RawMessage(configJSON))
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

// InstalledExtensionVersion reports whether an extension named name is installed
// (enabled or disabled on disk) and its version. `opsctl ext dev` shows it in the
// confirmation dialog so the user sees an overwrite before approving it; it is a
// package-level function for the same reason InstallExtensionDir is.
func InstalledExtensionVersion(e *Extension, name string) (string, bool) {
	if e.service == nil {
		return "", false
	}
	info, err := e.service.GetDetail(name, e.lang.Lang())
	if err != nil {
		// GetDetail's only failure is "not installed".
		return "", false
	}
	return info.Version, true
}
