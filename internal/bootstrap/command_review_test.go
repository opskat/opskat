package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/opskat/opskat/internal/service/command_review_svc"
)

func TestCommandReviewTimeoutDefaultsWhenUnset(t *testing.T) {
	orig := appConfig
	t.Cleanup(func() { appConfig = orig })

	appConfig = &AppConfig{}
	assert.Equal(t, command_review_svc.DefaultTimeout, CommandReviewTimeout())
	appConfig = &AppConfig{CommandReviewTimeoutMs: 8000}
	assert.Equal(t, 8*time.Second, CommandReviewTimeout())
}

// 没有扩展系统的进程（比如只做本地操作的工具）没有审核扩展可用：审核失败，不会因此放行。
func TestNoExtensionSystemMeansNoReviewer(t *testing.T) {
	orig := extensionService
	t.Cleanup(func() { extensionService = orig })
	extensionService = nil

	_, err := activeCommandReviewer(context.Background())
	assert.ErrorIs(t, err, command_review_svc.ErrNoReviewer)
}
