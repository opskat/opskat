package helper

import (
	"context"
	"errors"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
)

// LocalWriteGate 是"一次本地写要先过 local_write 门禁"的接缝。
//
// 实现是 internal/ai/tool.LocalToolGate —— 模型直接调用 local_write 工具时走的就是它，
// 两条路共用同一份会话白名单、同一个对话框、同一套 pattern 语义。接口留在本包，是因为
// 被它保护的那个原语（localAdapter.Write：MkdirAll + os.Create + io.Copy）就在本包，
// 而 helper 不能反向 import tool。
//
// **它不是通用的本地写检查**：本地端点照旧不产生审批项（spec §6.2）。它只补 D11 的前提
// 不成立的那一档 —— D11 说"本地路径由用户批准的那条命令串完全决定"，而当两端都被策略/
// grant 自动放行时，压根没有"用户批准的那条串"，于是本地写需要另一道门。
type LocalWriteGate interface {
	// CheckLocalWrites 让一批本地写路径过一次 local_write 门禁：返回 nil 即放行，
	// 非 nil 是拒绝理由（可直接回给模型）。paths 必须非空。
	CheckLocalWrites(ctx context.Context, paths []string, detail string) error
}

type localWriteGateKeyType struct{}

// WithLocalWriteGate 把本地写门禁注入 ctx。由 internal/app/ai 在每次 Send 时注入同一个
// LocalToolGate 实例 —— 白名单按 convID 分片，因此"本次会话允许"在两条路上是同一件事。
func WithLocalWriteGate(ctx context.Context, gate LocalWriteGate) context.Context {
	return context.WithValue(ctx, localWriteGateKeyType{}, gate)
}

// RequireLocalWriteGate 取出本地写门禁，缺失即报错 —— 不放行。
//
// 与 permission.RequireChecker 同一条理由（#249）：把"注入缺失"写成放行，漏接线就等于
// 没有门禁。而调用方走到这里时，恰恰是"这次操作一个弹框都没有过"的场合，它是仅剩的那道门。
func RequireLocalWriteGate(ctx context.Context) (LocalWriteGate, error) {
	if gate, ok := ctx.Value(localWriteGateKeyType{}).(LocalWriteGate); ok && gate != nil {
		return gate, nil
	}
	return nil, errors.New("local write gate not available")
}

// gateExecLocalWrite 是 exec 面上"这条命令要往本机写一个文件"的那道门（目前只有 OSS 的
// `object get --file=P`）。判据与传输面的 cp 逐字相同，只是"有没有弹过框"的来源不同：
// exec 的权限检查在 handleExec 里，判定写在审计槽上，执行器读的就是那一格。
//
//   - opsctl（WithPreapproved，没有 checker）：本地路径写在用户自己敲的命令串里，不问。
//   - 判定来源是 user_allow：用户刚批准过这条完整命令串（含 --file=，D11 强制绝对路径），
//     前提成立，不问。
//   - 其余（policy_allow / grant_allow，以及槽压根没挂上）：这次执行一个弹框都没有过，
//     本地写因此过一次 local_write 门禁。
func gateExecLocalWrite(ctx context.Context, path, detail string) error {
	checker, err := permission.RequireCheckerOrPreapproved(ctx)
	if err != nil {
		return err
	}
	if checker == nil {
		return nil
	}
	if res := aictx.GetCheckResult(ctx); res != nil && res.DecisionSource == aictx.SourceUserAllow {
		return nil
	}
	gate, err := RequireLocalWriteGate(ctx)
	if err != nil {
		return err
	}
	if err := gate.CheckLocalWrites(ctx, []string{path}, detail); err != nil {
		// 被门禁挡下的命令什么都没写，审计行不能停在权限检查记下的 allow 上。
		aictx.RecordDecision(ctx, aictx.CheckResult{
			Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny, Message: err.Error(),
		})
		return err
	}
	return nil
}
