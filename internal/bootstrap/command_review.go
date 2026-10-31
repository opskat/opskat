package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/repository/command_review_repo"
	"github.com/opskat/opskat/internal/service/command_review_svc"
)

// registerCommandReview 注册命令的模型审核服务。桌面端和 opsctl 都经 Init 走到这里，
// 两边用同一份设置和同一个数据库缓存。
func registerCommandReview() {
	command_review_svc.Register(command_review_svc.New(
		CommandReviewTimeout,
		command_review_svc.NewRepoCache(command_review_repo.CommandReview()),
		activeCommandReviewer,
	))
}

// CommandReviewTimeout 是每次调用审核器的超时，没设置时用默认值。
func CommandReviewTimeout() time.Duration {
	if ms := GetConfig().CommandReviewTimeoutMs; ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return command_review_svc.DefaultTimeout
}

// 审核器来源还没接上：现在一个审核器都没有，每次审核都按 no_reviewer 失败
// （辅助审批问人、Autopilot 拒绝），设置页列出的审核器为空。

// activeCommandReviewer 找出这次要用的审核器。
func activeCommandReviewer(context.Context) (command_review_svc.ExtensionReviewer, error) {
	return nil, fmt.Errorf("%w: no command reviewer source is available", command_review_svc.ErrNoReviewer)
}

// CommandReviewState 是设置页和权限模式卡片看到的审核状态。
func CommandReviewState(context.Context) command_review_svc.State {
	// Reviewers 经 IPC 交给前端，空列表也要是数组：nil 切片编码成 null，前端遍历时就报错。
	return command_review_svc.State{Reviewers: []command_review_svc.ReviewerInfo{}, Problem: command_review_svc.ReasonNoReviewer}
}

// CommandReviewer 返回名为 name 的审核器，供设置页测试；不是可用的审核器时返回 ErrReviewerUnavailable。
func CommandReviewer(_ context.Context, name string) (command_review_svc.ExtensionReviewer, error) {
	return nil, fmt.Errorf("%w: %q is not an available command reviewer", command_review_svc.ErrReviewerUnavailable, name)
}
