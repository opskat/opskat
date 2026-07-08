package oss_svc

import (
	"context"
	"time"

	"github.com/minio/minio-go/v7"
)

// minioAdapter 把 *minio.Client 适配成窄接口 Client。
type minioAdapter struct{ mc *minio.Client }

func newMinioAdapter(mc *minio.Client) Client { return &minioAdapter{mc: mc} }

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

func (a *minioAdapter) ListObjects(ctx context.Context, bucket, prefix string, maxKeys int, startAfter string) ([]ObjectItem, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make([]ObjectItem, 0, maxKeys+1)
	for obj := range a.mc.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix:     prefix,
		Recursive:  false,
		StartAfter: startAfter,
		MaxKeys:    maxKeys + 1,
	}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, toObjectItem(obj))
		if len(out) > maxKeys {
			break // 已读到 maxKeys+1,足以判断"还有下一页";cancel 停止后续
		}
	}
	return out, nil
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
