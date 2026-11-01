package extension_settings_repo

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/opskat/opskat/internal/model/entity/extension_settings_entity"
)

// errorRecorder is a GORM logger that records every statement GORM would log as an error.
type errorRecorder struct{ errs []error }

func (l *errorRecorder) LogMode(logger.LogLevel) logger.Interface { return l }
func (l *errorRecorder) Info(context.Context, string, ...any)     {}
func (l *errorRecorder) Warn(context.Context, string, ...any)     {}
func (l *errorRecorder) Error(context.Context, string, ...any)    {}
func (l *errorRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), err error) {
	if err != nil {
		l.errs = append(l.errs, err)
	}
}

var errLog = &errorRecorder{}

func setupRepo(t *testing.T) (context.Context, ExtensionSettingsRepo) {
	t.Helper()
	errLog.errs = nil
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: errLog})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&extension_settings_entity.ExtensionSettings{}))
	return db.WithContextDB(context.Background(), gdb), NewExtensionSettings()
}

func TestFindReturnsNilWhenNothingIsStored(t *testing.T) {
	ctx, r := setupRepo(t)
	got, err := r.Find(ctx, "jev")
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.Empty(t, errLog.errs, "a miss is an ordinary result and must not be logged as an error")
}

func TestSaveReplacesTheExtensionsOneRow(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Save(ctx, "jev", `{"a":1}`))
	require.NoError(t, r.Save(ctx, "jev", `{"a":2}`))
	require.NoError(t, r.Save(ctx, "other", `{"b":1}`))

	got, err := r.Find(ctx, "jev")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, `{"a":2}`, got.Value)
	assert.NotZero(t, got.Updatetime)
}

func TestDeleteRemovesOnlyThatExtension(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Save(ctx, "jev", `{"a":1}`))
	require.NoError(t, r.Save(ctx, "other", `{"b":1}`))
	require.NoError(t, r.Delete(ctx, "jev"))

	got, err := r.Find(ctx, "jev")
	require.NoError(t, err)
	assert.Nil(t, got)
	kept, err := r.Find(ctx, "other")
	require.NoError(t, err)
	require.NotNil(t, kept)
}
