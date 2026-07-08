package oss_svc

import "context"

// listObjectsWith 把一层平铺列表拆成"文件夹"前缀与对象。
func listObjectsWith(ctx context.Context, c Client, bucket, prefix string) (*ListObjectsResult, error) {
	items, err := c.ListObjects(ctx, bucket, prefix)
	if err != nil {
		return nil, err
	}
	res := &ListObjectsResult{}
	for _, it := range items {
		if it.IsPrefix {
			res.Prefixes = append(res.Prefixes, it.Key)
		} else {
			res.Objects = append(res.Objects, it)
		}
	}
	return res, nil
}
