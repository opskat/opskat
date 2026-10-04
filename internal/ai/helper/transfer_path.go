package helper

import (
	"context"
	"fmt"
	"runtime"
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
// 查询。Windows 盘符（"E:/src/..."、"E:\src\..."）在切分之前就判成本地路径，优先于同名
// 资产，也不查资产。其余情况冒号后必须以 "/" 开头，或是 SSH 支持的 "~" / "~/..."，才算远端
// 路径。冒号后不是这种写法时，D15 守卫会查一次前缀：查到资产就报错，查不到就当本地路径
// （如非 Windows 上的 "C:\windows"）。home 语法在适配器接线后解析，解析出的绝对路径才进入
// 审批与 I/O。
//
// D15 守卫（spec §6.2 / §11）：冒号后不以 "/" 开头、但前缀确实解析成了一个资产时报错，
// 明示正确写法 "<asset>:/<path>"，而不是静默回落成本地路径——写错的远端路径不该变成一句
// 对着磁盘上某个字面路径的"文件不存在"。
func ParseTransferEndpoint(
	ctx context.Context, s string, resolve ResolveAssetFunc,
) (asset *asset_entity.Asset, path string, err error) {
	if isWindowsDrivePath(s) {
		return nil, s, nil
	}
	idx := strings.Index(s, ":")
	if idx <= 0 {
		return nil, s, nil
	}
	prefix := s[:idx]
	rest := s[idx+1:]

	if strings.HasPrefix(rest, "/") || rest == "~" || strings.HasPrefix(rest, "~/") {
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
			"remote paths must be written %s:/<path> (SSH also accepts %s:~/<path>); got %q",
			prefix, prefix, s)
	}
	return nil, s, nil
}

// isWindowsDrivePath 认出 "E:/src/..." 和 "E:\\src\\..."。只在 Windows 上算盘符：
// 别的系统没有这种本地路径，单字母前缀仍按资产引用解析。
func isWindowsDrivePath(s string) bool {
	if runtime.GOOS != "windows" || len(s) < 3 || s[1] != ':' {
		return false
	}
	c := s[0]
	if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
		return false
	}
	return s[2] == '/' || s[2] == '\\'
}
