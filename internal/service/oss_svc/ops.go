package oss_svc

import (
	"context"
	"fmt"
	"strings"
)

const defaultListMaxKeys = 200

// listObjectsWith 读一层有界页:拆"文件夹"前缀与对象;超出 maxKeys 则回续传游标。
func listObjectsWith(ctx context.Context, c Client, bucket, prefix string, maxKeys int, startAfter string) (*ListObjectsResult, error) {
	limit := maxKeys
	if limit <= 0 {
		limit = defaultListMaxKeys
	}
	items, err := c.ListObjects(ctx, bucket, prefix, limit, startAfter)
	if err != nil {
		return nil, err
	}
	res := &ListObjectsResult{Prefixes: []string{}, Objects: []ObjectItem{}}
	if len(items) > limit {
		res.IsTruncated = true
		res.NextContinuationToken = items[limit-1].Key
		items = items[:limit]
	}
	for _, it := range items {
		if it.IsPrefix {
			res.Prefixes = append(res.Prefixes, it.Key)
		} else {
			res.Objects = append(res.Objects, it)
		}
	}
	return res, nil
}

func copyObjectWith(ctx context.Context, c Client, srcBucket, srcKey, dstBucket, dstKey string) error {
	return c.CopyObject(ctx, srcBucket, srcKey, dstBucket, dstKey)
}

// moveObjectWith 先 copy 成功再删源;copy 失败原样返回,绝不删除源对象。
func moveObjectWith(ctx context.Context, c Client, srcBucket, srcKey, dstBucket, dstKey string) error {
	if err := c.CopyObject(ctx, srcBucket, srcKey, dstBucket, dstKey); err != nil {
		return err
	}
	return c.RemoveObject(ctx, srcBucket, srcKey)
}

func removeObjectsWith(ctx context.Context, c Client, bucket string, keys []string) error {
	return c.RemoveObjects(ctx, bucket, keys)
}

// normalizeFolderPrefix 校验并规范化文件夹前缀为以 "/" 结尾;空则报错。
func normalizeFolderPrefix(prefix string) (string, error) {
	p := strings.TrimSpace(prefix)
	if p == "" {
		return "", fmt.Errorf("文件夹前缀不能为空")
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p, nil
}

// createFolderWith 对 <prefix>/ 做零字节 PUT(S3 文件夹占位符约定)。
func createFolderWith(ctx context.Context, c Client, bucket, prefix string) error {
	normalized, err := normalizeFolderPrefix(prefix)
	if err != nil {
		return err
	}
	return c.PutObject(ctx, bucket, normalized, strings.NewReader(""), 0, "application/x-directory")
}
