package ssh

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFinalTransferEvent(t *testing.T) {
	t.Run("success is done and carries the local path", func(t *testing.T) {
		ev := finalTransferEvent("t1", "/tmp/a.txt", nil)
		assert.Equal(t, "done", ev.Status)
		assert.Equal(t, "/tmp/a.txt", ev.LocalPath)
		assert.Empty(t, ev.Error)
	})
	t.Run("a cancelled context is reported as cancelled, not error", func(t *testing.T) {
		ev := finalTransferEvent("t1", "/tmp/a.txt", fmt.Errorf("upload: %w", context.Canceled))
		assert.Equal(t, "cancelled", ev.Status)
	})
	t.Run("other failures keep their message", func(t *testing.T) {
		ev := finalTransferEvent("t1", "/tmp/a.txt", errors.New("permission denied"))
		assert.Equal(t, "error", ev.Status)
		assert.Equal(t, "permission denied", ev.Error)
	})
}
