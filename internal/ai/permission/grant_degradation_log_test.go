package permission

import (
	"context"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

// TestHandleConfirm_ZeroPatternWarningRedactsCommand locks the grant-degradation log
// sink (spec task 3, decision D20): when "always allow" normalizes to zero grant
// patterns, checker.go's warning logs the original command — a long-term ops surface
// that must go through the canonical redactor like the audit row does. The synthetic
// command hits the zero-pattern branch (unknown OSS verb → no policy strings) while
// carrying a secret token, so a regression that logs it verbatim fails here.
func TestHandleConfirm_ZeroPatternWarningRedactsCommand(t *testing.T) {
	// Capture logger.Default() (logger.Ctx falls back to it when ctx carries no logger).
	core, logs := observer.New(zap.DebugLevel)
	origLogger := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(origLogger) })

	withOSSPolicyStrings(t)
	_ = withStubAudit(t) // grant_discarded audit write needs a non-nil repo
	ctx, mockAsset, _ := setupPolicyTest(t)

	stubGrant := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stubGrant)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})

	asset := &asset_entity.Asset{ID: 1, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	secret := "mt-" + "zero-warning-token"
	checker := NewCommandPolicyChecker(func(context.Context, string, []ApprovalItem) ApprovalResponse {
		return ApprovalResponse{Decision: "allowAll"}
	})
	got := checker.HandleConfirm(aictx.WithSessionID(ctx, "sess-zero"), 1,
		asset_entity.AssetTypeOSS, "object frobnicate mybucket/a --token="+secret)
	assert.Equal(t, aictx.Allow, got.Decision, "the operation itself is still approved; only the grant is dropped")

	var warningSeen bool
	for _, le := range logs.All() {
		if le.Message == "always-allow approved but normalized to zero grant patterns; nothing persisted" {
			warningSeen = true
			if cmd, ok := le.ContextMap()["command"].(string); ok {
				assert.False(t, strings.Contains(cmd, secret),
					"zero-pattern warning logged raw command secret: %q", cmd)
			}
		}
	}
	assert.True(t, warningSeen, "the zero-pattern warning must actually have fired")
}
