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
	ListObjects(ctx context.Context, bucket, prefix string, maxKeys int, startAfter string) ([]ObjectItem, error)
	StatObject(ctx context.Context, bucket, key string) (ObjectItem, error)
	RemoveObject(ctx context.Context, bucket, key string) error
	CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error
	RemoveObjects(ctx context.Context, bucket string, keys []string) error
	PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error
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
