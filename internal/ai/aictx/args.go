package aictx

import (
	"encoding/json"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// ArgString 从 tool 参数 map 中提取 string 值,缺失/类型不匹配返回空串。
func ArgString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ArgInt64 从 tool 参数 map 中提取 int64,兼容 JSON 反序列化后的 float64 / json.Number。
func ArgInt64(args map[string]any, key string) int64 {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int:
			return int64(n)
		case int64:
			return n
		case json.Number:
			i, err := n.Int64()
			if err != nil {
				logger.Default().Warn("convert json.Number to int64", zap.String("value", n.String()), zap.Error(err))
			}
			return i
		}
	}
	return 0
}

// ArgInt 是 ArgInt64 的 int 收窄包装。
func ArgInt(args map[string]any, key string) int {
	return int(ArgInt64(args, key))
}
