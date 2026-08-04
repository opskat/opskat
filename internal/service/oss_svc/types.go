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

// ListObjectsPage 是对象存储服务端返回的一张真实页。ContinuationToken 是 opaque 游标，
// 调用方只能原样传回；不能从对象 key 猜出下一页边界。
type ListObjectsPage struct {
	Items                 []ObjectItem
	IsTruncated           bool
	NextContinuationToken string
}

// Client 是服务依赖的窄对象存储接口(可 mock)。
type Client interface {
	ListBuckets(ctx context.Context) ([]BucketItem, error)
	// ListObjects 返回一张真实 S3 页。continuationToken 来自上一页并原样交还服务端；页未填满
	// 也可能 IsTruncated，调用方不得用返回条数或某个 key 推断是否还有下一页。
	ListObjects(ctx context.Context, bucket, prefix string, maxKeys int, continuationToken string) (*ListObjectsPage, error)
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
