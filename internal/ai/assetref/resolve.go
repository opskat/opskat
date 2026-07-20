// Package assetref 把 LLM 传入的资产标识（数字 id 或名称）解析为资产实体。
// exec / help / batch_exec 共用它，避免各自实现一遍 name→id 查询。
package assetref

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// ErrAmbiguous 在同名资产多于一个时返回。名称不是唯一键，静默取第一个会让模型
// 对着错误的机器执行命令，因此这里必须报错并要求改用数字 id。
type ErrAmbiguous struct {
	Ref string
	IDs []int64
}

func (e *ErrAmbiguous) Error() string {
	parts := make([]string, 0, len(e.IDs))
	for _, id := range e.IDs {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return fmt.Sprintf(
		"asset reference %q is ambiguous, it matches ids [%s]; use the numeric id instead",
		e.Ref, strings.Join(parts, ", "))
}

// Resolve 解析资产标识。纯数字按 id 查，否则按名称精确匹配。
func Resolve(ctx context.Context, ref string) (*asset_entity.Asset, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("missing required parameter: asset")
	}

	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		asset, err := asset_svc.Asset().Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("asset not found: %s", ref)
		}
		return asset, nil
	}

	assets, err := asset_svc.Asset().List(ctx, "", 0)
	if err != nil {
		return nil, err
	}

	var matched []*asset_entity.Asset
	for _, a := range assets {
		if a.Name == ref {
			matched = append(matched, a)
		}
	}

	switch len(matched) {
	case 0:
		return nil, fmt.Errorf("asset not found: %s", ref)
	case 1:
		return matched[0], nil
	default:
		ids := make([]int64, 0, len(matched))
		for _, a := range matched {
			ids = append(ids, a.ID)
		}
		return nil, &ErrAmbiguous{Ref: ref, IDs: ids}
	}
}
