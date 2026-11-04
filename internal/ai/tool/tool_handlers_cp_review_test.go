package tool

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"

	. "github.com/smartystreets/goconvey/convey"
)

// rejectReviewer 是审核端口的替身：每条命令都审核未通过（可能破坏数据）。
type rejectReviewer struct{}

func (rejectReviewer) ReviewCommands(_ context.Context, reqs []permission.ReviewRequest) []aictx.ReviewInfo {
	out := make([]aictx.ReviewInfo, len(reqs))
	for i := range out {
		out[i] = aictx.ReviewInfo{Outcome: aictx.ReviewReject, Failed: []string{"destructive"}}
	}
	return out
}

// useReviewer 把审核端口换成 r，测试结束后撤掉。
func useReviewer(t *testing.T, r permission.Reviewer) {
	t.Helper()
	permission.SetReviewer(r)
	t.Cleanup(func() { permission.SetReviewer(nil) })
}

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

	useReviewer(t, rejectReviewer{})
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

// countingReviewer 同 rejectReviewer，另外数审核端口被调用了几次、共审了几条。
type countingReviewer struct {
	rejectReviewer
	batches, reviewed int
}

func (r *countingReviewer) ReviewCommands(ctx context.Context, reqs []permission.ReviewRequest) []aictx.ReviewInfo {
	r.batches++
	r.reviewed += len(reqs)
	return r.rejectReviewer.ReviewCommands(ctx, reqs)
}

// 一次传输的各条主体一次送审：审核是并行的，逐条送就成了串行，每条最多要等两倍超时。
func TestCpReviewsAllSubjectsInOneBatch(t *testing.T) {
	Convey("源与目的都在辅助审批资产上的递归 cp，读写两条主体一次送审", t, func() {
		ctx, _ := setupCpDialogs(t, "allow", "allow")
		useAssistedSink(t)
		reviewer := &countingReviewer{}
		useReviewer(t, reviewer)
		seedCpSource("a.log", "b.log")

		_, _ = handleCp(ctx, map[string]any{"src": "sink-01:/src/", "dst": "sink-01:/backup/", "recursive": true})
		So(reviewer.reviewed, ShouldBeGreaterThan, 1)
		So(reviewer.batches, ShouldEqual, 1)
	})
}
