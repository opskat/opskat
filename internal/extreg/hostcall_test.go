package extreg

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// CallHostTool is how a host subsystem calls a host-only tool of a loaded
// extension: through the real runtime, arguments checked against the tool's
// schema, the caller's timeout clamped to the tool's own limit.
func TestCallHostTool(t *testing.T) {
	ext := extensiontest.LoadFixture(t)
	require.NoError(t, attachExtension(ext))
	t.Cleanup(func() { detach(ext.Name) })
	ctx := context.Background()

	t.Run("calls a host-only tool and returns its result", func(t *testing.T) {
		out, err := CallHostTool(ctx, ext.Name, "host_echo", json.RawMessage(`{"msg":"hi"}`), 0)
		require.NoError(t, err)
		assert.Contains(t, string(out), "hi")
	})

	t.Run("refuses a tool that is not host-only, an unknown tool and an unknown extension", func(t *testing.T) {
		_, err := CallHostTool(ctx, ext.Name, "echo", json.RawMessage(`{"msg":"hi"}`), 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "echo")
		_, err = CallHostTool(ctx, ext.Name, "nope", json.RawMessage(`{}`), 0)
		require.Error(t, err)
		_, err = CallHostTool(ctx, "no-such-ext", "host_echo", json.RawMessage(`{}`), 0)
		require.Error(t, err)
	})

	t.Run("validates arguments with the tool's schema", func(t *testing.T) {
		_, err := CallHostTool(ctx, ext.Name, "host_echo", json.RawMessage(`{"bogus":1}`), 0)
		require.Error(t, err)
		_, err = CallHostTool(ctx, ext.Name, "host_echo", json.RawMessage(`{"msg":5}`), 0)
		require.Error(t, err)
	})

	t.Run("a shorter caller timeout wins, a longer one is clamped to the tool's limit", func(t *testing.T) {
		start := time.Now()
		_, err := CallHostTool(ctx, ext.Name, "host_spin", json.RawMessage(`{"ms":20000}`), 300*time.Millisecond)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deadline exceeded")
		assert.Less(t, time.Since(start), 2*time.Second, "cut off by the caller's 300ms, not the tool's 3s")

		start = time.Now()
		_, err = CallHostTool(ctx, ext.Name, "host_spin", json.RawMessage(`{"ms":20000}`), time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deadline exceeded")
		elapsed := time.Since(start)
		assert.GreaterOrEqual(t, elapsed, 2*time.Second, "an hour is not honoured: the tool's 3s limit applies")
		assert.Less(t, elapsed, 10*time.Second)
	})

	t.Run("a revoked extension cannot be called", func(t *testing.T) {
		detach(ext.Name)
		_, err := CallHostTool(ctx, ext.Name, "host_echo", json.RawMessage(`{}`), 0)
		require.Error(t, err)
	})
}
