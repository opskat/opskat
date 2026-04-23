package snippet_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/snippet_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/snippet_repo"
)

// CreateReq 创建请求
type CreateReq struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Content     string `json:"content"`
	Description string `json:"description"`
	Tags        string `json:"tags"` // 逗号分隔
	AssetID     *int64 `json:"assetId,omitempty"`
}

// UpdateReq 更新请求。分类不可变更（业务语义上变更分类 = 一个新的片段）。
type UpdateReq struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	Description string `json:"description"`
	Tags        string `json:"tags"`
	AssetID     *int64 `json:"assetId,omitempty"`
}

// ListReq 列表请求
type ListReq struct {
	Categories    []string `json:"categories"`
	AssetID       *int64   `json:"assetId,omitempty"`
	IncludeGlobal bool     `json:"includeGlobal"`
	Keyword       string   `json:"keyword"`
	Tag           string   `json:"tag"`
	Limit         int      `json:"limit"`
	Offset        int      `json:"offset"`
	OrderBy       string   `json:"orderBy"`
}

// SeedDef 扩展 seed 片段的描述结构。
// 故意不引用 pkg/extension 类型，避免 snippet_svc ↔ pkg/extension 循环依赖；
// 由调用方（extension_svc 层的 hook 适配）按字段翻译 manifest.SnippetsDef.Seed。
type SeedDef struct {
	Key         string
	Name        string
	Category    string
	Content     string
	Description string
	Tags        []string
}

// SnippetSvc 片段业务接口
type SnippetSvc interface {
	Create(ctx context.Context, req CreateReq) (*snippet_entity.Snippet, error)
	Update(ctx context.Context, req UpdateReq) (*snippet_entity.Snippet, error)
	Delete(ctx context.Context, id int64) error
	Duplicate(ctx context.Context, id int64) (*snippet_entity.Snippet, error)
	Get(ctx context.Context, id int64) (*snippet_entity.Snippet, error)
	List(ctx context.Context, req ListReq) ([]*snippet_entity.Snippet, error)
	ListCategories() []Category
	RecordUse(ctx context.Context, id int64) error
	DetachFromAsset(ctx context.Context, assetID int64) error

	// Extension lifecycle hooks（由 extension_svc 在 Install/Uninstall 调用）
	SyncExtensionSeeds(ctx context.Context, extName string, seeds []SeedDef) error
	RemoveExtensionSeeds(ctx context.Context, extName string) error
	RefreshCategories()
	KnownCategoryIDs() []string

	// Registry 暴露注册表，用于 bootstrap 注入 ExtensionCategoryProvider
	Registry() *CategoryRegistry
}

// snippetSvc 默认实现
type snippetSvc struct {
	registry *CategoryRegistry
}

// NewSnippetSvc 创建片段服务
func NewSnippetSvc(registry *CategoryRegistry) SnippetSvc {
	if registry == nil {
		registry = NewCategoryRegistry()
	}
	return &snippetSvc{registry: registry}
}

var defaultSnippet SnippetSvc

// Register 注册服务单例
func Register(s SnippetSvc) { defaultSnippet = s }

// Snippet 获取服务单例
func Snippet() SnippetSvc { return defaultSnippet }

// validateAssetBinding 校验 AssetID 与分类的绑定关系。
// 对于扩展声明的分类，取其 AssetType（CategoryRegistry 中）而非内置 CategoryAssetType 映射。
func (s *snippetSvc) validateAssetBinding(ctx context.Context, category string, assetID *int64) error {
	if assetID == nil {
		return nil
	}
	if category == snippet_entity.CategoryPrompt {
		return errors.New("prompt snippets cannot bind to an asset")
	}
	wantType := ""
	if c, ok := s.registry.Get(category); ok {
		wantType = c.AssetType
	} else {
		// 兼容内置映射（理论上注册表包含全部内置；这里是防御性兜底）。
		wantType = snippet_entity.CategoryAssetType(category)
	}
	if wantType == "" {
		return fmt.Errorf("category %q does not support asset binding", category)
	}
	asset, err := asset_repo.Asset().Find(ctx, *assetID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("asset %d not found", *assetID)
		}
		return fmt.Errorf("load asset %d: %w", *assetID, err)
	}
	if asset.Status != asset_entity.StatusActive {
		return fmt.Errorf("asset %d is not active", *assetID)
	}
	if asset.Type != wantType {
		return fmt.Errorf("asset type %q does not match category %q (expect %q)", asset.Type, category, wantType)
	}
	return nil
}

// validateRegisteredCategory 基于注册表判定分类是否可被用户创建（内置 + 已加载扩展的分类）。
// 扩展来源的 seed 同步通过独立路径（SyncExtensionSeeds），不走该校验。
func (s *snippetSvc) validateRegisteredCategory(category string) error {
	if _, ok := s.registry.Get(category); !ok {
		return fmt.Errorf("snippet category %q is not registered (installed extension may be missing)", category)
	}
	return nil
}

func (s *snippetSvc) Create(ctx context.Context, req CreateReq) (*snippet_entity.Snippet, error) {
	entity := &snippet_entity.Snippet{
		Name:        req.Name,
		Category:    req.Category,
		Content:     req.Content,
		Description: req.Description,
		Tags:        req.Tags,
		AssetID:     req.AssetID,
		Source:      snippet_entity.SourceUser,
		SourceRef:   "",
		Status:      snippet_entity.StatusActive,
	}
	if err := entity.Validate(); err != nil {
		return nil, err
	}
	if err := s.validateRegisteredCategory(entity.Category); err != nil {
		return nil, err
	}
	if err := s.validateAssetBinding(ctx, entity.Category, entity.AssetID); err != nil {
		return nil, err
	}
	if err := snippet_repo.Snippet().Create(ctx, entity); err != nil {
		return nil, err
	}
	return entity, nil
}

func (s *snippetSvc) Update(ctx context.Context, req UpdateReq) (*snippet_entity.Snippet, error) {
	if req.ID == 0 {
		return nil, errors.New("snippet id is required")
	}
	existing, err := snippet_repo.Snippet().GetByID(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing.IsReadOnly() {
		return nil, errors.New("snippet is read-only (extension-provided)")
	}

	// Category 不可变更
	existing.Name = req.Name
	existing.Content = req.Content
	existing.Description = req.Description
	existing.Tags = req.Tags
	existing.AssetID = req.AssetID

	if err := existing.Validate(); err != nil {
		return nil, err
	}
	if err := s.validateAssetBinding(ctx, existing.Category, existing.AssetID); err != nil {
		return nil, err
	}
	if err := snippet_repo.Snippet().Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

func (s *snippetSvc) Delete(ctx context.Context, id int64) error {
	if id == 0 {
		return errors.New("snippet id is required")
	}
	existing, err := snippet_repo.Snippet().GetByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.IsReadOnly() {
		return errors.New("snippet is read-only (extension-provided)")
	}
	return snippet_repo.Snippet().SoftDelete(ctx, id)
}

func (s *snippetSvc) Duplicate(ctx context.Context, id int64) (*snippet_entity.Snippet, error) {
	if id == 0 {
		return nil, errors.New("snippet id is required")
	}
	existing, err := snippet_repo.Snippet().GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	clone := &snippet_entity.Snippet{
		Name:        existing.Name + " (copy)",
		Category:    existing.Category,
		Content:     existing.Content,
		Description: existing.Description,
		Tags:        existing.Tags,
		AssetID:     existing.AssetID,
		Source:      snippet_entity.SourceUser,
		SourceRef:   "",
		Status:      snippet_entity.StatusActive,
	}
	if err := clone.Validate(); err != nil {
		return nil, err
	}
	if err := snippet_repo.Snippet().Create(ctx, clone); err != nil {
		return nil, err
	}
	return clone, nil
}

func (s *snippetSvc) Get(ctx context.Context, id int64) (*snippet_entity.Snippet, error) {
	if id == 0 {
		return nil, errors.New("snippet id is required")
	}
	return snippet_repo.Snippet().GetByID(ctx, id)
}

func (s *snippetSvc) List(ctx context.Context, req ListReq) ([]*snippet_entity.Snippet, error) {
	return snippet_repo.Snippet().Find(ctx, snippet_repo.SnippetQuery{
		Categories:    req.Categories,
		AssetID:       req.AssetID,
		IncludeGlobal: req.IncludeGlobal,
		Keyword:       req.Keyword,
		Tag:           req.Tag,
		Limit:         req.Limit,
		Offset:        req.Offset,
		OrderBy:       req.OrderBy,
	})
}

func (s *snippetSvc) ListCategories() []Category {
	return s.registry.List()
}

func (s *snippetSvc) RecordUse(ctx context.Context, id int64) error {
	if id == 0 {
		return errors.New("snippet id is required")
	}
	return snippet_repo.Snippet().TouchUsage(ctx, id)
}

func (s *snippetSvc) DetachFromAsset(ctx context.Context, assetID int64) error {
	if assetID == 0 {
		return nil
	}
	return snippet_repo.Snippet().DetachFromAsset(ctx, assetID)
}

// Registry 暴露底层 CategoryRegistry，bootstrap 用它挂载 ExtensionCategoryProvider。
func (s *snippetSvc) Registry() *CategoryRegistry { return s.registry }

// RefreshCategories 重建分类表（内置 + provider 当前扩展分类）。
// extension_svc 在 Install/Uninstall 完成后调用。
func (s *snippetSvc) RefreshCategories() {
	s.registry.RefreshFromExtensions()
}

// KnownCategoryIDs 返回当前注册表已知分类 ID（内置 + 已加载扩展声明）。
// 用于扩展安装前的跨扩展分类 ID 冲突检测。
func (s *snippetSvc) KnownCategoryIDs() []string {
	return s.registry.IDs()
}

// SyncExtensionSeeds 同步单个扩展的 seed 片段到数据库。幂等：
//   - 若 (source=ext:<ext>, source_ref=seed.key) 存在：覆盖 name/category/content/description/tags，
//     保留 use_count/last_used_at/status/created_at。
//   - 若不存在：以 source="ext:<ext>" / source_ref=seed.key 新建。
//   - 扫尾：删除当前 source 下不在 seed.key 集合中的记录（扩展升级时 seed 被移除）。
func (s *snippetSvc) SyncExtensionSeeds(ctx context.Context, extName string, seeds []SeedDef) error {
	if extName == "" {
		return errors.New("extension name is required")
	}
	source := snippet_entity.SourceExtPrefix + extName

	keepRefs := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		if err := s.validateSeed(seed); err != nil {
			return fmt.Errorf("sync seed %q: %w", seed.Key, err)
		}
		ent := &snippet_entity.Snippet{
			Name:        strings.TrimSpace(seed.Name),
			Category:    seed.Category,
			Content:     seed.Content,
			Description: seed.Description,
			Tags:        strings.Join(normalizeTags(seed.Tags), ","),
			Source:      source,
			SourceRef:   seed.Key,
			Status:      snippet_entity.StatusActive,
		}
		if err := ent.Validate(); err != nil {
			return fmt.Errorf("sync seed %q: %w", seed.Key, err)
		}
		if err := snippet_repo.Snippet().UpsertExtensionSeed(ctx, ent); err != nil {
			return fmt.Errorf("upsert seed %q: %w", seed.Key, err)
		}
		keepRefs = append(keepRefs, seed.Key)
	}

	if err := snippet_repo.Snippet().DeleteExtensionSeedsMissing(ctx, source, keepRefs); err != nil {
		return fmt.Errorf("prune stale seeds for %q: %w", extName, err)
	}
	return nil
}

// RemoveExtensionSeeds 删除单个扩展的所有 seed 片段（硬删除）。
// 用户自建片段（source=user）不受影响，即使 category 指向扩展分类也会保留（前端按孤儿处理）。
func (s *snippetSvc) RemoveExtensionSeeds(ctx context.Context, extName string) error {
	if extName == "" {
		return errors.New("extension name is required")
	}
	source := snippet_entity.SourceExtPrefix + extName
	return snippet_repo.Snippet().HardDeleteBySource(ctx, source)
}

func (s *snippetSvc) validateSeed(seed SeedDef) error {
	if seed.Key == "" {
		return errors.New("seed key is required")
	}
	if strings.TrimSpace(seed.Name) == "" {
		return errors.New("seed name is required")
	}
	if strings.TrimSpace(seed.Content) == "" {
		return errors.New("seed content is required")
	}
	if seed.Category == "" {
		return errors.New("seed category is required")
	}
	return nil
}

// normalizeTags 裁剪空白、丢弃空串、小写化，保持去重稳定顺序。
func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		v := strings.ToLower(strings.TrimSpace(t))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
