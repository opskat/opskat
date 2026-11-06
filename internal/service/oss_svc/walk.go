package oss_svc

import (
	"context"
	"fmt"
	"strings"
)

// ListPageFunc 取 prefix 之下**一层**的一页,即 ListObjectsWith 的那份契约。
type ListPageFunc func(ctx context.Context, prefix, continuationToken string) (*ListObjectsResult, error)

// WalkObjects 把 prefix 之下**任意深度**的内容逐页拉全,每一项交给 visit 恰好一次:
// 对象原样交出;子前缀("文件夹")以 IsPrefix 的一项交出,且先于它之下的任何内容。
//
// 列举是分层的(一次调用只看一层,下层作为公共前缀单独返回),所以下钻在这里。分页使用
// S3 返回的 opaque continuation token;它不是对象 key,不能比较或自行解释。
//
// 展开顺序是"本层对象、再逐个子树",服务端每一页内按 key 字典序——同一份桶内容展开出的
// 顺序固定。越出 prefix 的返回项与原地打转的游标都当错误处理:调用方拿这棵树去做的事
// (授权、落盘)都以"它确实在 prefix 之下"为前提。
func WalkObjects(ctx context.Context, list ListPageFunc, prefix string, visit func(ObjectItem) error) error {
	token := ""
	seenTokens := map[string]bool{"": true}
	for {
		res, err := list(ctx, prefix, token)
		if err != nil {
			return err
		}
		for _, obj := range res.Objects {
			if !strings.HasPrefix(obj.Key, prefix) {
				return fmt.Errorf("oss list for prefix %q returned object %q outside that prefix", prefix, obj.Key)
			}
			if err := visit(obj); err != nil {
				return err
			}
		}
		for _, sub := range res.Prefixes {
			if !strings.HasPrefix(sub, prefix) || len(sub) <= len(prefix) {
				return fmt.Errorf("oss list for prefix %q returned non-child prefix %q", prefix, sub)
			}
			if err := visit(ObjectItem{Key: sub, IsPrefix: true}); err != nil {
				return err
			}
			if err := WalkObjects(ctx, list, sub, visit); err != nil {
				return err
			}
		}
		if !res.IsTruncated {
			return nil
		}
		next := res.NextContinuationToken
		if next == "" || seenTokens[next] {
			return fmt.Errorf("oss list for prefix %q returned a non-advancing continuation token", prefix)
		}
		seenTokens[next] = true
		token = next
	}
}
