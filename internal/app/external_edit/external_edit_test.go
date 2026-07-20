package external_edit

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/service/external_edit_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type langStub struct{}

type testContextKey struct{}

func (langStub) Lang() string { return "en" }

func TestNewReceivesConstructedService(t *testing.T) {
	svc := &external_edit_svc.Service{}
	emitter := NewEventEmitter()

	binder := New(langStub{}, svc, emitter)

	require.NotNil(t, binder)
	assert.Same(t, svc, binder.svc)
	assert.Same(t, emitter, binder.emitter)
}

func TestEventEmitterDropsEventsUntilStartupContextIsAvailable(t *testing.T) {
	emitter := NewEventEmitter()

	assert.NotPanics(t, func() {
		emitter.Emit(external_edit_svc.Event{})
	})

	ctx := context.WithValue(context.Background(), testContextKey{}, "wails")
	emitter.Startup(ctx)
	assert.Same(t, ctx, emitter.ctx)
}
