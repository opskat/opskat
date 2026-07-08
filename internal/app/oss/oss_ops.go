package oss

import (
	"fmt"

	"github.com/opskat/opskat/internal/service/oss_svc"
)

// OSSTestConnection 用已保存的资产拨号并列 Bucket 以验证凭证。
func (o *OSS) OSSTestConnection(assetID int64) error {
	if assetID <= 0 {
		return fmt.Errorf("invalid assetID")
	}
	return o.service.TestConnection(o.i18nCtx(), assetID)
}

// OSSListBuckets 列出账号下的所有 Bucket。
func (o *OSS) OSSListBuckets(assetID int64) ([]oss_svc.BucketItem, error) {
	if assetID <= 0 {
		return nil, fmt.Errorf("invalid assetID")
	}
	return o.service.ListBuckets(o.i18nCtx(), assetID)
}

// OSSListObjects 列出某 Bucket/前缀下的对象与子前缀。
func (o *OSS) OSSListObjects(req oss_svc.ListObjectsRequest) (*oss_svc.ListObjectsResult, error) {
	if req.AssetID <= 0 || req.Bucket == "" {
		return nil, fmt.Errorf("invalid request: assetID and bucket are required")
	}
	return o.service.ListObjects(o.i18nCtx(), &req)
}

// OSSStatObject 取单个对象的元信息。
func (o *OSS) OSSStatObject(req oss_svc.ObjectRequest) (*oss_svc.ObjectItem, error) {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return nil, fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	return o.service.StatObject(o.i18nCtx(), &req)
}

// OSSRemoveObject 删除单个对象。
func (o *OSS) OSSRemoveObject(req oss_svc.ObjectRequest) error {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	return o.service.RemoveObject(o.i18nCtx(), &req)
}

// OSSPresignGet 生成对象的预签名下载 URL。
func (o *OSS) OSSPresignGet(req oss_svc.PresignRequest) (string, error) {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	return o.service.PresignGet(o.i18nCtx(), &req)
}

// OSSPresignPut 生成对象的预签名上传 URL。
func (o *OSS) OSSPresignPut(req oss_svc.PresignRequest) (string, error) {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	return o.service.PresignPut(o.i18nCtx(), &req)
}
