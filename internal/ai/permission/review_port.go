package permission

import (
	"context"

	"github.com/opskat/opskat/internal/ai/aictx"
)

// Syntax 是命令的写法，随命令交给审核器；shell 写法在送审前判断是否既解码又执行（见 applyReviews）。
// 内置类型注册时声明，扩展资产类型按纯文本。
type Syntax string

const (
	SyntaxText  Syntax = "text"  // 纯文本：SQL、路径、不经过 shell 执行的命令 DSL
	SyntaxShell Syntax = "shell" // shell 命令
	SyntaxRedis Syntax = "redis" // Redis 命令
)

// ReviewRequest 是交给审核端口的一条命令：规范资产类型、原命令和写法。审核器收到的就是
// Command，原样，不改写也不追加；单条长度与缓存键也按它算。
type ReviewRequest struct {
	AssetType string
	Command   string
	Syntax    Syntax
}

// Reviewer 是命令审核端口：权限检查把规则判为"需要确认"、权限模式又需要审核的命令交给它。
// 结果与 reqs 一一对应；Mode 由权限检查填写，端口留空。没能得到判断时给 Outcome 为
// aictx.ReviewFail 的结果并写明原因，不会因此放行。
type Reviewer interface {
	ReviewCommands(ctx context.Context, reqs []ReviewRequest) []aictx.ReviewInfo
}

var reviewer Reviewer

// SetReviewer 注入审核端口，由启动代码调用。没有注入时（例如测试），需要审核的命令一律按
// 审核失败（no_reviewer）处理：辅助审批问人，Autopilot 拒绝。
func SetReviewer(r Reviewer) { reviewer = r }

// reviewCommands 经注入的端口审核 reqs；没有注入时每条都按没有审核器失败。
func reviewCommands(ctx context.Context, reqs []ReviewRequest) []aictx.ReviewInfo {
	if reviewer != nil {
		return reviewer.ReviewCommands(ctx, reqs)
	}
	out := make([]aictx.ReviewInfo, len(reqs))
	for i := range out {
		out[i] = aictx.ReviewInfo{Outcome: aictx.ReviewFail, Reason: aictx.ReviewReasonNoReviewer}
	}
	return out
}
