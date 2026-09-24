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

func newSessionTextBinder() *ExternalEdit {
	binder := New(langStub{}, &external_edit_svc.Service{}, NewEventEmitter())
	binder.ctx = context.Background()
	return binder
}

func TestReadExternalEditSessionTextRejectsEmptyAndUnknownSession(t *testing.T) {
	binder := newSessionTextBinder()

	_, err := binder.ReadExternalEditSessionText("   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sessionId 不能为空")

	_, err = binder.ReadExternalEditSessionText("missing-session")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "外部编辑会话不存在")
}

func TestSaveExternalEditSessionTextRejectsEmptyAndUnknownSession(t *testing.T) {
	binder := newSessionTextBinder()

	_, err := binder.SaveExternalEditSessionText(SaveSessionTextRequest{Text: "hello"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sessionId 不能为空")

	_, err = binder.SaveExternalEditSessionText(SaveSessionTextRequest{SessionID: "missing-session", Text: "hello"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "外部编辑会话不存在")
}
