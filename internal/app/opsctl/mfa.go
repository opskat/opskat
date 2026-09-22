package opsctl

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/opskat/opskat/internal/approval"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// mfaCanceledReason 是用户在桌面 MFA 对话框里取消 / 关闭时回给 opsctl 的原因。
const mfaCanceledReason = "mfa canceled"

// mfaBroker 承接 opsctl 经 approval.sock 发来的 SSH MFA 挑战（type="mfa"）：发
// opsctl:mfa 事件让前端弹出全局对话框，等前端经 RespondOpsctlMFA / CancelOpsctlMFA
// 回答。请求方断开（ctx 结束）时发 opsctl:mfa-closed 让前端关掉对应对话框。
// 答案只在 channel 里经手一次，绝不写日志。
type mfaBroker struct {
	emit     func(name string, payload map[string]any)
	activate func()
	seq      atomic.Uint64
	pending  sync.Map // challengeID → *pendingMFA
}

type pendingMFA struct {
	prompts int
	ch      chan mfaReply
}

type mfaReply struct {
	answers  []string
	canceled bool
}

func newMFABroker(emit func(string, map[string]any), activate func()) *mfaBroker {
	return &mfaBroker{emit: emit, activate: activate}
}

func (b *mfaBroker) challenge(ctx context.Context, req approval.ApprovalRequest) approval.ApprovalResponse {
	if req.MFA == nil || len(req.MFA.Prompts) == 0 {
		return approval.ApprovalResponse{Approved: false, Reason: "invalid mfa request"}
	}
	id := fmt.Sprintf("opsctl_mfa_%d", b.seq.Add(1))
	log := logger.Ctx(ctx).With(zap.String("challengeID", id), zap.Int64("assetID", req.AssetID))
	p := &pendingMFA{prompts: len(req.MFA.Prompts), ch: make(chan mfaReply, 1)}
	b.pending.Store(id, p)
	defer b.pending.Delete(id)

	log.Info("opsctl mfa challenge started", zap.Int("prompts", p.prompts))
	b.activate()
	b.emit("opsctl:mfa", map[string]any{
		"challenge_id": id,
		"asset_id":     req.AssetID,
		"asset_name":   req.AssetName,
		"name":         req.MFA.Name,
		"instruction":  req.MFA.Instruction,
		"prompts":      req.MFA.Prompts,
		"echo":         req.MFA.Echo,
	})

	select {
	case reply := <-p.ch:
		if reply.canceled {
			log.Info("opsctl mfa challenge completed", zap.Bool("answered", false))
			return approval.ApprovalResponse{Approved: false, Reason: mfaCanceledReason}
		}
		log.Info("opsctl mfa challenge completed", zap.Bool("answered", true))
		return approval.ApprovalResponse{Approved: true, MFAAnswers: reply.answers}
	case <-ctx.Done():
		b.pending.Delete(id)
		b.emit("opsctl:mfa-closed", map[string]any{"challenge_id": id})
		log.Info("opsctl mfa challenge closed", zap.String("reason", "requester gone"))
		return approval.ApprovalResponse{Approved: false, Reason: "requester gone"}
	}
}

// respond 是前端提交答案的 IPC 边界：挑战必须仍在等待，且答案数与提示数一致。
func (b *mfaBroker) respond(id string, answers []string) error {
	v, ok := b.pending.LoadAndDelete(id)
	if !ok {
		return fmt.Errorf("mfa challenge %s is no longer pending", id)
	}
	p := v.(*pendingMFA)
	if len(answers) != p.prompts {
		b.pending.Store(id, p)
		return fmt.Errorf("mfa challenge %s expects %d answers, got %d", id, p.prompts, len(answers))
	}
	p.ch <- mfaReply{answers: answers}
	return nil
}

// cancel 是前端取消 / 关闭对话框的 IPC 边界；挑战已结束时无事可做。
func (b *mfaBroker) cancel(id string) {
	if v, ok := b.pending.LoadAndDelete(id); ok {
		v.(*pendingMFA).ch <- mfaReply{canceled: true}
	}
}

// RespondOpsctlMFA 前端提交 opsctl MFA 挑战的答案（按提示顺序）。
func (o *Opsctl) RespondOpsctlMFA(challengeID string, answers []string) error {
	return o.mfa.respond(challengeID, answers)
}

// CancelOpsctlMFA 前端取消 / 关闭 opsctl MFA 对话框。
func (o *Opsctl) CancelOpsctlMFA(challengeID string) {
	o.mfa.cancel(challengeID)
}
