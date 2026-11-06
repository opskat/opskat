package extension_data_repo_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/model/entity/extension_data_entity"
	"github.com/opskat/opskat/internal/repository/extension_data_repo"
)

// openProcesses stands in for the desktop app and opsctl: n independent database
// handles (separate connection pools) on one SQLite file, opened with the DSN the
// app uses. Each returned context routes the repo to its own handle.
func openProcesses(t *testing.T, n int) (ctxs []context.Context, handles []*gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opskat.db")
	for i := 0; i < n; i++ {
		gdb, err := gorm.Open(sqlite.Open(bootstrap.SQLiteDSN(path)), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := gdb.DB()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })
		if i == 0 {
			require.NoError(t, gdb.AutoMigrate(&extension_data_entity.ExtensionData{}))
		}
		ctxs = append(ctxs, db.WithContextDB(context.Background(), gdb))
		handles = append(handles, gdb)
	}
	return ctxs, handles
}

func TestConcurrentSetFromTwoProcessesNeverExceedsQuota(t *testing.T) {
	ctxs, handles := openProcesses(t, 2)
	repo := extension_data_repo.NewExtensionData()

	value := []byte(strings.Repeat("x", extension_data_repo.MaxKVValueSize))
	capacity := extension_data_repo.MaxKVPerExtension / extension_data_repo.MaxKVValueSize
	const perProcess = 12 // 24 distinct 1MB keys race for `capacity` slots

	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok int
	var unexpected []error
	for p, ctx := range ctxs {
		for i := 0; i < perProcess; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := repo.Set(ctx, "ext", fmt.Sprintf("k-%d-%d", p, i), value)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					ok++
				case strings.Contains(err.Error(), "quota exceeded"):
				default:
					unexpected = append(unexpected, err)
				}
			}()
		}
	}
	wg.Wait()

	require.Empty(t, unexpected, "writers must wait for each other, not fail")
	var rows int64
	require.NoError(t, handles[0].Model(&extension_data_entity.ExtensionData{}).Count(&rows).Error)
	require.Equal(t, int64(capacity), rows, "quota admits exactly %d 1MB values", capacity)
	require.Equal(t, capacity, ok)
}

func TestConcurrentSetOfSameKeyFromTwoProcessesKeepsOneRowWithLaterValue(t *testing.T) {
	ctxs, handles := openProcesses(t, 2)
	repo := extension_data_repo.NewExtensionData()

	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = repo.Set(ctxs[i%2], "ext", "shared", []byte(fmt.Sprintf("v%d", i)))
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err, "both writers of the same key must succeed")
	}

	var rows []extension_data_entity.ExtensionData
	require.NoError(t, handles[0].Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Regexp(t, `^v\d+$`, string(rows[0].Value))

	// Sequential overwrite from the other process: last writer's value wins.
	require.NoError(t, repo.Set(ctxs[0], "ext", "shared", []byte("first")))
	require.NoError(t, repo.Set(ctxs[1], "ext", "shared", []byte("last")))
	got, err := repo.Get(ctxs[0], "ext", "shared")
	require.NoError(t, err)
	require.Equal(t, "last", string(got))
}
