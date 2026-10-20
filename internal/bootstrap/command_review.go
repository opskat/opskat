package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/repository/command_review_repo"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/internal/service/extension_svc"
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

// CommandReviewTimeout 是每次调用审核扩展的超时，没设置时用默认值。
func CommandReviewTimeout() time.Duration {
	if ms := GetConfig().CommandReviewTimeoutMs; ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return command_review_svc.DefaultTimeout
}

// extensionService 由扩展系统初始化后注入（桌面端在 main.go，opsctl 在注册扩展时）。
var extensionService *extension_svc.Service

// SetExtensionService 注入扩展服务，供审核找出要用的审核扩展。
func SetExtensionService(svc *extension_svc.Service) { extensionService = svc }

// activeCommandReviewer 找出这次要用的审核扩展：设置里选了就用选的，没选时启用中的只有一个就用它。
// 没有可用的返回 ErrNoReviewer；要用的这次加载不了或来源/摘要检查不过返回 ErrReviewerUnavailable。
func activeCommandReviewer(ctx context.Context) (command_review_svc.ExtensionReviewer, error) {
	if extensionService == nil {
		return nil, fmt.Errorf("%w: the extension system is not initialized in this process", command_review_svc.ErrNoReviewer)
	}
	name, err := extensionService.CommandReviewerName(ctx, GetConfig().CommandReviewExtension)
	if errors.Is(err, extension_svc.ErrNoCommandReviewer) || errors.Is(err, extension_svc.ErrSeveralCommandReviewers) {
		return nil, fmt.Errorf("%w: %s", command_review_svc.ErrNoReviewer, err.Error())
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", command_review_svc.ErrReviewerUnavailable, err.Error())
	}
	active, err := extensionService.ActiveCommandReviewer(ctx, name)
	if err != nil {
		logger.Ctx(ctx).Warn("command reviewer unavailable", zap.String("extension", name), zap.Error(err))
		return nil, fmt.Errorf("%w: %s", command_review_svc.ErrReviewerUnavailable, err.Error())
	}
	return active, nil
}
