package command

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/approval"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/command_review_svc"
)

// 多源 cp 的多条主体合进同一个批量确认，审计只有一行：人确认之后，这一行也要带上审核结果，
// 说明为什么问了人（设计："审核过的行带审核标志"）。
func TestRequireCpBatchApprovalKeepsReviewForAudit(t *testing.T) {
	for _, c := range []struct {
		name    string
		approve bool
	}{
		{"approved", true},
		{"denied", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			restoreAssetRepoAfter(t)
			env := setupOpsctlExecAssets(t)
			setAssetPermissionMode(t, env, "web-1", policyent.PermissionModeAssisted)

			reviewer := &fakeReviewer{result: command_review_svc.Result{
				Outcome: command_review_svc.OutcomeReject, Failed: []string{command_review_svc.QuestionDestructive},
			}}
			origReviewer := command_review_svc.Default()
			command_review_svc.Register(reviewer)
			t.Cleanup(func() { command_review_svc.Register(origReviewer) })

			origSend := cpBatchSendFn
			cpBatchSendFn = func(items []approval.BatchItem, session string) (ApprovalResult, error) {
				if c.approve {
					return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: session}, nil
				}
				return ApprovalResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny, SessionID: session}, errors.New("batch denied: denied")
			}
			t.Cleanup(func() { cpBatchSendFn = origSend })

			res, _ := requireCpBatchApproval(context.Background(), []cpSubject{
				{approvalType: "cp:write", assetID: 2, assetName: "web-1", command: "/etc/app/a.conf"},
				{approvalType: "cp:write", assetID: 2, assetName: "web-1", command: "/etc/app/b.conf"},
			}, "./conf/ → web-1:/etc/app/")

			if reviewer.reviewed == 0 {
				t.Fatal("the subjects were never reviewed; the test is not exercising assisted mode")
			}
			if res.Review == nil || res.Review.Outcome != aictx.ReviewReject {
				t.Fatalf("result review = %+v, want the reject review that sent the batch to a person", res.Review)
			}
		})
	}
}
