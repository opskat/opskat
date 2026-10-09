package system

import (
	"context"
	"os"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/opskat/opskat/internal/bootstrap"
)

// extensionStoreRefreshedEvent tells the frontend the extension store index was
// refreshed in the background; it re-reads the store state (no payload).
const extensionStoreRefreshedEvent = "ext:store-refreshed"

// SetExtensionStoreRefresher wires the extension store's index refresh into the
// daily online checks. main.go calls it once, before Startup.
func (s *System) SetExtensionStoreRefresher(refresh func(context.Context) error) {
	s.storeRefresh = refresh
}

// onlineChecksDue reports whether the daily online checks (app update, extension
// store index) should run now: never under OPSKAT_E2E=1, whose result would
// depend on the outside network and the release calendar, and at most once per
// 24 hours.
func (s *System) onlineChecksDue(now time.Time) bool {
	if os.Getenv("OPSKAT_E2E") == "1" {
		return false
	}
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return false
	}
	return now.Unix()-cfg.LastUpdateCheck >= 86400
}

// refreshExtensionStore refreshes the store index and announces it. A failure is
// already logged by the store service and stays visible as the store page's
// error state, so it only suppresses the announcement.
func (s *System) refreshExtensionStore() {
	if s.storeRefresh == nil {
		return
	}
	if err := s.storeRefresh(s.appCtx); err != nil {
		return
	}
	s.emitEvent(extensionStoreRefreshedEvent)
}

func (s *System) emitEvent(event string, data ...any) {
	if s.emit != nil {
		s.emit(event, data...)
		return
	}
	wailsRuntime.EventsEmit(s.ctx, event, data...)
}
