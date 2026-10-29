package extension_svc

import (
	"context"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/pkg/appversion"
)

// ErrReviewerUnsigned 表示非正式版上本地或开发安装的审核器可用，但未经签名。
// 调用方据此在设置里标注，而不是当成不可用。
var ErrReviewerUnsigned = errors.New("command reviewer is installed locally and is not signed")

// reviewerEligible 按第 3 条判断这条安装记录能不能当审核器。
// 摘要必须与磁盘上的 WASM 一致。正式版只接受商店安装；nightly 与开发版接受本地和开发安装，
// 但返回 ErrReviewerUnsigned。来源为空（迁移前的旧记录）一律不合格。
func reviewerEligible(state *extension_state_entity.ExtensionState, wasmSHA256 string, app appversion.Info) error {
	if state == nil || state.PackageSHA256 == "" || state.PackageSHA256 != wasmSHA256 {
		return fmt.Errorf("command reviewer package digest does not match the installed wasm")
	}
	switch state.Source {
	case InstallOriginStore:
		return nil
	case InstallOriginLocal, InstallOriginDev:
		if app.Kind == appversion.KindRelease {
			return fmt.Errorf("command reviewer must be installed from the store on a release build (source %s)", state.Source)
		}
		return ErrReviewerUnsigned
	default:
		return fmt.Errorf("command reviewer has no install source")
	}
}

// recordProvenance 写入或覆盖安装来源与包摘要，同时设置启用状态，返回被覆盖的旧记录
// （之前没有时为 nil），安装失败时据此恢复。
// 写库失败时返回错误：装完却没有来源记录，审核器资格就无从判断。
func (s *Service) recordProvenance(ctx context.Context, name, source, sha256 string, enabled bool) (*extension_state_entity.ExtensionState, error) {
	state, err := s.stateRepo.Find(ctx, name)
	if err != nil {
		if err := s.stateRepo.Create(ctx, &extension_state_entity.ExtensionState{
			Name: name, Enabled: enabled, Source: source, PackageSHA256: sha256,
		}); err != nil {
			return nil, fmt.Errorf("create extension state: %w", err)
		}
		return nil, nil
	}
	prev := *state
	state.Enabled = enabled
	state.Source = source
	state.PackageSHA256 = sha256
	if err := s.stateRepo.Update(ctx, state); err != nil {
		return nil, fmt.Errorf("update extension state: %w", err)
	}
	return &prev, nil
}

// CheckReviewerProvenance 拿包摘要 sum 与安装记录比对，并按当前构建判断能否当审核器。
// 已加载的扩展传它加载时的摘要（Extension.PackageSHA256），还没加载的传磁盘上算出的。
// 非正式版上本地安装、未经签名的审核器可以用；摘要不符或来源不合格时返回错误，调用方按审核失败处理。
func (s *Service) CheckReviewerProvenance(ctx context.Context, name, sum string) error {
	_, err := s.reviewerProvenance(ctx, name, sum)
	return err
}

// reviewerProvenance 同 CheckReviewerProvenance，另外报告审核器是否经过官方签名。
func (s *Service) reviewerProvenance(ctx context.Context, name, sum string) (signed bool, err error) {
	state, err := s.stateRepo.Find(ctx, name)
	if err != nil {
		return false, fmt.Errorf("command reviewer %q has no install record: %w", name, err)
	}
	err = reviewerEligible(state, sum, currentApp())
	if errors.Is(err, ErrReviewerUnsigned) {
		return false, nil
	}
	if err != nil {
		logger.Ctx(ctx).Warn("command reviewer provenance rejected",
			zap.String("extension", name), zap.String("source", state.Source), zap.Error(err))
		return false, err
	}
	return true, nil
}
