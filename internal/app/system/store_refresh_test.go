package system

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"
)

func TestOnlineChecksDue(t *testing.T) {
	initBootstrapForSystemTest(t)
	s := New(t.Context(), SkillContent{})
	now := time.Unix(2_000_000_000, 0)

	save := func(last int64) {
		t.Helper()
		if err := bootstrap.SaveConfig(&bootstrap.AppConfig{LastUpdateCheck: last}); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}
	}

	save(now.Unix() - 86400 - 1)
	t.Setenv("OPSKAT_E2E", "")
	if !s.onlineChecksDue(now) {
		t.Fatal("last check over 24h ago: want due")
	}
	save(now.Unix() - 3600)
	if s.onlineChecksDue(now) {
		t.Fatal("checked an hour ago: want not due")
	}
	save(now.Unix() - 86400 - 1)
	t.Setenv("OPSKAT_E2E", "1")
	if s.onlineChecksDue(now) {
		t.Fatal("OPSKAT_E2E=1: online checks must be skipped")
	}
}

func TestRefreshExtensionStore(t *testing.T) {
	newSystem := func(refresh func(context.Context) error) (*System, *[]string) {
		s := New(t.Context(), SkillContent{})
		var events []string
		s.emit = func(event string, _ ...any) { events = append(events, event) }
		s.SetExtensionStoreRefresher(refresh)
		return s, &events
	}

	t.Run("announces a refreshed index", func(t *testing.T) {
		s, events := newSystem(func(context.Context) error { return nil })
		s.refreshExtensionStore()
		if len(*events) != 1 || (*events)[0] != "ext:store-refreshed" {
			t.Fatalf("events = %v, want [ext:store-refreshed]", *events)
		}
	})

	t.Run("a failed refresh announces nothing", func(t *testing.T) {
		s, events := newSystem(func(context.Context) error { return errors.New("offline") })
		s.refreshExtensionStore()
		if len(*events) != 0 {
			t.Fatalf("events = %v, want none", *events)
		}
	})

	t.Run("without a store service it does nothing", func(t *testing.T) {
		s := New(t.Context(), SkillContent{})
		s.refreshExtensionStore()
	})
}
