package extension

import (
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/command_review_svc"
)

// GetCommandReviewState 返回审核状态：提供命令审核器的扩展（审核器注册表里的候选）、设置里选的，
// 以及实际在用的，没有可用的时说明原因。
func (e *Extension) GetCommandReviewState() (*command_review_svc.State, error) {
	st := bootstrap.CommandReviewState(e.ctx, e.lang.Lang())
	return &st, nil
}

// SetCommandReviewer 选用审核器；name 为空表示不指定（候选只有一个时用它）。
// 只能选现在能用的审核器：选一个用不了的，之后每次审核都会失败。
func (e *Extension) SetCommandReviewer(name string) error {
	if name != "" {
		if err := checkSelectableReviewer(bootstrap.CommandReviewState(e.ctx, e.lang.Lang()).Reviewers, name); err != nil {
			logger.Ctx(e.ctx).Warn("select command reviewer refused", zap.String("extension", name), zap.Error(err))
			return err
		}
	}
	cfg := bootstrap.GetConfig()
	cfg.CommandReviewExtension = name
	if err := bootstrap.SaveConfig(cfg); err != nil {
		logger.Ctx(e.ctx).Error("save command reviewer failed", zap.String("extension", name), zap.Error(err))
		return err
	}
	logger.Ctx(e.ctx).Info("command reviewer selected", zap.String("extension", name))
	return nil
}

func checkSelectableReviewer(reviewers []command_review_svc.ReviewerInfo, name string) error {
	for _, r := range reviewers {
		if r.Name != name {
			continue
		}
		if !r.Usable {
			return fmt.Errorf("%s 的命令审核器现在不能使用：%s", name, r.Reason)
		}
		return nil
	}
	return fmt.Errorf("%s 没有提供可用的命令审核器", name)
}

// TestCommandReviewer 用名为 name 的扩展的审核器审一条无害的命令（不读写缓存），返回审核结果：
// 扩展设置页"保存并测试"据此确认这个审核器现在能不能用。审核器不可用或设置没填完时
// 不调用它，返回的结果说明原因。
func (e *Extension) TestCommandReviewer(name string) (*aictx.ReviewInfo, error) {
	rev, err := bootstrap.CommandReviewer(e.ctx, name)
	if err != nil {
		reason := command_review_svc.FailReason(err)
		logger.Ctx(e.ctx).Warn("test command reviewer: unavailable", zap.String("extension", name), zap.String("reason", reason), zap.Error(err))
		return command_review_svc.Result{Outcome: command_review_svc.OutcomeFail, Reason: reason, Extension: name}.Info(""), nil
	}
	r := command_review_svc.Default().TestReviewer(e.ctx, rev)
	logger.Ctx(e.ctx).Info("test command reviewer", zap.String("extension", name),
		zap.String("outcome", string(r.Outcome)), zap.String("reason", r.Reason))
	return r.Info(""), nil
}
