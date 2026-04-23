package snippet_svc

import (
	"context"
	"errors"
	"fmt"

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

// validateAssetBinding 校验 AssetID 与分类的绑定关系
func (s *snippetSvc) validateAssetBinding(ctx context.Context, category string, assetID *int64) error {
	if assetID == nil {
		return nil
	}
	if category == snippet_entity.CategoryPrompt {
		return errors.New("prompt snippets cannot bind to an asset")
	}
	wantType := snippet_entity.CategoryAssetType(category)
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
