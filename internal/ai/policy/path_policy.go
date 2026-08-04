package policy

import (
	"path"
	"strings"
)

// MatchPathRule 按 POSIX glob 匹配文件路径，`*` 不跨 `/`；尾随 `/` 的规则表示该目录的
// 整棵子树，用于递归 cp 的目录范围授权。
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
	// 授权匹配必须使用规范路径。否则一条 /safe/ 目录 grant 会按字符串前缀放行
	// /safe/../etc/passwd，而文件系统最终解析到 /etc/passwd。这里仍然 fail-closed，端点
	// 边界负责把正常输入规范化后同时交给审批、审计和 I/O。
	if !canonicalPathName(filePath) {
		return false
	}
	if rule == "*" || rule == filePath {
		return true
	}
	if strings.HasSuffix(rule, "/") {
		return strings.HasPrefix(filePath, rule)
	}
	ok, err := path.Match(rule, filePath)
	return err == nil && ok
}

func canonicalPathName(name string) bool {
	if strings.HasSuffix(name, "/") && name != "/" {
		return path.Clean(strings.TrimSuffix(name, "/"))+"/" == name
	}
	return path.Clean(name) == name
}
