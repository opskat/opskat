package extension

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/service/extension_svc"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

const (
	// installConfirmEvent asks the frontend to show an install confirm; the
	// payload is installConfirmRequest.
	installConfirmEvent = "ext:install-confirm"
	// installConfirmClosedEvent tells the frontend a confirm ended without an
	// answer (caller gone, app shutting down, timed out); payload {"id": ...}.
	installConfirmClosedEvent = "ext:install-confirm-closed"
)

// installConfirmTimeout bounds how long an install waits for the user; a
// variable so tests can shorten it.
var installConfirmTimeout = 10 * time.Minute

// installConfirmRequest is the ext:install-confirm payload: the confirm itself
// plus the id RespondExtensionInstallConfirm answers it by.
type installConfirmRequest struct {
	ID string `json:"id"`
	extension_svc.InstallConfirm
}

// installConfirms are the install confirms waiting for the user, keyed by id. An
// entry is taken out exactly once — by the answer or by the wait ending — so
// whichever comes first decides.
type installConfirms struct {
	seq     atomic.Uint64
	pending sync.Map // id → chan bool (buffered 1)
}

// confirmInstall shows c in the app-wide install confirm dialog and waits for the
// user. It returns nil when the user accepts and extension_svc.ErrInstallCanceled
// when they decline; when ctx ends, the app shuts down or nobody answers within
// installConfirmTimeout it closes the dialog and returns why. It is the one
// confirm every install path goes through — local ZIP / directory here, the store
// with a confirm built from index data before it downloads anything.
func (e *Extension) confirmInstall(ctx context.Context, c extension_svc.InstallConfirm) error {
	id := fmt.Sprintf("ext_install_%d", e.installConfirms.seq.Add(1))
	log := logger.Ctx(ctx).With(
		zap.String("confirmID", id),
		zap.String("extension", c.Name),
		zap.String("from", c.From),
		zap.String("to", c.To),
		zap.String("source", string(c.Source)),
	)
	ch := make(chan bool, 1)
	// Register before emitting: the answer must find the entry.
	e.installConfirms.pending.Store(id, ch)
	log.Info("extension install confirm started")
	e.emit(installConfirmEvent, installConfirmRequest{ID: id, InstallConfirm: c})

	timer := time.NewTimer(installConfirmTimeout)
	defer timer.Stop()
	var waitErr error
	select {
	case ok := <-ch:
		return answered(log, ok)
	case <-ctx.Done():
		waitErr = ctx.Err()
	case <-e.appCtx.Done():
		waitErr = errors.New("app shutting down")
	case <-timer.C:
		waitErr = errors.New(i18n.Pick(e.lang.Lang(),
			"等待安装确认超时，扩展未安装",
			"Timed out waiting for the install to be confirmed; the extension was not installed"))
	}
	if _, stillPending := e.installConfirms.pending.LoadAndDelete(id); !stillPending {
		// The answer took the entry just as the wait ended; it decides.
		return answered(log, <-ch)
	}
	e.emit(installConfirmClosedEvent, map[string]any{"id": id})
	log.Warn("extension install confirm ended without an answer", zap.Error(waitErr))
	return waitErr
}

func answered(log *zap.Logger, ok bool) error {
	log.Info("extension install confirm completed", zap.Bool("approved", ok))
	if !ok {
		return extension_svc.ErrInstallCanceled
	}
	return nil
}

// RespondExtensionInstallConfirm answers the install confirm id: ok installs, !ok
// cancels. A confirm that is no longer waiting — already answered, closed, or
// never issued — is an error.
func (e *Extension) RespondExtensionInstallConfirm(id string, ok bool) error {
	v, pending := e.installConfirms.pending.LoadAndDelete(id)
	if !pending {
		return fmt.Errorf("extension install confirm %q is no longer pending", id)
	}
	v.(chan bool) <- ok
	return nil
}
