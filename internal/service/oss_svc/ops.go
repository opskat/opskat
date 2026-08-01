package oss_svc

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

const defaultListMaxKeys = 200

// ListObjectsWith 读一层有界页:拆"文件夹"前缀与对象;超出 maxKeys 则回续传游标。
//
// 导出是因为它是**分页契约本身**,而 Service 的 connect 没有注入点:任何要按这份契约
// 逐页拉全一棵子树的调用方(internal/ai/helper 的传输适配器),都得能拿一个假 Client
// 把这段真实现跑起来,而不是在自己的测试里照抄一份截断逻辑——照抄的那份会与这里漂移,
// 而漂移出来的恰恰是"测试模型比真实客户端更宽容",于是缺陷在绿灯下活着。
func ListObjectsWith(ctx context.Context, c Client, bucket, prefix string, maxKeys int, startAfter string) (*ListObjectsResult, error) {
	limit := maxKeys
	if limit <= 0 {
		limit = defaultListMaxKeys
	}
	items, err := c.ListObjects(ctx, bucket, prefix, limit, startAfter)
	if err != nil {
		return nil, err
	}
	res := &ListObjectsResult{Prefixes: []string{}, Objects: []ObjectItem{}}
	// 截断之前先按 key 排序:Client 交来的这一串**不是**按 key 有序的。minio 对每个 S3
	// 响应先逐条交出 Contents、再逐条交出 CommonPrefixes(api-list.go:164-177),而 S3 是把
	// 两者合起来按 key 序截到 MaxKeys 的。照原顺序取"第 limit 条"当游标,得到的既不是本页
	// 最大的 key、也不是分界线,于是两侧各漏一种:排在游标之前的公共前缀被丢掉后,
	// startAfter 排他,它再也回不来(cp -r 少传一整棵子树还报成功);排在游标之后的对象
	// 本页已交出去,下一页又交一遍(重复条目与重复读写)。
	// 排序之后,留下的恰好是最小的 limit 条,游标就是它们的最大值——两条都不再可能。
	slices.SortStableFunc(items, func(a, b ObjectItem) int { return strings.Compare(a.Key, b.Key) })
	if len(items) > limit {
		res.IsTruncated = true
		res.NextContinuationToken = items[limit-1].Key
		items = items[:limit]
	}
	for _, it := range items {
		if it.IsPrefix {
			if it.Key == prefix {
				continue
			}
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
