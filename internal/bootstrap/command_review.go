package bootstrap

import (
	"context"
	"time"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/repository/command_review_repo"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/internal/service/command_review_svc/reviewerreg"
)

// commandReviewers 是审核服务的审核器来源：进程的审核器注册表（commandReviewer 贡献点
// 填充，main.go 与 opsctl 注册同一个 reviewerreg.Default().Point()），按设置里选用的扩展名挑。
var commandReviewers = reviewerreg.Default().Selecting(func() string { return GetConfig().CommandReviewExtension })

// registerCommandReview 注册命令的模型审核服务，并把它作为审核端口注入权限检查。
// 桌面端和 opsctl 都经 Init 走到这里，两边用同一份设置和同一个数据库缓存。
func registerCommandReview() {
	svc := command_review_svc.New(
		CommandReviewTimeout,
		command_review_svc.NewRepoCache(command_review_repo.CommandReview()),
		commandReviewers,
	)
	command_review_svc.Register(svc)
	permission.SetReviewer(svc)
}

// CommandReviewTimeout 是每次调用审核器的超时，没设置时用默认值。
func CommandReviewTimeout() time.Duration {
	if ms := GetConfig().CommandReviewTimeoutMs; ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return command_review_svc.DefaultTimeout
}

// CommandReviewState 是设置页和权限模式卡片看到的审核状态，审核器的显示名按 lang。
func CommandReviewState(ctx context.Context, lang string) command_review_svc.State {
	return commandReviewers.State(ctx, lang)
}

// CommandReviewer 返回名为 name 的审核器（选没选用都行），供设置页测试；不是现在能用的
// 审核器时返回 ErrReviewerUnavailable。
func CommandReviewer(ctx context.Context, name string) (command_review_svc.Reviewer, error) {
	return commandReviewers.Reviewer(ctx, name)
}
