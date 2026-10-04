// Package custom_type_svc 是自定义类型的领域服务：保存校验（含标识唯一与不可改）、
// 按执行方式预填默认策略、类型结构变更时同步改写各通用资产上的值、删除保护，以及
// 把通用资产解析成可直接交给 authtmpl 渲染的明文字段值。
//
// 本包不得 import internal/assettype（避免循环依赖）：内置与扩展类型名由调用方
// 通过 SetReservedNames 注入。
package custom_type_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// Resolved 是通用资产解析结果。
type Resolved struct {
	Type *custom_type_entity.CustomType
	// Values 覆盖类型的每一个字段（未填写的字段取默认值，否则为空串），密钥已解密，
	// 可直接作为 authtmpl.NewRenderContext 的 fields。不得写入日志或审计。
	Values map[string]string
	// Missing 是值为空的必填字段名，按字段结构顺序。非空时调用方必须报
	// "缺少字段 X"，不发出任何请求、不启动任何进程。
	Missing []string
}

// InUseError 表示类型还有资产在用，拒绝删除（Design decision 14）。
type InUseError struct {
	Slug   string
	Assets []string // 在用资产的名称，按资产 id 升序
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("自定义类型 %q 仍被 %d 个资产使用，不能删除: %s", e.Slug, len(e.Assets), strings.Join(e.Assets, ", "))
}

// IsNotFound 报告 err 是否是 Get / GetBySlug 的"类型不存在"。调用方（含不得 import gorm
// 的 internal/app）借它区分"不存在"与查询失败，而不是各自对 gorm.ErrRecordNotFound 判等。
func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// CustomTypeSvc 自定义类型业务接口。
type CustomTypeSvc interface {
	List(ctx context.Context) ([]*custom_type_entity.CustomType, error)
	// Get / GetBySlug 未找到时返回的错误满足 IsNotFound。
	Get(ctx context.Context, id int64) (*custom_type_entity.CustomType, error)
	GetBySlug(ctx context.Context, slug string) (*custom_type_entity.CustomType, error)
	// Save 创建（ID == 0）或整体更新一个类型，会原地回填 ID、时间戳与默认策略。
	// 校验失败返回 *custom_type_entity.ValidationError（含标识重名 / 被修改）。
	// DefaultPolicy 为 nil 表示调用方没有编辑默认策略：创建时按执行方式预填，
	// 更新时保留原策略。与执行方式不对应的那份配置（HTTP / Command）会被丢弃。
	// 更新时在同一事务里改写该类型的全部资产：删除字段的值随之删除，密钥属性
	// 变化的值改按新属性存储。新增必填字段不阻止保存，缺值由 ResolveAsset 报告。
	Save(ctx context.Context, ct *custom_type_entity.CustomType) error
	// Delete 删除类型；还有资产在用时返回 *InUseError。
	Delete(ctx context.Context, id int64) error
	// UsedBy 返回使用该类型的活动资产名称；没有资产在用时为空。
	UsedBy(ctx context.Context, id int64) ([]string, error)
	// ResolveAsset 解析一台通用资产：找到其类型并解出全部字段的明文值。
	ResolveAsset(ctx context.Context, asset *asset_entity.Asset) (*Resolved, error)
	// SetReservedNames 注入返回内置 + 已加载扩展类型名的函数；每次创建类型时调用
	// （比较不区分大小写，另外总是保留 "generic"）。未注入时创建报错，以免产生与
	// 内置类型重名的标识；更新不受影响（标识不可改）。
	SetReservedNames(fn func() []ReservedName)
}

// ReservedName 是一个不能用作自定义类型标识的类型名。Extension 是声明它的扩展名，为空
// 表示内置类型——重名时据此指出冲突对象（spec「自定义类型」基本信息）。
type ReservedName struct {
	Name      string
	Extension string
}

// BuiltinReservedNames 把一组内置类型名包装成 SetReservedNames 需要的函数。
func BuiltinReservedNames(fn func() []string) func() []ReservedName {
	return func() []ReservedName {
		names := fn()
		out := make([]ReservedName, 0, len(names))
		for _, n := range names {
			out = append(out, ReservedName{Name: n})
		}
		return out
	}
}

type customTypeSvc struct {
	mu       sync.RWMutex
	reserved func() []ReservedName
}

var defaultSvc CustomTypeSvc = New()

// CustomType 获取 CustomTypeSvc 实例
func CustomType() CustomTypeSvc { return defaultSvc }

// New 创建一个独立的服务实例（测试用；业务代码走 CustomType()）。
func New() CustomTypeSvc { return &customTypeSvc{} }

func (s *customTypeSvc) SetReservedNames(fn func() []ReservedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserved = fn
}

func (s *customTypeSvc) reservedNames() ([]ReservedName, error) {
	s.mu.RLock()
	fn := s.reserved
	s.mu.RUnlock()
	if fn == nil {
		return nil, fmt.Errorf("自定义类型服务未注入保留类型名")
	}
	// 通用资产自身的类型名同样不能作为标识。
	return append(fn(), ReservedName{Name: asset_entity.AssetTypeGeneric}), nil
}

func (s *customTypeSvc) List(ctx context.Context) ([]*custom_type_entity.CustomType, error) {
	return custom_type_repo.CustomType().List(ctx)
}

func (s *customTypeSvc) Get(ctx context.Context, id int64) (*custom_type_entity.CustomType, error) {
	ct, err := custom_type_repo.CustomType().Find(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("自定义类型 %d 不存在: %w", id, err)
	}
	return ct, nil
}

func (s *customTypeSvc) GetBySlug(ctx context.Context, slug string) (*custom_type_entity.CustomType, error) {
	ct, err := custom_type_repo.CustomType().FindBySlug(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("自定义类型 %q 不存在: %w", slug, err)
	}
	return ct, nil
}

func (s *customTypeSvc) Save(ctx context.Context, ct *custom_type_entity.CustomType) error {
	var existing *custom_type_entity.CustomType
	if ct.ID != 0 {
		var err error
		if existing, err = s.Get(ctx, ct.ID); err != nil {
			return err
		}
	}

	switch ct.ExecMode {
	case custom_type_entity.ExecModeHTTP:
		ct.Command = nil
	case custom_type_entity.ExecModeCommand:
		ct.HTTP = nil
	}
	if ct.DefaultPolicy == nil {
		if existing != nil {
			ct.DefaultPolicy = existing.DefaultPolicy
		} else {
			ct.DefaultPolicy = custom_type_entity.DefaultPolicyFor(ct.ExecMode)
		}
	}
	if err := s.validate(ctx, ct, existing); err != nil {
		return err
	}

	now := time.Now().Unix()
	ct.Updatetime = now
	if existing == nil {
		ct.Createtime = now
		logger.Ctx(ctx).Info("custom type create start", zap.String("slug", ct.Slug))
		if err := custom_type_repo.CustomType().Create(ctx, ct); err != nil {
			logger.Ctx(ctx).Error("custom type create failed", zap.String("slug", ct.Slug), zap.Error(err))
			return fmt.Errorf("创建自定义类型失败: %w", err)
		}
		logger.Ctx(ctx).Info("custom type created", zap.String("slug", ct.Slug), zap.Int64("customTypeID", ct.ID))
		return nil
	}

	ct.Createtime = existing.Createtime
	logger.Ctx(ctx).Info("custom type update start", zap.String("slug", ct.Slug), zap.Int64("customTypeID", ct.ID))
	var rewritten int
	err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		if err := custom_type_repo.CustomType().Update(txCtx, ct); err != nil {
			return fmt.Errorf("更新自定义类型失败: %w", err)
		}
		n, err := reconcileAssets(txCtx, existing, ct)
		rewritten = n
		return err
	})
	if err != nil {
		logger.Ctx(ctx).Error("custom type update failed", zap.String("slug", ct.Slug), zap.Int64("customTypeID", ct.ID), zap.Error(err))
		return err
	}
	logger.Ctx(ctx).Info("custom type updated", zap.String("slug", ct.Slug), zap.Int64("customTypeID", ct.ID), zap.Int("rewrittenAssets", rewritten))
	return nil
}

// validate 汇总实体校验与依赖其他数据的标识校验，一次返回全部问题。
func (s *customTypeSvc) validate(ctx context.Context, ct, existing *custom_type_entity.CustomType) error {
	var issues []custom_type_entity.Issue
	if err := ct.Validate(); err != nil {
		var verr *custom_type_entity.ValidationError
		if !errors.As(err, &verr) {
			return err
		}
		issues = verr.Issues
	}
	slugIssue := func(code string, kv ...string) {
		issues = append(issues, custom_type_entity.NewIssue("slug", code, kv...))
	}
	switch {
	case existing != nil && existing.Slug != ct.Slug:
		slugIssue(custom_type_entity.IssueSlugImmutable, "original", existing.Slug)
	case existing == nil:
		reserved, err := s.reservedNames()
		if err != nil {
			return err
		}
		for _, r := range reserved {
			if !strings.EqualFold(r.Name, ct.Slug) {
				continue
			}
			if r.Extension != "" {
				slugIssue(custom_type_entity.IssueSlugReservedExtension, "slug", ct.Slug, "extension", r.Extension)
			} else {
				slugIssue(custom_type_entity.IssueSlugReserved, "slug", ct.Slug)
			}
			break
		}
		other, err := custom_type_repo.CustomType().FindBySlug(ctx, ct.Slug)
		switch {
		case err == nil:
			slugIssue(custom_type_entity.IssueSlugTaken, "slug", ct.Slug, "name", other.Name)
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return fmt.Errorf("检查标识是否重名失败: %w", err)
		}
	}
	if len(issues) > 0 {
		return &custom_type_entity.ValidationError{Issues: issues}
	}
	return nil
}

// reconcileAssets 让该类型的全部资产符合新结构，返回被改写的资产数。
func reconcileAssets(ctx context.Context, before, after *custom_type_entity.CustomType) (int, error) {
	assets, err := asset_repo.Asset().ListByCustomType(ctx, before.Slug)
	if err != nil {
		return 0, fmt.Errorf("查询使用该类型的资产失败: %w", err)
	}
	rewritten := 0
	for _, a := range assets {
		cfg, err := a.GetGenericConfig()
		if err != nil {
			return rewritten, fmt.Errorf("资产 %q: %w", a.Name, err)
		}
		changed := false
		for name, v := range cfg.Values {
			newField, ok := after.FieldByName(name)
			if !ok {
				delete(cfg.Values, name)
				changed = true
				continue
			}
			oldField, _ := before.FieldByName(name)
			if oldField.Secret == newField.Secret {
				continue
			}
			converted, err := convertSecretness(ctx, v, newField.Secret)
			if err != nil {
				return rewritten, fmt.Errorf("资产 %q 字段 %s: %w", a.Name, name, err)
			}
			cfg.Values[name] = converted
			changed = true
		}
		if !changed {
			continue
		}
		if err := a.SetGenericConfig(cfg); err != nil {
			return rewritten, err
		}
		a.Updatetime = time.Now().Unix()
		if err := asset_repo.Asset().Update(ctx, a); err != nil {
			return rewritten, fmt.Errorf("更新资产 %q 失败: %w", a.Name, err)
		}
		rewritten++
	}
	return rewritten, nil
}

// convertSecretness 在字段密钥属性变化时按新属性重存值：普通 → 密钥加密明文；
// 密钥 → 普通解出明文（托管凭据引用同样解成明文，保持非密钥值只有明文一种形态）。
func convertSecretness(ctx context.Context, v asset_entity.GenericValue, toSecret bool) (asset_entity.GenericValue, error) {
	if toSecret {
		if v.Value == "" {
			return v, nil
		}
		cipher, err := credential_svc.Default().Encrypt(v.Value)
		if err != nil {
			return v, fmt.Errorf("加密失败: %w", err)
		}
		return asset_entity.GenericValue{Value: cipher}, nil
	}
	plain, err := decryptSecret(ctx, v)
	if err != nil {
		return v, err
	}
	return asset_entity.GenericValue{Value: plain}, nil
}

func decryptSecret(ctx context.Context, v asset_entity.GenericValue) (string, error) {
	if v.CredentialID > 0 {
		return credential_mgr_svc.GetDecryptedPassword(ctx, v.CredentialID)
	}
	if v.Value == "" {
		return "", nil
	}
	plain, err := credential_svc.Default().Decrypt(v.Value)
	if err != nil {
		return "", fmt.Errorf("解密失败: %w", err)
	}
	return plain, nil
}

func (s *customTypeSvc) Delete(ctx context.Context, id int64) error {
	logger.Ctx(ctx).Info("custom type delete start", zap.Int64("customTypeID", id))
	err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		ct, err := s.Get(txCtx, id)
		if err != nil {
			return err
		}
		names, err := s.assetNamesUsing(txCtx, ct.Slug)
		if err != nil {
			return err
		}
		if len(names) > 0 {
			return &InUseError{Slug: ct.Slug, Assets: names}
		}
		return custom_type_repo.CustomType().Delete(txCtx, id)
	})
	if err != nil {
		logger.Ctx(ctx).Error("custom type delete failed", zap.Int64("customTypeID", id), zap.Error(err))
		return err
	}
	logger.Ctx(ctx).Info("custom type deleted", zap.Int64("customTypeID", id))
	return nil
}

func (s *customTypeSvc) UsedBy(ctx context.Context, id int64) ([]string, error) {
	ct, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.assetNamesUsing(ctx, ct.Slug)
}

func (s *customTypeSvc) assetNamesUsing(ctx context.Context, slug string) ([]string, error) {
	assets, err := asset_repo.Asset().ListByCustomType(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("查询使用该类型的资产失败: %w", err)
	}
	names := make([]string, 0, len(assets))
	for _, a := range assets {
		names = append(names, a.Name)
	}
	return names, nil
}

func (s *customTypeSvc) ResolveAsset(ctx context.Context, asset *asset_entity.Asset) (*Resolved, error) {
	cfg, err := asset.GetGenericConfig()
	if err != nil {
		return nil, err
	}
	ct, err := s.GetBySlug(ctx, cfg.CustomType)
	if err != nil {
		return nil, err
	}
	res := &Resolved{Type: ct, Values: make(map[string]string, len(ct.Fields))}
	for _, f := range ct.Fields {
		v, present := cfg.Values[f.Name]
		var value string
		switch {
		case !present:
			value = f.Default
		case f.Secret:
			if value, err = decryptSecret(ctx, v); err != nil {
				return nil, fmt.Errorf("资产 %q 字段 %s: %w", asset.Name, f.Name, err)
			}
		default:
			value = v.Value
		}
		res.Values[f.Name] = value
		if f.Required && value == "" {
			res.Missing = append(res.Missing, f.Name)
		}
	}
	return res, nil
}
