package policy

import "path"

// MatchPathRule 按 POSIX glob 匹配文件路径，`*` 不跨 `/`。
//
// 文件传输授权（cp）与 local_write / local_edit 的路径白名单共用这一份实现：
// 命令用 MatchCommandRule，路径用本函数，两者不可互换——把路径 pattern 交给命令
// 匹配器，一条 `/opt/*` 授权就能放行任意命令。
//
// pattern 非法或双方为空时按不匹配处理（fail-closed）。
func MatchPathRule(rule, filePath string) bool {
	if rule == "" || filePath == "" {
		return false
	}
	if rule == "*" || rule == filePath {
		return true
	}
	ok, err := path.Match(rule, filePath)
	return err == nil && ok
}
