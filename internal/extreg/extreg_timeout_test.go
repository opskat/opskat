package extreg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// TestExecHonoursToolDeclaredTimeout drives an extension tool through the
// executor the unified exec registers for the asset type — the path AI exec and
// opsctl's delegated exec both run — and shows the tool's own declared timeout,
// not the host's 30s default, is what stops it.
func TestExecHonoursToolDeclaredTimeout(t *testing.T) {
	ext := extensiontest.LoadFixture(t)
	require.NoError(t, Register(ext))
	t.Cleanup(func() { Unregister(ext.Name) })

	execFn, ok := permission.ExecutorFor("fixture")
	require.True(t, ok)
	asset := &asset_entity.Asset{ID: 1, Name: "fixture-1", Type: "fixture"}

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := execFn(context.Background(), asset, "spin_short --ms=20000", "")
		done <- err
	}()
	select {
	case err := <-done:
		elapsed := time.Since(start)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deadline exceeded")
		assert.Less(t, elapsed, 5*time.Second, "cut off by the tool's 300ms declaration, not the 30s default")
	case <-time.After(10 * time.Second):
		t.Fatal("exec of a tool declaring a 300ms timeout did not return within 10s")
	}
}
