package helper

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ResolveAssetFunc 把 "<asset>:" 前缀解析成资产。由调用方注入：**共享的是语法，不是引用
// 解析策略**——opsctl 传自己的 resolveAsset（`cmd/opsctl/command/resolve.go`，支持
// "组路径/名称" 消歧），AI 侧传 assetref.Resolve（数字 id 或精确名称）。两者在解析器上提
// 之前就已经不同，共享解析器不能替入口选一个。
//
// 解析不出资产时返回 error；ParseTransferEndpoint 只看 error 是否为 nil，不解读它的种类。
type ResolveAssetFunc func(ctx context.Context, ref string) (*asset_entity.Asset, error)

// ParseTransferEndpoint 解析 cp 的一端："<asset>:<path>"，AI cp 工具与 opsctl cp 共用。
// 只按第一个冒号切，其后整段都是该资产上的路径（路径里可以再有冒号）。
//
// 无冒号、或冒号前为空（":foo"）一律是本地路径：asset 为 nil、path 是原串，且不做任何资产
// 查询。否则冒号后必须以 "/" 开头才算远端路径——这正是 "C:\windows" 仍判为本地的原因
// （下面的 D15 守卫仍会查一次前缀以决定要不要报错，查不到就还是本地）。
//
// D15 守卫（spec §6.2 / §11）：冒号后不以 "/" 开头、但前缀确实解析成了一个资产时报错，
// 明示正确写法 "<asset>:/<path>"，而不是静默回落成本地路径——写错的远端路径不该变成一句
// 对着磁盘上某个字面路径的"文件不存在"。
func ParseTransferEndpoint(
	ctx context.Context, s string, resolve ResolveAssetFunc,
) (asset *asset_entity.Asset, path string, err error) {
	idx := strings.Index(s, ":")
	if idx <= 0 {
		return nil, s, nil
	}
	prefix := s[:idx]
	rest := s[idx+1:]

	if strings.HasPrefix(rest, "/") {
		a, resolveErr := resolve(ctx, prefix)
		if resolveErr != nil {
			return nil, "", fmt.Errorf("resolving asset %q: %w", prefix, resolveErr)
		}
		return a, rest, nil
	}

	// 前缀真的是资产才报错；否则这是一个恰好带冒号的本地路径（如 Windows 盘符）。解析失败
	// 在这一支里是"前缀不是资产"这一个信号，不是要上报的错误。
	if _, resolveErr := resolve(ctx, prefix); resolveErr == nil {
		return nil, "", fmt.Errorf(
			"remote paths must be written %s:/<path> (missing leading \"/\" after the colon); got %q", prefix, s)
	}
	return nil, s, nil
}
