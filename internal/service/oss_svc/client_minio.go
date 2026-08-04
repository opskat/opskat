package oss_svc

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

// minioAdapter 把 *minio.Client 适配成窄接口 Client。
type minioAdapter struct {
	mc         *minio.Client
	partSizeMB int
}

func newMinioAdapter(mc *minio.Client, partSizeMB ...int) Client {
	a := &minioAdapter{mc: mc}
	if len(partSizeMB) > 0 {
		a.partSizeMB = partSizeMB[0]
	}
	return a
}

func (a *minioAdapter) ListBuckets(ctx context.Context) ([]BucketItem, error) {
	bs, err := a.mc.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BucketItem, 0, len(bs))
	for _, b := range bs {
		out = append(out, BucketItem{Name: b.Name, CreationDate: b.CreationDate.Unix()})
	}
	return out, nil
}

func (a *minioAdapter) ListObjects(
	ctx context.Context, bucket, prefix string, maxKeys int, continuationToken string,
) (*ListObjectsPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// minio-go 的高层迭代器会跨页拉平结果，丢失真实页边界。Core 返回单张 ListObjectsV2
	// 响应，保留 S3 的 opaque continuation token；这是未填满但 truncated 的页不漏数据
	// 所必需的契约。Core 当前没有 context 形参，因此调用前后都显式检查取消。
	result, err := (minio.Core{Client: a.mc}).ListObjectsV2(bucket, prefix, "", continuationToken, "/", maxKeys)
	if err != nil {
		return nil, err
	}
	if result.IsTruncated && result.NextContinuationToken == "" {
		return nil, fmt.Errorf("list objects response is truncated without a continuation token")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := make([]ObjectItem, 0, len(result.Contents)+len(result.CommonPrefixes))
	for _, obj := range result.Contents {
		items = append(items, toObjectItem(obj))
	}
	for _, p := range result.CommonPrefixes {
		items = append(items, ObjectItem{Key: p.Prefix, IsPrefix: true})
	}
	return &ListObjectsPage{
		Items: items, IsTruncated: result.IsTruncated,
		NextContinuationToken: result.NextContinuationToken,
	}, nil
}

func (a *minioAdapter) StatObject(ctx context.Context, bucket, key string) (ObjectItem, error) {
	info, err := a.mc.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectItem{}, err
	}
	return toObjectItem(info), nil
}

func (a *minioAdapter) RemoveObject(ctx context.Context, bucket, key string) error {
	return a.mc.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

func (a *minioAdapter) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	_, err := a.mc.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: dstBucket, Object: dstKey},
		minio.CopySrcOptions{Bucket: srcBucket, Object: srcKey},
	)
	return err
}

func (a *minioAdapter) RemoveObjects(ctx context.Context, bucket string, keys []string) error {
	objCh := make(chan minio.ObjectInfo, len(keys))
	for _, k := range keys {
		objCh <- minio.ObjectInfo{Key: k}
	}
	close(objCh)
	var errs []minio.RemoveObjectError
	for rerr := range a.mc.RemoveObjects(ctx, bucket, objCh, minio.RemoveObjectsOptions{}) {
		errs = append(errs, rerr)
	}
	return aggregateRemoveErrors(errs)
}

// aggregateRemoveErrors 把 minio 批量删除通道里的逐项错误聚合成一个显式报告;
// 空则 nil。绝不静默丢弃部分失败。
func aggregateRemoveErrors(errs []minio.RemoveObjectError) error {
	if len(errs) == 0 {
		return nil
	}
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, fmt.Sprintf("%s: %v", e.ObjectName, e.Err))
	}
	return fmt.Errorf("批量删除部分失败(%d): %s", len(errs), strings.Join(parts, "; "))
}

func (a *minioAdapter) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error {
	opts := minio.PutObjectOptions{ContentType: contentType}
	if a.partSizeMB > 0 {
		opts.PartSize = uint64(a.partSizeMB) * 1024 * 1024
	}
	_, err := a.mc.PutObject(ctx, bucket, key, r, size, opts)
	return err
}

func (a *minioAdapter) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, int64, error) {
	obj, err := a.mc.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, 0, err
	}
	return obj, info.Size, nil
}

func (a *minioAdapter) PresignGet(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	u, err := a.mc.PresignedGetObject(ctx, bucket, key, expiry, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (a *minioAdapter) PresignPut(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	u, err := a.mc.PresignedPutObject(ctx, bucket, key, expiry)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (a *minioAdapter) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return a.mc.BucketExists(ctx, bucket)
}

// toObjectItem 把 minio.ObjectInfo 映射为 DTO;"文件夹"前缀表现为 Key 以 "/" 结尾且 Size 为 0。
func toObjectItem(o minio.ObjectInfo) ObjectItem {
	item := ObjectItem{
		Key: o.Key, Size: o.Size, ETag: o.ETag,
		StorageClass: o.StorageClass, ContentType: o.ContentType,
	}
	if !o.LastModified.IsZero() {
		item.LastModified = o.LastModified.Unix()
	}
	if o.Size == 0 && len(o.Key) > 0 && o.Key[len(o.Key)-1] == '/' {
		item.IsPrefix = true
	}
	return item
}
