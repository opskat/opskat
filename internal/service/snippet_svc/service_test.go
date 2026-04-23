package snippet_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/snippet_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/snippet_repo"
	"github.com/opskat/opskat/internal/repository/snippet_repo/mock_snippet_repo"
)

type svcFixture struct {
	ctx      context.Context
	svc      SnippetSvc
	snippets *mock_snippet_repo.MockSnippetRepo
	assets   *mock_asset_repo.MockAssetRepo
}

func setupSvcTest(t *testing.T) *svcFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })

	snippets := mock_snippet_repo.NewMockSnippetRepo(ctrl)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	snippet_repo.RegisterSnippet(snippets)
	asset_repo.RegisterAsset(assets)

	return &svcFixture{
		ctx:      context.Background(),
		svc:      NewSnippetSvc(NewCategoryRegistry()),
		snippets: snippets,
		assets:   assets,
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestSnippetSvc_Create(t *testing.T) {
	convey.Convey("Create 片段", t, func() {
		convey.Convey("合法无资产片段创建成功", func() {
			f := setupSvcTest(t)
			f.snippets.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, s *snippet_entity.Snippet) error {
					s.ID = 42
					return nil
				},
			)
			got, err := f.svc.Create(f.ctx, CreateReq{
				Name: "ls", Category: snippet_entity.CategoryShell, Content: "ls -al",
			})
			assert.NoError(t, err)
			assert.EqualValues(t, 42, got.ID)
			assert.Equal(t, snippet_entity.SourceUser, got.Source)
			assert.Equal(t, snippet_entity.StatusActive, got.Status)
		})

		convey.Convey("名称为空拒绝", func() {
			f := setupSvcTest(t)
			_, err := f.svc.Create(f.ctx, CreateReq{
				Name: " ", Category: snippet_entity.CategoryShell, Content: "ls",
			})
			assert.Error(t, err)
		})

		convey.Convey("非法分类拒绝", func() {
			f := setupSvcTest(t)
			_, err := f.svc.Create(f.ctx, CreateReq{
				Name: "x", Category: "bogus", Content: "ls",
			})
			assert.Error(t, err)
		})

		convey.Convey("内容为空拒绝", func() {
			f := setupSvcTest(t)
			_, err := f.svc.Create(f.ctx, CreateReq{
				Name: "x", Category: snippet_entity.CategoryShell, Content: " ",
			})
			assert.Error(t, err)
		})

		convey.Convey("prompt 分类绑定资产被拒绝", func() {
			f := setupSvcTest(t)
			_, err := f.svc.Create(f.ctx, CreateReq{
				Name: "x", Category: snippet_entity.CategoryPrompt, Content: "hi",
				AssetID: int64Ptr(1),
			})
			assert.Error(t, err)
		})

		convey.Convey("资产不存在拒绝", func() {
			f := setupSvcTest(t)
			f.assets.EXPECT().Find(gomock.Any(), int64(1)).Return(nil, gorm.ErrRecordNotFound)
			_, err := f.svc.Create(f.ctx, CreateReq{
				Name: "x", Category: snippet_entity.CategoryShell, Content: "ls",
				AssetID: int64Ptr(1),
			})
			assert.Error(t, err)
		})

		convey.Convey("资产类型不匹配拒绝", func() {
			f := setupSvcTest(t)
			f.assets.EXPECT().Find(gomock.Any(), int64(1)).Return(&asset_entity.Asset{
				ID: 1, Type: asset_entity.AssetTypeDatabase, Status: asset_entity.StatusActive,
			}, nil)
			_, err := f.svc.Create(f.ctx, CreateReq{
				Name: "x", Category: snippet_entity.CategoryShell, Content: "ls",
				AssetID: int64Ptr(1),
			})
			assert.Error(t, err)
		})

		convey.Convey("资产已软删除拒绝", func() {
			f := setupSvcTest(t)
			f.assets.EXPECT().Find(gomock.Any(), int64(1)).Return(&asset_entity.Asset{
				ID: 1, Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusDeleted,
			}, nil)
			_, err := f.svc.Create(f.ctx, CreateReq{
				Name: "x", Category: snippet_entity.CategoryShell, Content: "ls",
				AssetID: int64Ptr(1),
			})
			assert.Error(t, err)
		})

		convey.Convey("资产类型匹配成功", func() {
			f := setupSvcTest(t)
			f.assets.EXPECT().Find(gomock.Any(), int64(1)).Return(&asset_entity.Asset{
				ID: 1, Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusActive,
			}, nil)
			f.snippets.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

			_, err := f.svc.Create(f.ctx, CreateReq{
				Name: "x", Category: snippet_entity.CategoryShell, Content: "ls",
				AssetID: int64Ptr(1),
			})
			assert.NoError(t, err)
		})
	})
}

func TestSnippetSvc_Update(t *testing.T) {
	convey.Convey("Update 片段", t, func() {
		convey.Convey("合法更新成功", func() {
			f := setupSvcTest(t)
			f.snippets.EXPECT().GetByID(gomock.Any(), int64(10)).Return(&snippet_entity.Snippet{
				ID: 10, Name: "old", Category: snippet_entity.CategoryShell, Content: "ls",
				Source: snippet_entity.SourceUser, Status: snippet_entity.StatusActive,
			}, nil)
			f.snippets.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

			got, err := f.svc.Update(f.ctx, UpdateReq{
				ID: 10, Name: "new", Content: "pwd",
			})
			assert.NoError(t, err)
			assert.Equal(t, "new", got.Name)
			assert.Equal(t, "pwd", got.Content)
			assert.Equal(t, snippet_entity.CategoryShell, got.Category, "category must be preserved")
		})

		convey.Convey("扩展来源只读拒绝", func() {
			f := setupSvcTest(t)
			f.snippets.EXPECT().GetByID(gomock.Any(), int64(10)).Return(&snippet_entity.Snippet{
				ID: 10, Name: "ext", Category: snippet_entity.CategoryShell, Content: "ls",
				Source: "ext:foo", Status: snippet_entity.StatusActive,
			}, nil)
			_, err := f.svc.Update(f.ctx, UpdateReq{ID: 10, Name: "new", Content: "pwd"})
			assert.Error(t, err)
		})

		convey.Convey("资产类型不匹配拒绝", func() {
			f := setupSvcTest(t)
			f.snippets.EXPECT().GetByID(gomock.Any(), int64(10)).Return(&snippet_entity.Snippet{
				ID: 10, Name: "n", Category: snippet_entity.CategoryShell, Content: "ls",
				Source: snippet_entity.SourceUser, Status: snippet_entity.StatusActive,
			}, nil)
			f.assets.EXPECT().Find(gomock.Any(), int64(2)).Return(&asset_entity.Asset{
				ID: 2, Type: asset_entity.AssetTypeDatabase, Status: asset_entity.StatusActive,
			}, nil)
			_, err := f.svc.Update(f.ctx, UpdateReq{
				ID: 10, Name: "n", Content: "ls", AssetID: int64Ptr(2),
			})
			assert.Error(t, err)
		})
	})
}

func TestSnippetSvc_Delete(t *testing.T) {
	convey.Convey("Delete 片段", t, func() {
		convey.Convey("软删除成功", func() {
			f := setupSvcTest(t)
			f.snippets.EXPECT().GetByID(gomock.Any(), int64(1)).Return(&snippet_entity.Snippet{
				ID: 1, Source: snippet_entity.SourceUser,
			}, nil)
			f.snippets.EXPECT().SoftDelete(gomock.Any(), int64(1)).Return(nil)
			assert.NoError(t, f.svc.Delete(f.ctx, 1))
		})
		convey.Convey("扩展来源只读拒绝", func() {
			f := setupSvcTest(t)
			f.snippets.EXPECT().GetByID(gomock.Any(), int64(1)).Return(&snippet_entity.Snippet{
				ID: 1, Source: "ext:foo",
			}, nil)
			assert.Error(t, f.svc.Delete(f.ctx, 1))
		})
	})
}

func TestSnippetSvc_Duplicate(t *testing.T) {
	convey.Convey("Duplicate 片段", t, func() {
		convey.Convey("克隆带 (copy) 后缀并复位来源", func() {
			f := setupSvcTest(t)
			f.snippets.EXPECT().GetByID(gomock.Any(), int64(1)).Return(&snippet_entity.Snippet{
				ID: 1, Name: "orig", Category: snippet_entity.CategoryShell, Content: "ls",
				Source: "ext:foo", SourceRef: "ref-1", Status: snippet_entity.StatusActive,
			}, nil)
			f.snippets.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, s *snippet_entity.Snippet) error {
					s.ID = 2
					return nil
				},
			)

			got, err := f.svc.Duplicate(f.ctx, 1)
			assert.NoError(t, err)
			assert.Equal(t, "orig (copy)", got.Name)
			assert.Equal(t, snippet_entity.SourceUser, got.Source)
			assert.Equal(t, "", got.SourceRef)
		})
	})
}

func TestSnippetSvc_RecordUse(t *testing.T) {
	f := setupSvcTest(t)
	f.snippets.EXPECT().TouchUsage(gomock.Any(), int64(1)).Return(nil)
	assert.NoError(t, f.svc.RecordUse(f.ctx, 1))
}

func TestSnippetSvc_RecordUse_Errors(t *testing.T) {
	f := setupSvcTest(t)
	f.snippets.EXPECT().TouchUsage(gomock.Any(), int64(1)).Return(errors.New("boom"))
	assert.Error(t, f.svc.RecordUse(f.ctx, 1))
}

func TestSnippetSvc_DetachFromAsset(t *testing.T) {
	f := setupSvcTest(t)
	f.snippets.EXPECT().DetachFromAsset(gomock.Any(), int64(7)).Return(nil)
	assert.NoError(t, f.svc.DetachFromAsset(f.ctx, 7))
}

func TestSnippetSvc_ListCategories(t *testing.T) {
	svc := NewSnippetSvc(NewCategoryRegistry())
	assert.Len(t, svc.ListCategories(), 5)
}
