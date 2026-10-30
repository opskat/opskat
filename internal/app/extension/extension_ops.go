package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// ListInstalledExtensions returns all installed extensions with their manifests
// localized for lang (the frontend's i18next language, e.g. "zh-CN"). The caller
// names the language instead of relying on the System.SetLanguage mirror: the
// frontend re-fetches on every language switch, and Wails runs the two calls
// concurrently, so the mirror may not hold the new language yet.
func (e *Extension) ListInstalledExtensions(lang string) []extension_svc.ExtensionInfo {
	if e.service == nil {
		return nil
	}
	return e.service.ListInstalled(lang)
}

// GetExtensionManifest returns a single extension's merged manifest: the typed
// view of its contributions (extreg.Manifest), as the extension list carries it.
func (e *Extension) GetExtensionManifest(name string) (*extreg.Manifest, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	ext := e.service.Manager().GetExtension(name)
	if ext == nil {
		return nil, fmt.Errorf("extension %q not found", name)
	}
	return extreg.ManifestOf(ext.Manifest)
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
// held to the tool's declared schema, as Plugin.CallTool does not check them. A
// page may call any tool of its extension, page-only or not; it runs under the
// tool's declared timeout, or none for a page-only tool declaring NoTimeout.
//
// invocationID is the frontend's per-call correlation token, minted there because
// the page needs it before the call returns: every event the tool emits reaches
// the page as "ext:tool:event" stamped with it, and CancelExtensionTool takes it
// to stop this call while it runs. A failure, the guest's included, is returned
// as-is.
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

	view, err := extreg.ManifestOf(ext.Manifest)
	if err != nil {
		return "", err
	}
	args := []byte(argsJSON)
	if argsJSON == "" {
		args = []byte("{}")
	}
	args, err = extreg.ValidateToolArgs(view, tool, args)
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

	ctx, end, err := e.toolCalls.begin(i18n.Ctx(e.ctx, e.lang.Lang()), extName, invocationID, ext.Plugin.CancelTool)
	if err != nil {
		return "", err
	}
	defer end()

	result, err := ext.Plugin.CallTool(ctx, extension.ToolCall{
		InvocationID: invocationID,
		Name:         tool,
		Args:         args,
		Asset:        asset,
		Timeout:      extreg.ToolTimeout(view, tool),
	})
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// CancelExtensionTool stops the page tool call running under invocationID: the
// guest's ShouldStop turns true, host IO it is blocked in fails, a guest that
// does not return within the grace period is cut off (Plugin.CancelTool), and
// the call ends as canceled. A call that has already returned is not an error
// to cancel — the page cannot know whether its abort raced the result — and a
// cancel that overtakes its call keeps that call from starting (see toolCalls).
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
// frontend saying it has none — a page calling a tool before it has an asset —
// and the guest is told so.
// Any other id must be an asset of a type extName registers: the page belongs to
// that extension, and letting it name a builtin or another extension's asset would
// scope the call — and its asset config — to something it has no claim on.
func (e *Extension) assetRef(extName string, assetID int64) (*extension.AssetRef, error) {
	if assetID == 0 {
		return nil, nil
	}
	_, asset, _, err := e.service.OwnedTypedAsset(i18n.Ctx(e.ctx, e.lang.Lang()), extName, assetID)
	if err != nil {
		return nil, err
	}
	return &extension.AssetRef{ID: assetID, Name: asset.Name, Type: asset.Type}, nil
}

// GetDecryptedExtensionConfig returns the config of an asset extName owns for
// that extension's configuration form: password fields decrypted when extName
// declares credentials:read, withheld otherwise (see extension_svc.Service.FormConfig).
// An asset of a type extName does not register is refused.
func (e *Extension) GetDecryptedExtensionConfig(assetID int64, extName string) (string, error) {
	if e.service == nil {
		return "", fmt.Errorf("extension system not initialized")
	}
	ctx, cancel := context.WithTimeout(i18n.Ctx(e.ctx, e.lang.Lang()), 10*time.Second)
	defer cancel()
	return e.service.FormConfig(ctx, extName, assetID)
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
	view, err := extreg.ManifestOf(ext.Manifest)
	if err != nil {
		return nil, err
	}
	if view.AssetType(assetType) == nil {
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

// installExtensionFromPath installs a ZIP / directory the user picked, after they
// confirm what it is in the install confirm dialog. Declining is the same quiet
// no-op as closing the file dialog: nil, nil.
func (e *Extension) installExtensionFromPath(sourcePath string) (*extension_svc.ExtensionInfo, error) {
	manifest, err := e.service.Install(i18n.Ctx(e.ctx, e.lang.Lang()), sourcePath, e.confirmInstall)
	if errors.Is(err, extension_svc.ErrInstallCanceled) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	tr := func(key string) string { return key }
	if ext := e.service.Manager().GetExtension(manifest.Name); ext != nil {
		tr = func(key string) string { return ext.Translate(e.lang.Lang(), key) }
	}
	return extension_svc.NewExtensionInfo(manifest, tr, true)
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
// binding, and "install whatever directory I name, with a confirm I supply" is
// not something the frontend should be able to ask for.
//
// The path itself is deliberately the same one the "install from directory"
// button takes — one install implementation, so a dev build cannot load through
// a laxer route than a shipped one. Only where the confirm is shown differs:
// confirm is `opsctl ext dev`'s approval dialog instead of the app-wide install
// confirm, and the commit re-checks the installed version it approved.
func InstallExtensionDir(e *Extension, ctx context.Context, sourceDir string, confirm extension_svc.ConfirmInstallFunc) (string, string, error) {
	if e.service == nil {
		return "", "", fmt.Errorf("extension system not initialized")
	}
	manifest, err := e.service.Install(i18n.Ctx(ctx, e.lang.Lang()), sourceDir, confirm)
	if err != nil {
		return "", "", err
	}
	return manifest.Name, manifest.Version, nil
}
