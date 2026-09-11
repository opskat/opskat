package permission

import (
	"context"
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

// TestHandleConfirm_ZeroPatternWarningOmitsCommandKeepsCorrelation locks the
// grant-degradation log sink (spec task 6, decision D20): when "always allow"
// normalizes to zero grant patterns, checker.go's warning must not record the command
// payload at all while keeping assetID/assetType correlation.
//
// A system subject that normalizes to zero patterns only gets a one-shot approval
// (ApprovalKindFor), so the zero-pattern branch is reached through the user's edit: the
// requested subject is matchable, the edited pattern is not (unknown OSS verb → no policy
// strings) and carries a secret token, so a regression that logs it (raw or redacted) fails here.
func TestHandleConfirm_ZeroPatternWarningOmitsCommandKeepsCorrelation(t *testing.T) {
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
	t.Cleanup(func() { grant_repo.RegisterGrant(orig) })

	asset := &asset_entity.Asset{ID: 1, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	secret := "mt-" + "zero-warning-token"
	checker := NewCommandPolicyChecker(func(_ context.Context, _ string, items []ApprovalItem) ApprovalResponse {
		edited := items[0]
		edited.Command = "object frobnicate mybucket/a --token=" + secret
		return ApprovalResponse{Decision: "allowAll", EditedItems: []ApprovalItem{edited}}
	})
	got := checker.HandleConfirm(aictx.WithSessionID(ctx, "sess-zero"), 1,
		asset_entity.AssetTypeOSS, "object stat mybucket/report.txt")
	assert.Equal(t, aictx.Allow, got.Decision, "the operation itself is still approved; only the grant is dropped")

	var warningSeen bool
	for _, le := range logs.All() {
		if le.Message == "always-allow approved but normalized to zero grant patterns; nothing persisted" {
			warningSeen = true
			cm := le.ContextMap()
			_, commandLogged := cm["command"]
			assert.False(t, commandLogged, "zero-pattern warning must not record the command payload")
			assert.Equal(t, int64(1), cm["assetID"], "assetID correlation must be retained")
			assert.Equal(t, string(asset_entity.AssetTypeOSS), cm["assetType"], "assetType correlation must be retained")
		}
	}
	assert.True(t, warningSeen, "the zero-pattern warning must actually have fired")
}
