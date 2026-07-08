package oss_svc

import (
	"context"
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
	ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectItem, error)
	StatObject(ctx context.Context, bucket, key string) (ObjectItem, error)
	RemoveObject(ctx context.Context, bucket, key string) error
	PresignGet(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
	PresignPut(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
	BucketExists(ctx context.Context, bucket string) (bool, error)
}

type ListObjectsRequest struct {
	AssetID int64  `json:"assetId"`
	Bucket  string `json:"bucket"`
	Prefix  string `json:"prefix"`
}
type ListObjectsResult struct {
	Prefixes []string     `json:"prefixes"`
	Objects  []ObjectItem `json:"objects"`
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
