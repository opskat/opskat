package oss_svc

import (
	"context"
	"io"
	"time"
)

// BucketItem 是账号下的一个 Bucket。
type BucketItem struct {
	Name         string `json:"name"`
	CreationDate int64  `json:"creationDate"`
}

// ObjectItem 是 Bucket 下的一个对象或"文件夹"前缀。
type ObjectItem struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	LastModified int64  `json:"lastModified"`
	ETag         string `json:"etag"`
	StorageClass string `json:"storageClass"`
	ContentType  string `json:"contentType"`
	IsPrefix     bool   `json:"isPrefix"`
}

// Client 是服务依赖的窄对象存储接口(可 mock)。
type Client interface {
	ListBuckets(ctx context.Context) ([]BucketItem, error)
	// ListObjects 分页契约:maxKeys<=0 表示"使用服务端默认值";当超出 maxKeys 还有更多对象时,
	// 实现必须多返回 1 条(即最多返回 maxKeys+1 条),调用方(ListObjectsWith)靠这多出的一条
	// 判断 IsTruncated 并截断结果——若实现只按字面返回恰好 maxKeys 条,截断检测会永远为 false,
	// 分页将在"假的最后一页"上卡死。
	//
	// 返回的**顺序**无所谓(ListObjectsWith 自己排序),但这一串必须是按 key 序**最靠前**的
	// 那些:调用方要从中推出一个 start-after 续传游标,漏掉一个小 key 而带回一个大 key
	// 会让游标直接越过前者,它再也回不来。minio 适配器在单个 S3 响应之内满足这条
	// (S3 按 key 序截到 MaxKeys);只有 S3 少给却仍报 truncated、迫使它跨到下一页时不成立,
	// 而 minio 的 channel API 看不到页边界。
	ListObjects(ctx context.Context, bucket, prefix string, maxKeys int, startAfter string) ([]ObjectItem, error)
	StatObject(ctx context.Context, bucket, key string) (ObjectItem, error)
	RemoveObject(ctx context.Context, bucket, key string) error
	CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error
	RemoveObjects(ctx context.Context, bucket string, keys []string) error
	PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, int64, error)
	PresignGet(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
	PresignPut(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
	BucketExists(ctx context.Context, bucket string) (bool, error)
}

type ListObjectsRequest struct {
	AssetID           int64  `json:"assetId"`
	Bucket            string `json:"bucket"`
	Prefix            string `json:"prefix"`
	MaxKeys           int    `json:"maxKeys"`
	ContinuationToken string `json:"continuationToken"`
}
type ListObjectsResult struct {
	Prefixes              []string     `json:"prefixes"`
	Objects               []ObjectItem `json:"objects"`
	NextContinuationToken string       `json:"nextContinuationToken"`
	IsTruncated           bool         `json:"isTruncated"`
}
type ObjectRequest struct {
	AssetID int64  `json:"assetId"`
	Bucket  string `json:"bucket"`
	Key     string `json:"key"`
}
type PresignRequest struct {
	AssetID    int64  `json:"assetId"`
	Bucket     string `json:"bucket"`
	Key        string `json:"key"`
	ExpirySecs int    `json:"expirySecs"`
}
type CopyRequest struct { // OSSCopyObject / OSSMoveObject 共用
	AssetID   int64  `json:"assetId"`
	SrcBucket string `json:"srcBucket"`
	SrcKey    string `json:"srcKey"`
	DstBucket string `json:"dstBucket"`
	DstKey    string `json:"dstKey"`
}
type RemoveObjectsRequest struct {
	AssetID int64    `json:"assetId"`
	Bucket  string   `json:"bucket"`
	Keys    []string `json:"keys"`
}
type CreateFolderRequest struct {
	AssetID int64  `json:"assetId"`
	Bucket  string `json:"bucket"`
	Prefix  string `json:"prefix"` // 服务端规范化为以 "/" 结尾
}
