package aictx

import (
	"encoding/json"
	"strconv"
	"strings"

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

// ArgBool 从 tool 参数 map 中提取 bool，兼容 LLM 传入的原生 bool 与字符串 "true"。
func ArgBool(args map[string]any, key string) bool {
	if v, ok := args[key]; ok {
		switch b := v.(type) {
		case bool:
			return b
		case string:
			return strings.EqualFold(strings.TrimSpace(b), "true")
		}
	}
	return false
}

// ArgInt64 从 tool 参数 map 中提取 int64,兼容 JSON 反序列化后的 float64 / json.Number,
// 以及以字符串形式给出的数值。
//
// string 分支不是给 kafka 开的特例:这组 Arg* 函数的职责就是在 IPC/LLM 边界上容忍模型
// 给出的各种标量写法(ArgBool 早就容忍 "true"),缺 string 分支是它自己的缺口——统一 exec
// 的命令 DSL 把 --limit=500 这类 flag 原样以字符串送进来,落到这里返回 0 是**静默**的:
// Limit 变 0 之后 service 套用默认条数,用户批准了取 500 条、拿到别的数字,不报错。
// kafka_args.go 的 ArgOptionalPartition 自己单独做了一份 string 解析,正是这个缺口的症状。
//
// 解析不出来一律返回 0,**越界也算解析不出来**:strconv.ParseInt 与 json.Number.Int64
// (它包着同一个 ParseInt)在 ErrRange 时返回的是**钳位后的** MaxInt64/MinInt64 而不是 0,
// 顺手把 i 交出去会绕过调用方的守卫。0 在这组调用方那里是 fail-closed 哨兵
// (tool_handlers_asset.go 的 `if id == 0 { error }`、`if gid > 0`,kafka_svc 的
// `Partitions <= 0`),MaxInt64 不是:它一路通过那些守卫,再被 kafka_args.go 收窄成
// int32 时变成 -1,而 -1 恰好是 Kafka 线上协议里 "用 broker 默认值" 的哨兵。
// 越界输入本就没有正确答案,返回 0 让它落到各调用方既有的必填校验上,是唯一安全的选择。
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
				return 0
			}
			return i
		case string:
			i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
			if err != nil {
				logger.Default().Warn("convert string to int64", zap.String("value", n), zap.Error(err))
				return 0
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
