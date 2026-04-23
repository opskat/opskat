package snippet_svc

import (
	"github.com/opskat/opskat/internal/model/entity/snippet_entity"
)

// Category 片段分类元信息
type Category struct {
	ID        string `json:"id"`        // shell / sql / redis / mongo / prompt
	AssetType string `json:"assetType"` // ssh / database / redis / mongodb / ""
	Label     string `json:"label"`     // 英文 fallback；前端自行做 i18n
	Source    string `json:"source"`    // "builtin" 或 "ext:<name>"（扩展来源从 PR 5 起启用）
}

// CategorySourceBuiltin 内置分类来源
const CategorySourceBuiltin = "builtin"

// CategoryRegistry 分类注册表。PR 1 仅包含 5 个内置分类；
// PR 5 将扩展 RefreshFromExtensions 支持。
type CategoryRegistry struct {
	categories []Category
	index      map[string]Category
}

// NewCategoryRegistry 预加载内置分类
func NewCategoryRegistry() *CategoryRegistry {
	builtins := []Category{
		{ID: snippet_entity.CategoryShell, AssetType: "ssh", Label: "Shell", Source: CategorySourceBuiltin},
		{ID: snippet_entity.CategorySQL, AssetType: "database", Label: "SQL", Source: CategorySourceBuiltin},
		{ID: snippet_entity.CategoryRedis, AssetType: "redis", Label: "Redis", Source: CategorySourceBuiltin},
		{ID: snippet_entity.CategoryMongo, AssetType: "mongodb", Label: "Mongo", Source: CategorySourceBuiltin},
		{ID: snippet_entity.CategoryPrompt, AssetType: "", Label: "Prompt", Source: CategorySourceBuiltin},
	}
	index := make(map[string]Category, len(builtins))
	for _, c := range builtins {
		index[c.ID] = c
	}
	return &CategoryRegistry{categories: builtins, index: index}
}

// List 返回所有已注册分类（顺序稳定）
func (r *CategoryRegistry) List() []Category {
	out := make([]Category, len(r.categories))
	copy(out, r.categories)
	return out
}

// Get 查找指定 ID 的分类
func (r *CategoryRegistry) Get(id string) (Category, bool) {
	c, ok := r.index[id]
	return c, ok
}
