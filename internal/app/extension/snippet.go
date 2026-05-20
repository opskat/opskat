package extension

import (
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/snippet_entity"
	"github.com/opskat/opskat/internal/service/snippet_svc"
)

// ListSnippetCategories 返回所有片段分类（内置 + 扩展提供）
func (e *Extension) ListSnippetCategories() []snippet_svc.Category {
	return snippet_svc.Snippet().ListCategories()
}

// ListSnippets 查询片段列表
func (e *Extension) ListSnippets(req snippet_svc.ListReq) ([]*snippet_entity.Snippet, error) {
	return snippet_svc.Snippet().List(i18n.Ctx(e.ctx, e.lang.Lang()), req)
}

// GetSnippet 获取单个片段
func (e *Extension) GetSnippet(id int64) (*snippet_entity.Snippet, error) {
	return snippet_svc.Snippet().Get(i18n.Ctx(e.ctx, e.lang.Lang()), id)
}

// CreateSnippet 创建片段
func (e *Extension) CreateSnippet(req snippet_svc.CreateReq) (*snippet_entity.Snippet, error) {
	return snippet_svc.Snippet().Create(i18n.Ctx(e.ctx, e.lang.Lang()), req)
}

// UpdateSnippet 更新片段
func (e *Extension) UpdateSnippet(req snippet_svc.UpdateReq) (*snippet_entity.Snippet, error) {
	return snippet_svc.Snippet().Update(i18n.Ctx(e.ctx, e.lang.Lang()), req)
}

// DeleteSnippet 软删除片段
func (e *Extension) DeleteSnippet(id int64) error {
	return snippet_svc.Snippet().Delete(i18n.Ctx(e.ctx, e.lang.Lang()), id)
}

// DuplicateSnippet 复制片段
func (e *Extension) DuplicateSnippet(id int64) (*snippet_entity.Snippet, error) {
	return snippet_svc.Snippet().Duplicate(i18n.Ctx(e.ctx, e.lang.Lang()), id)
}

// RecordSnippetUse 记录片段使用，原子更新 use_count / last_used_at
func (e *Extension) RecordSnippetUse(id int64) error {
	return snippet_svc.Snippet().RecordUse(i18n.Ctx(e.ctx, e.lang.Lang()), id)
}

// SetSnippetLastAssets records the asset IDs most recently used to run a snippet.
func (e *Extension) SetSnippetLastAssets(id int64, assetIDs []int64) error {
	return snippet_svc.Snippet().SetLastAssets(i18n.Ctx(e.ctx, e.lang.Lang()), id, assetIDs)
}

// GetSnippetLastAssets returns the (live-filtered) asset IDs last used to run a snippet.
func (e *Extension) GetSnippetLastAssets(id int64) ([]int64, error) {
	return snippet_svc.Snippet().GetLastAssets(i18n.Ctx(e.ctx, e.lang.Lang()), id)
}
