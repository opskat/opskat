package extension

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/service/extstore_svc"
)

// storeProgressEvent reports a store install's progress to the frontend; the
// payload is extstore_svc.Progress {name, phase, done, total}.
const storeProgressEvent = "ext:store-progress"

// InstallStoreExtension installs or updates name from the official store: the
// install confirm (built from the signed index) first, then download, sha256
// check and install, reporting ext:store-progress along the way. The outcome —
// success, a silent cancel, or a failure with its kind — is the result; the
// error is only for a binder that is not set up.
func (e *Extension) InstallStoreExtension(name string) (extstore_svc.InstallResult, error) {
	if e.service == nil {
		return extstore_svc.InstallResult{}, fmt.Errorf("extension system not initialized")
	}
	if e.store == nil {
		return extstore_svc.InstallResult{}, errStoreNotInitialized
	}
	installedName, version, err := InstallFromStore(e, e.ctx, name)
	return extstore_svc.InstallOutcome(installedName, version, err), nil
}

// InstallFromStore installs or updates name from the official store through the
// app-wide install confirm, emitting ext:store-progress, and returns what
// landed. It is a package-level function, like InstallExtensionDir, so opsctl
// can drive it through main.go without it becoming a second binding.
func InstallFromStore(e *Extension, ctx context.Context, name string) (string, string, error) {
	if e.service == nil {
		return "", "", fmt.Errorf("extension system not initialized")
	}
	if e.store == nil {
		return "", "", errStoreNotInitialized
	}
	return e.store.InstallFromStore(i18n.Ctx(ctx, e.lang.Lang()), name, extstore_svc.InstallOptions{
		Installer:  e.service,
		Confirm:    e.confirmInstall,
		OnProgress: func(p extstore_svc.Progress) { e.emit(storeProgressEvent, p) },
	})
}
