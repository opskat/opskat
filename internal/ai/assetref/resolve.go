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

// Resolve 解析资产标识。纯数字既按 id 查，也按名称查——资产名称允许纯数字
// （Validate 只拒绝空名称，name 列也没有唯一索引），如果两者命中不同的资产，
// 说明引用有歧义，必须报错而不是默默选一个，否则可能对着错误的机器执行命令。
func Resolve(ctx context.Context, ref string) (*asset_entity.Asset, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("missing required parameter: asset")
	}

	nameMatches, err := asset_svc.Asset().FindByName(ctx, ref)
	if err != nil {
		return nil, err
	}

	// 按 id 去重合并候选：同一个资产既匹配 id 又匹配名称时不算歧义。
	candidates := make(map[int64]*asset_entity.Asset, len(nameMatches)+1)
	order := make([]int64, 0, len(nameMatches)+1)
	add := func(a *asset_entity.Asset) {
		if _, exists := candidates[a.ID]; !exists {
			order = append(order, a.ID)
		}
		candidates[a.ID] = a
	}

	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		if idAsset, err := asset_svc.Asset().Get(ctx, id); err == nil {
			add(idAsset)
		}
	}
	for _, a := range nameMatches {
		add(a)
	}

	switch len(order) {
	case 0:
		return nil, fmt.Errorf("asset not found: %s", ref)
	case 1:
		return candidates[order[0]], nil
	default:
		return nil, &ErrAmbiguous{Ref: ref, IDs: order}
	}
}
