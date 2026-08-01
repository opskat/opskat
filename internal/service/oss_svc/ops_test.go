package oss_svc_test

import (
	"context"
	"testing"

	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestListObjectsWithSplitsPrefixesAndObjects(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "images/", 200, "").Return([]oss_svc.ObjectItem{
		{Key: "images/thumbnails/", IsPrefix: true},
		{Key: "images/hero.jpg", Size: 2516480},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "images/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"images/thumbnails/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "images/hero.jpg", res.Objects[0].Key)
	assert.False(t, res.IsTruncated)
	assert.Empty(t, res.NextContinuationToken)
}

func TestListObjectsWithOmitsCurrentPrefixFolderMarker(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "docs/", 200, "").Return([]oss_svc.ObjectItem{
		{Key: "docs/", IsPrefix: true},
		{Key: "docs/archive/", IsPrefix: true},
		{Key: "docs/readme.pdf", Size: 1024},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "docs/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/archive/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "docs/readme.pdf", res.Objects[0].Key)
}

func TestListObjectsWithEmptyFolderMarkerReturnsEmptyListing(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "empty/", 200, "").Return([]oss_svc.ObjectItem{
		{Key: "empty/", IsPrefix: true},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "empty/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, res.Prefixes)
	assert.Equal(t, []oss_svc.ObjectItem{}, res.Objects)
}

func TestListObjectsWithOnlyOmitsExactCurrentPrefix(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "parent/", 200, "").Return([]oss_svc.ObjectItem{
		{Key: "parent/", IsPrefix: true},
		{Key: "parent-empty/", IsPrefix: true},
		{Key: "parent/empty/", IsPrefix: true},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "parent/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"parent-empty/", "parent/empty/"}, res.Prefixes)
}

// 空 bucket/前缀应序列化为 JSON "[]" 而非 "null"。
func TestListObjectsWithEmptyBucketReturnsEmptySlicesNotNil(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "empty-bucket", "", 200, "").Return([]oss_svc.ObjectItem{}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "empty-bucket", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, res.Prefixes)
	assert.Equal(t, []oss_svc.ObjectItem{}, res.Objects)
	assert.NotNil(t, res.Prefixes)
	assert.NotNil(t, res.Objects)
}

// maxKeys+1 项 → 截断:丢掉最后一项,next = 第 maxKeys 项的 Key。
func TestListObjectsWithTruncatesAndSetsNextCursor(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	// maxKeys=2,adapter 会多读 1 项;这里 mock 直接返回 3 项模拟"还有下一页"。
	c.EXPECT().ListObjects(gomock.Any(), "b", "docs/", 2, "").Return([]oss_svc.ObjectItem{
		{Key: "docs/a.md", Size: 10},
		{Key: "docs/b.md", Size: 20},
		{Key: "docs/c.md", Size: 30},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "docs/", 2, "")
	require.NoError(t, err)
	assert.True(t, res.IsTruncated)
	assert.Equal(t, "docs/b.md", res.NextContinuationToken)
	require.Len(t, res.Objects, 2)
	assert.Equal(t, "docs/a.md", res.Objects[0].Key)
	assert.Equal(t, "docs/b.md", res.Objects[1].Key)
}

// 截断游标要能被原样喂回 startAfter,因此它必须同时满足两条:
// 本页交出去的每一条都 <= 游标(否则下一页会把它再交一遍),被丢掉的每一条都 > 游标
// (否则它永远回不来)。两条一起才是 start-after 的语义。
//
// 这不是形式检查:Client 交来的那一串**不是按 key 排好序的**。minio-go 的 listObjectsV2
// 对每个 S3 响应先逐条交出 result.Contents、再逐条交出 result.CommonPrefixes
// (api-list.go:164-177,v1 的 listObjects:351-364 同形),而 S3 是把两者合在一起按 key 序
// 截到 MaxKeys 的。于是"最后一条"既不是最大的 key,也不是分界线。
func assertCursorIsAStartAfterWatermark(t *testing.T, res *oss_svc.ListObjectsResult, all []oss_svc.ObjectItem) {
	t.Helper()
	require.True(t, res.IsTruncated, "fixture 应当触发截断")
	returned := make(map[string]bool, len(all))
	for _, p := range res.Prefixes {
		returned[p] = true
	}
	for _, o := range res.Objects {
		returned[o.Key] = true
	}
	for _, it := range all {
		if returned[it.Key] {
			assert.LessOrEqual(t, it.Key, res.NextContinuationToken,
				"%q 已经交出去了,却排在游标 %q 之后:下一页 startAfter 会把它再交一遍",
				it.Key, res.NextContinuationToken)
			continue
		}
		assert.Greater(t, it.Key, res.NextContinuationToken,
			"%q 被这一页丢掉了,却排在游标 %q 之前:startAfter 是排他的,它再也回不来",
			it.Key, res.NextContinuationToken)
	}
}

// 页边界落在 Contents 里、而公共前缀排在这些 key **之前**:一个桶根下有 archive/ 与
// b001…b200,S3 首页按 key 序是 [archive/, b001…b200],minio 交出来的却是
// [b001…b200, archive/]。按"最后一条"取游标就得到 b200,archive/ 连同它下面整棵子树
// 被静默丢掉——cp -r 会报 transferred: 200 且一个字都不提。
func TestListObjectsWithCursorDoesNotSkipAPrefixSortingBeforeIt(t *testing.T) {
	items := []oss_svc.ObjectItem{
		{Key: "b1", Size: 1}, {Key: "b2", Size: 1}, {Key: "b3", Size: 1},
		{Key: "archive/", IsPrefix: true},
	}
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "", 3, "").Return(items, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "", 3, "")
	require.NoError(t, err)
	assertCursorIsAStartAfterWatermark(t, res, items)
}

// 镜像情形:页边界落在 CommonPrefixes 里,而某个对象的 key 排在这些前缀**之后**。
// 那个对象本页已经交出去了,游标却停在它前面,于是下一页 startAfter 把它再交一遍——
// 重复条目、重复读写、以及虚报的 200 条上限。
func TestListObjectsWithCursorDoesNotPrecedeAReturnedObject(t *testing.T) {
	items := []oss_svc.ObjectItem{
		{Key: "z.txt", Size: 1},
		{Key: "p1/", IsPrefix: true}, {Key: "p2/", IsPrefix: true}, {Key: "p3/", IsPrefix: true},
	}
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "", 3, "").Return(items, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "", 3, "")
	require.NoError(t, err)
	assertCursorIsAStartAfterWatermark(t, res, items)
}

// 续读:startAfter 透传给 Client;不足一页则不截断。
func TestListObjectsWithResumeCursorNotTruncated(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "docs/", 2, "docs/b.md").Return([]oss_svc.ObjectItem{
		{Key: "docs/c.md", Size: 30},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "docs/", 2, "docs/b.md")
	require.NoError(t, err)
	assert.False(t, res.IsTruncated)
	assert.Empty(t, res.NextContinuationToken)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "docs/c.md", res.Objects[0].Key)
}
