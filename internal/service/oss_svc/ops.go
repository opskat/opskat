package oss_svc

import "context"

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
