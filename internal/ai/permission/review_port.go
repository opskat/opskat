package permission

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
)

// Syntax 是命令的写法，随命令交给审核器；shell 写法在送审前展开解码后执行的脚本（见 applyReviews）。
// 内置类型注册时声明，扩展资产类型按纯文本。
type Syntax string

const (
	SyntaxText  Syntax = "text"  // 纯文本：SQL、路径、不经过 shell 执行的命令 DSL
	SyntaxShell Syntax = "shell" // shell 命令
	SyntaxRedis Syntax = "redis" // Redis 命令
)

// ReviewRequest 是交给审核端口的一条命令：规范资产类型、原命令、写法，以及权限检查从 shell
// 命令里解出的、解码后会执行的脚本（shelldecode.Decode，已过禁止规则）。审核器收到的是 Text()。
type ReviewRequest struct {
	AssetType string
	Command   string
	Syntax    Syntax
	Decoded   []string
}

// Text 是送审的文本，审核器的单条长度、缓存键和审核器调用都用它：原命令原样开头，一个字符
// 都不改；解码出的脚本依次追加在后面，各自标明是解码出来的。
func (r ReviewRequest) Text() string {
	if len(r.Decoded) == 0 {
		return r.Command
	}
	var b strings.Builder
	b.WriteString(r.Command)
	for i, script := range r.Decoded {
		fmt.Fprintf(&b, "\n\n# Decoded script %d of %d: the command above decodes this and runs it\n%s", i+1, len(r.Decoded), script)
	}
	return b.String()
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
