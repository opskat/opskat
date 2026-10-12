package extension

import (
	"errors"

	"github.com/opskat/opskat/internal/service/extstore_svc"
)

var errStoreNotInitialized = errors.New("extension store not initialized")

// SetStoreService main.go 注入扩展商店服务。
func (e *Extension) SetStoreService(svc *extstore_svc.Service) { e.store = svc }

// ListStore returns the store as last refreshed, localized for lang (the
// frontend's i18next language — see ListInstalledExtensions for why the caller
// names it). It does not fetch; before the first refresh it is empty with no
// error.
func (e *Extension) ListStore(lang string) (extstore_svc.State, error) {
	if e.store == nil {
		return extstore_svc.State{}, errStoreNotInitialized
	}
	return e.store.State(lang), nil
}

// RefreshStore fetches and verifies the official index, then returns the store
// like ListStore. A failed refresh is not an error here: it comes back as
// State.Error, whose kind decides what the store page offers.
func (e *Extension) RefreshStore(lang string) (extstore_svc.State, error) {
	if e.store == nil {
		return extstore_svc.State{}, errStoreNotInitialized
	}
	// Refresh logs its own start / end / failure, and State carries the failure.
	_ = e.store.Refresh(e.ctx)
	return e.store.State(lang), nil
}

// InstalledVersions returns the installed extensions (enabled or disabled),
// name → version, for the store to compare against. Empty until the extension
// system has started. A package-level function, like InstalledExtensionVersion,
// so it is not exposed as a binding.
func InstalledVersions(e *Extension) map[string]string {
	out := map[string]string{}
	if e.service == nil {
		return out
	}
	for _, info := range e.service.ListInstalled(e.lang.Lang()) {
		out[info.Name] = info.Version
	}
	return out
}
