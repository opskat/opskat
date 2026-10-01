package tool

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/command_review_svc"

	. "github.com/smartystreets/goconvey/convey"
)

// rejectReviewer 让每条命令都审核未通过（可能破坏数据）。
type rejectReviewer struct{}

func (r rejectReviewer) Review(ctx context.Context, in command_review_svc.Input) command_review_svc.Result {
	return r.ReviewBatch(ctx, []command_review_svc.Input{in})[0]
}

func (rejectReviewer) ReviewBatch(_ context.Context, ins []command_review_svc.Input) []command_review_svc.Result {
	out := make([]command_review_svc.Result, len(ins))
	for i := range out {
		out[i] = command_review_svc.Result{Outcome: command_review_svc.OutcomeReject, Failed: []string{command_review_svc.QuestionDestructive}}
	}
	return out
}

func (rejectReviewer) TestModel(context.Context, command_review_svc.Config) (string, error) {
	return "", nil
}
func (rejectReviewer) Status() command_review_svc.Status   { return command_review_svc.Status{} }
func (rejectReviewer) SetConfigErrorListener(func(string)) {}

// useAssistedSink 把 sink-01 设成辅助审批，审核一律不通过：它的每条传输主体都会带着审核结果
// 交给人确认。在 setupCp 之后调用，覆盖它注册的资产。
func useAssistedSink(t *testing.T) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := mock_asset_repo.NewMockAssetRepo(ctrl)
	for _, a := range cpTestAssets() {
		if a.Name == "sink-01" {
			a.PermissionMode = policyent.PermissionModeAssisted
		}
		m.EXPECT().Find(gomock.Any(), a.ID).Return(a, nil).AnyTimes()
		m.EXPECT().FindByName(gomock.Any(), a.Name).Return([]*asset_entity.Asset{a}, nil).AnyTimes()
	}
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(m)
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })

	origReviewer := command_review_svc.Default()
	command_review_svc.Register(rejectReviewer{})
	t.Cleanup(func() { command_review_svc.Register(origReviewer) })
}

// 多条主体合进同一个批量确认时，审计只有一行：人确认之后，这一行也要带上审核结果，
// 说明为什么问了人（设计："审核过的行带审核标志"）。
func TestCpBatchConfirmationKeepsReviewForAudit(t *testing.T) {
	for _, c := range []struct {
		batchDecision string
		source        string
	}{
		{"allow", aictx.SourceUserAllow},
		{"deny", aictx.SourceUserDeny},
	} {
		Convey("辅助审批下审核未通过的范围，批量确认（"+c.batchDecision+"）后审计带审核结果", t, func() {
			ctx, calls := setupCpDialogs(t, "allow", c.batchDecision)
			useAssistedSink(t)
			seedCpSource("a.log", "b.log")
			var slot aictx.CheckResult
			ctx = aictx.WithCheckResultSlot(ctx, &slot)

			_, _ = handleCp(ctx, map[string]any{"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true})

			So(cpBatchCalls(*calls), ShouldHaveLength, 1)
			So(slot.DecisionSource, ShouldEqual, c.source)
			So(slot.Review, ShouldNotBeNil)
			So(slot.Review.Outcome, ShouldEqual, aictx.ReviewReject)
		})
	}
}
