package snippet_repo

//go:generate mockgen -source=snippet.go -destination=mock_snippet_repo/snippet.go -package=mock_snippet_repo

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/snippet_entity"
)

// SnippetQuery 查询参数
type SnippetQuery struct {
	Categories []string // 空表示不过滤
	// 资产过滤：
	//   AssetID == nil：不按资产过滤
	//   AssetID != nil 且 IncludeGlobal == false：仅返回该资产绑定的片段
	//   AssetID != nil 且 IncludeGlobal == true：返回该资产绑定 + 全局（asset_id IS NULL）
	AssetID       *int64
	IncludeGlobal bool
	Keyword       string   // 对 name / description / content 做 LIKE
	Tag           string   // tags 列子串匹配
	Sources       []string // 空表示不过滤；可选 "user" / "ext:foo"
	Limit         int      // 0 表示不限制
	Offset        int
	OrderBy       string // "use_count_desc" | "updated_at_desc" | ""（默认：last_used_at DESC NULLS LAST, updated_at DESC）
}

// SnippetRepo 数据访问接口
type SnippetRepo interface {
	Create(ctx context.Context, s *snippet_entity.Snippet) error
	Update(ctx context.Context, s *snippet_entity.Snippet) error
	SoftDelete(ctx context.Context, id int64) error
	HardDeleteBySource(ctx context.Context, source string) error
	GetByID(ctx context.Context, id int64) (*snippet_entity.Snippet, error)
	Find(ctx context.Context, q SnippetQuery) ([]*snippet_entity.Snippet, error)
	FindBySourceRef(ctx context.Context, source, ref string) (*snippet_entity.Snippet, error)
	TouchUsage(ctx context.Context, id int64) error
	DetachFromAsset(ctx context.Context, assetID int64) error
}

var defaultSnippet SnippetRepo

// Snippet 获取 SnippetRepo 实例
func Snippet() SnippetRepo { return defaultSnippet }

// RegisterSnippet 注册 SnippetRepo 实现
func RegisterSnippet(r SnippetRepo) { defaultSnippet = r }
