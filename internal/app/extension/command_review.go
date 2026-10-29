package extension

import (
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/extension_svc"
)

// GetCommandReviewState 返回审核状态：已加载的审核扩展、设置里选的，以及实际在用的
// （没选时启用中的只有一个就用它），没有可用的时说明原因。
func (e *Extension) GetCommandReviewState() (*extension_svc.CommandReviewState, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	st := e.service.CommandReviewState(e.ctx, e.lang.Lang(), bootstrap.GetConfig().CommandReviewExtension)
	return &st, nil
}

// SetCommandReviewer 选用审核扩展；name 为空表示不指定（启用中的只有一个时用它）。
// 只能选现在能用的审核扩展：选一个用不了的，之后每次审核都会失败。
func (e *Extension) SetCommandReviewer(name string) error {
	if name != "" {
		if e.service == nil {
			return fmt.Errorf("extension system not initialized")
		}
		reviewers := e.service.ListCommandReviewers(e.ctx, e.lang.Lang())
		if err := checkSelectableReviewer(reviewers, name); err != nil {
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

func checkSelectableReviewer(reviewers []extension_svc.CommandReviewerInfo, name string) error {
	for _, r := range reviewers {
		if r.Name != name {
			continue
		}
		if !r.Usable {
			return fmt.Errorf("审核扩展 %s 现在不能使用：%s", name, r.Reason)
		}
		return nil
	}
	return fmt.Errorf("%s 不是已加载的审核扩展", name)
}

// TestCommandReviewer 用名为 name 的审核扩展审一条无害的命令（不读写缓存），返回审核结果：
// 扩展设置页据此确认这个审核扩展现在能不能用。扩展不可用时返回的结果说明原因。
func (e *Extension) TestCommandReviewer(name string) (*aictx.ReviewInfo, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	rev, err := e.service.ActiveCommandReviewer(e.ctx, name)
	if err != nil {
		logger.Ctx(e.ctx).Warn("test command reviewer: unavailable", zap.String("extension", name), zap.Error(err))
		return command_review_svc.Result{Outcome: command_review_svc.OutcomeFail, Reason: command_review_svc.ReasonReviewerUnavailable, Extension: name}.Info(""), nil
	}
	r := command_review_svc.Default().TestReviewer(e.ctx, rev)
	logger.Ctx(e.ctx).Info("test command reviewer", zap.String("extension", name),
		zap.String("outcome", string(r.Outcome)), zap.String("reason", r.Reason))
	return r.Info(""), nil
}

// GetExtensionSettings 返回扩展设置表单：schema、非密钥字段的值和已设置的密钥字段。
func (e *Extension) GetExtensionSettings(name string) (*extension_svc.ExtensionSettingsView, error) {
	if e.service == nil {
		return nil, fmt.Errorf("extension system not initialized")
	}
	return e.service.SettingsView(e.ctx, name, e.lang.Lang())
}

// SaveExtensionSettings 保存扩展设置，密钥字段加密后入库；密钥字段留空表示不修改。
func (e *Extension) SaveExtensionSettings(name string, values map[string]string) error {
	if e.service == nil {
		return fmt.Errorf("extension system not initialized")
	}
	if err := e.service.SaveExtensionSettings(e.ctx, name, values, credential_svc.Default().Encrypt); err != nil {
		logger.Ctx(e.ctx).Warn("save extension settings failed", zap.String("extension", name), zap.Error(err))
		return err
	}
	logger.Ctx(e.ctx).Info("extension settings saved", zap.String("extension", name))
	return nil
}
