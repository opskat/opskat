package snippet_repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/snippet_entity"
)

func setupRepo(t *testing.T) (context.Context, SnippetRepo) {
	t.Helper()
	// 每个测试使用独立的匿名内存库
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&snippet_entity.Snippet{}))
	db.SetDefault(gdb)
	return context.Background(), NewSnippet()
}

func int64Ptr(v int64) *int64 { return &v }

func newSnippet(name, category string, assetID *int64) *snippet_entity.Snippet {
	return &snippet_entity.Snippet{
		Name:     name,
		Category: category,
		Content:  "echo " + name,
		Source:   snippet_entity.SourceUser,
		Status:   snippet_entity.StatusActive,
		AssetID:  assetID,
	}
}

func TestSnippetRepo_CreateAndGetByID(t *testing.T) {
	ctx, r := setupRepo(t)
	s := newSnippet("a", snippet_entity.CategoryShell, nil)
	require.NoError(t, r.Create(ctx, s))
	assert.NotZero(t, s.ID)

	got, err := r.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, "a", got.Name)
	assert.Equal(t, snippet_entity.CategoryShell, got.Category)
	assert.Equal(t, snippet_entity.StatusActive, got.Status)
}

func TestSnippetRepo_Update(t *testing.T) {
	ctx, r := setupRepo(t)
	s := newSnippet("a", snippet_entity.CategoryShell, nil)
	require.NoError(t, r.Create(ctx, s))

	s.Name = "a-renamed"
	s.Description = "desc"
	s.Tags = "t1,t2"
	require.NoError(t, r.Update(ctx, s))

	got, err := r.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, "a-renamed", got.Name)
	assert.Equal(t, "desc", got.Description)
	assert.Equal(t, "t1,t2", got.Tags)
}

func TestSnippetRepo_Update_ClearsAssetIDToNull(t *testing.T) {
	ctx, r := setupRepo(t)
	// 创建时带资产绑定
	s := newSnippet("a", snippet_entity.CategoryShell, int64Ptr(42))
	require.NoError(t, r.Create(ctx, s))

	// UI 的 "解除资产绑定" 流程：AssetID 置 nil 后调用 Update
	s.AssetID = nil
	require.NoError(t, r.Update(ctx, s))

	got, err := r.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.Nil(t, got.AssetID, "expected asset_id to be NULL after Update with nil pointer")
}

func TestSnippetRepo_Update_IgnoresProtectedFields(t *testing.T) {
	ctx, r := setupRepo(t)
	// 用户片段，带初始 category / source
	s := newSnippet("a", snippet_entity.CategoryShell, nil)
	require.NoError(t, r.Create(ctx, s))

	// 尝试在传入实体上篡改受保护字段
	s.Name = "renamed"
	s.Category = snippet_entity.CategorySQL // 不应生效
	s.Source = "ext:evil"                   // 不应生效
	s.SourceRef = "hijacked"                // 不应生效
	require.NoError(t, r.Update(ctx, s))

	got, err := r.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.Name)
	assert.Equal(t, snippet_entity.CategoryShell, got.Category, "category must be immutable via Update")
	assert.Equal(t, snippet_entity.SourceUser, got.Source, "source must be immutable via Update")
	assert.Equal(t, "", got.SourceRef, "source_ref must be immutable via Update")
}

func TestSnippetRepo_SoftDelete(t *testing.T) {
	ctx, r := setupRepo(t)
	s := newSnippet("a", snippet_entity.CategoryShell, nil)
	require.NoError(t, r.Create(ctx, s))

	require.NoError(t, r.SoftDelete(ctx, s.ID))

	_, err := r.GetByID(ctx, s.ID)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "expected ErrRecordNotFound, got %v", err)
}

func TestSnippetRepo_Find_CategoriesFilter(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Create(ctx, newSnippet("a", snippet_entity.CategoryShell, nil)))
	require.NoError(t, r.Create(ctx, newSnippet("b", snippet_entity.CategorySQL, nil)))
	require.NoError(t, r.Create(ctx, newSnippet("c", snippet_entity.CategoryPrompt, nil)))

	list, err := r.Find(ctx, SnippetQuery{Categories: []string{snippet_entity.CategoryShell, snippet_entity.CategorySQL}})
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestSnippetRepo_Find_AssetIDAndIncludeGlobal(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Create(ctx, newSnippet("global", snippet_entity.CategoryShell, nil)))
	require.NoError(t, r.Create(ctx, newSnippet("a1", snippet_entity.CategoryShell, int64Ptr(1))))
	require.NoError(t, r.Create(ctx, newSnippet("a2", snippet_entity.CategoryShell, int64Ptr(2))))

	// 仅该资产绑定
	only, err := r.Find(ctx, SnippetQuery{AssetID: int64Ptr(1)})
	require.NoError(t, err)
	assert.Len(t, only, 1)
	assert.Equal(t, "a1", only[0].Name)

	// 并集：该资产绑定 + 全局
	both, err := r.Find(ctx, SnippetQuery{AssetID: int64Ptr(1), IncludeGlobal: true})
	require.NoError(t, err)
	assert.Len(t, both, 2)
	names := []string{both[0].Name, both[1].Name}
	assert.Contains(t, names, "a1")
	assert.Contains(t, names, "global")
}

func TestSnippetRepo_Find_KeywordTagSources(t *testing.T) {
	ctx, r := setupRepo(t)
	s1 := newSnippet("alpha", snippet_entity.CategoryShell, nil)
	s1.Description = "first"
	s1.Tags = "foo,bar"
	require.NoError(t, r.Create(ctx, s1))

	s2 := newSnippet("beta", snippet_entity.CategoryShell, nil)
	s2.Description = "second alpha-ish"
	s2.Tags = "baz"
	require.NoError(t, r.Create(ctx, s2))

	s3 := newSnippet("gamma", snippet_entity.CategoryShell, nil)
	s3.Source = "ext:foo"
	require.NoError(t, r.Create(ctx, s3))

	// Keyword hits name and description
	list, err := r.Find(ctx, SnippetQuery{Keyword: "alpha"})
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// Tag filter
	list, err = r.Find(ctx, SnippetQuery{Tag: "foo"})
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "alpha", list[0].Name)

	// Sources filter: user only
	list, err = r.Find(ctx, SnippetQuery{Sources: []string{snippet_entity.SourceUser}})
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// Sources filter: extension only
	list, err = r.Find(ctx, SnippetQuery{Sources: []string{"ext:foo"}})
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "gamma", list[0].Name)
}

func TestSnippetRepo_Find_Ordering(t *testing.T) {
	ctx, r := setupRepo(t)

	s1 := newSnippet("a", snippet_entity.CategoryShell, nil)
	require.NoError(t, r.Create(ctx, s1))
	s2 := newSnippet("b", snippet_entity.CategoryShell, nil)
	require.NoError(t, r.Create(ctx, s2))
	s3 := newSnippet("c", snippet_entity.CategoryShell, nil)
	require.NoError(t, r.Create(ctx, s3))

	// 使 s2 的 use_count=5, s3 的 use_count=1; s1 的 last_used_at 最新
	for i := 0; i < 5; i++ {
		require.NoError(t, r.TouchUsage(ctx, s2.ID))
	}
	require.NoError(t, r.TouchUsage(ctx, s3.ID))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, r.TouchUsage(ctx, s1.ID))

	// use_count_desc: s2 (5), s3 (1), s1 (1)
	list, err := r.Find(ctx, SnippetQuery{OrderBy: "use_count_desc"})
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, "b", list[0].Name)

	// default: 最近使用优先，s1 应排第一
	list, err = r.Find(ctx, SnippetQuery{})
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, "a", list[0].Name)
}

func TestSnippetRepo_FindBySourceRef(t *testing.T) {
	ctx, r := setupRepo(t)
	s := newSnippet("ext-seed", snippet_entity.CategoryShell, nil)
	s.Source = "ext:foo"
	s.SourceRef = "seed-1"
	require.NoError(t, r.Create(ctx, s))

	got, err := r.FindBySourceRef(ctx, "ext:foo", "seed-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, s.ID, got.ID)

	missing, err := r.FindBySourceRef(ctx, "ext:foo", "missing")
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestSnippetRepo_TouchUsage(t *testing.T) {
	ctx, r := setupRepo(t)
	s := newSnippet("a", snippet_entity.CategoryShell, nil)
	require.NoError(t, r.Create(ctx, s))
	assert.EqualValues(t, 0, s.UseCount)

	require.NoError(t, r.TouchUsage(ctx, s.ID))

	got, err := r.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, got.UseCount)
	require.NotNil(t, got.LastUsedAt)
}

func TestSnippetRepo_DetachFromAsset(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Create(ctx, newSnippet("a1", snippet_entity.CategoryShell, int64Ptr(1))))
	require.NoError(t, r.Create(ctx, newSnippet("a1b", snippet_entity.CategoryShell, int64Ptr(1))))
	require.NoError(t, r.Create(ctx, newSnippet("a2", snippet_entity.CategoryShell, int64Ptr(2))))

	require.NoError(t, r.DetachFromAsset(ctx, 1))

	list, err := r.Find(ctx, SnippetQuery{})
	require.NoError(t, err)
	require.Len(t, list, 3)
	for _, s := range list {
		if s.Name == "a2" {
			require.NotNil(t, s.AssetID)
			assert.EqualValues(t, 2, *s.AssetID)
		} else {
			assert.Nil(t, s.AssetID, "expected asset_id NULL for %s", s.Name)
		}
	}
}

func TestSnippetRepo_HardDeleteBySource(t *testing.T) {
	ctx, r := setupRepo(t)
	s1 := newSnippet("a", snippet_entity.CategoryShell, nil)
	s1.Source = "ext:foo"
	s1.SourceRef = "r1"
	require.NoError(t, r.Create(ctx, s1))

	s2 := newSnippet("b", snippet_entity.CategoryShell, nil)
	s2.Source = "ext:foo"
	s2.SourceRef = "r2"
	require.NoError(t, r.Create(ctx, s2))

	s3 := newSnippet("c", snippet_entity.CategoryShell, nil)
	require.NoError(t, r.Create(ctx, s3))

	require.NoError(t, r.HardDeleteBySource(ctx, "ext:foo"))

	list, err := r.Find(ctx, SnippetQuery{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "c", list[0].Name)
}
