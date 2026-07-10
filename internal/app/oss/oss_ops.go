package oss

import (
	"context"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/service/oss_svc"
)

func logOSSStart(ctx context.Context, operation string, assetID int64) {
	logger.Ctx(ctx).Info(operation+" start", zap.Int64("assetId", assetID))
}

func logOSSEnd(ctx context.Context, operation string, assetID int64, err error) {
	if err != nil {
		logger.Ctx(ctx).Error(operation+" fail", zap.Int64("assetId", assetID), zap.Error(err))
		return
	}
	logger.Ctx(ctx).Info(operation+" end", zap.Int64("assetId", assetID))
}

// OSSTestConnection 用已保存的资产拨号并列 Bucket 以验证凭证。
func (o *OSS) OSSTestConnection(assetID int64) error {
	if assetID <= 0 {
		return fmt.Errorf("invalid assetID")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss test connection", assetID)
	err := o.service.TestConnection(ctx, assetID)
	logOSSEnd(ctx, "oss test connection", assetID, err)
	return err
}

// OSSListBuckets 列出账号下的所有 Bucket。
func (o *OSS) OSSListBuckets(assetID int64) ([]oss_svc.BucketItem, error) {
	if assetID <= 0 {
		return nil, fmt.Errorf("invalid assetID")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss list buckets", assetID)
	items, err := o.service.ListBuckets(ctx, assetID)
	logOSSEnd(ctx, "oss list buckets", assetID, err)
	return items, err
}

// OSSListObjects 列出某 Bucket/前缀下的对象与子前缀。
func (o *OSS) OSSListObjects(req oss_svc.ListObjectsRequest) (*oss_svc.ListObjectsResult, error) {
	if req.AssetID <= 0 || req.Bucket == "" {
		return nil, fmt.Errorf("invalid request: assetID and bucket are required")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss list objects", req.AssetID)
	result, err := o.service.ListObjects(ctx, &req)
	logOSSEnd(ctx, "oss list objects", req.AssetID, err)
	return result, err
}

// OSSStatObject 取单个对象的元信息。
func (o *OSS) OSSStatObject(req oss_svc.ObjectRequest) (*oss_svc.ObjectItem, error) {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return nil, fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss stat object", req.AssetID)
	item, err := o.service.StatObject(ctx, &req)
	logOSSEnd(ctx, "oss stat object", req.AssetID, err)
	return item, err
}

// OSSRemoveObject 删除单个对象。
func (o *OSS) OSSRemoveObject(req oss_svc.ObjectRequest) error {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss remove object", req.AssetID)
	err := o.service.RemoveObject(ctx, &req)
	logOSSEnd(ctx, "oss remove object", req.AssetID, err)
	return err
}

// OSSPresignGet 生成对象的预签名下载 URL。
func (o *OSS) OSSPresignGet(req oss_svc.PresignRequest) (string, error) {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss presign get", req.AssetID)
	url, err := o.service.PresignGet(ctx, &req)
	logOSSEnd(ctx, "oss presign get", req.AssetID, err)
	return url, err
}

// OSSPresignPut 生成对象的预签名上传 URL。
func (o *OSS) OSSPresignPut(req oss_svc.PresignRequest) (string, error) {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss presign put", req.AssetID)
	url, err := o.service.PresignPut(ctx, &req)
	logOSSEnd(ctx, "oss presign put", req.AssetID, err)
	return url, err
}

// OSSCopyObject 服务端复制单个对象(同/跨 Bucket 与前缀)。
func (o *OSS) OSSCopyObject(req oss_svc.CopyRequest) error {
	if req.AssetID <= 0 || req.SrcBucket == "" || req.SrcKey == "" || req.DstBucket == "" || req.DstKey == "" {
		return fmt.Errorf("invalid request: assetID, src/dst bucket and key are required")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss copy object", req.AssetID)
	if err := o.service.CopyObject(ctx, &req); err != nil {
		logOSSEnd(ctx, "oss copy object", req.AssetID, err)
		return err
	}
	logger.Ctx(ctx).Info("oss copy object",
		zap.Int64("assetId", req.AssetID),
		zap.String("srcBucket", req.SrcBucket), zap.String("srcKey", req.SrcKey),
		zap.String("dstBucket", req.DstBucket), zap.String("dstKey", req.DstKey))
	logOSSEnd(ctx, "oss copy object", req.AssetID, nil)
	return nil
}

// OSSMoveObject 复制成功后删除源,覆盖重命名与移动。
func (o *OSS) OSSMoveObject(req oss_svc.CopyRequest) error {
	if req.AssetID <= 0 || req.SrcBucket == "" || req.SrcKey == "" || req.DstBucket == "" || req.DstKey == "" {
		return fmt.Errorf("invalid request: assetID, src/dst bucket and key are required")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss move object", req.AssetID)
	if err := o.service.MoveObject(ctx, &req); err != nil {
		logOSSEnd(ctx, "oss move object", req.AssetID, err)
		return err
	}
	logger.Ctx(ctx).Info("oss move object",
		zap.Int64("assetId", req.AssetID),
		zap.String("srcBucket", req.SrcBucket), zap.String("srcKey", req.SrcKey),
		zap.String("dstBucket", req.DstBucket), zap.String("dstKey", req.DstKey))
	logOSSEnd(ctx, "oss move object", req.AssetID, nil)
	return nil
}

// OSSRemoveObjects 批量删除对象(一次调用一条关键流日志)。
func (o *OSS) OSSRemoveObjects(req oss_svc.RemoveObjectsRequest) error {
	if req.AssetID <= 0 || req.Bucket == "" || len(req.Keys) == 0 {
		return fmt.Errorf("invalid request: assetID, bucket and non-empty keys are required")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss remove objects", req.AssetID)
	if err := o.service.RemoveObjects(ctx, &req); err != nil {
		logOSSEnd(ctx, "oss remove objects", req.AssetID, err)
		return err
	}
	logger.Ctx(ctx).Info("oss remove objects",
		zap.Int64("assetId", req.AssetID),
		zap.String("bucket", req.Bucket),
		zap.Int("count", len(req.Keys)))
	logOSSEnd(ctx, "oss remove objects", req.AssetID, nil)
	return nil
}

// OSSCreateFolder 在指定前缀下新建"文件夹"(零字节占位对象)。
func (o *OSS) OSSCreateFolder(req oss_svc.CreateFolderRequest) error {
	if req.AssetID <= 0 || req.Bucket == "" || req.Prefix == "" {
		return fmt.Errorf("invalid request: assetID, bucket and prefix are required")
	}
	ctx := o.i18nCtx()
	logOSSStart(ctx, "oss create folder", req.AssetID)
	if err := o.service.CreateFolder(ctx, &req); err != nil {
		logOSSEnd(ctx, "oss create folder", req.AssetID, err)
		return err
	}
	logger.Ctx(ctx).Info("oss create folder",
		zap.Int64("assetId", req.AssetID),
		zap.String("bucket", req.Bucket),
		zap.String("prefix", req.Prefix))
	logOSSEnd(ctx, "oss create folder", req.AssetID, nil)
	return nil
}
