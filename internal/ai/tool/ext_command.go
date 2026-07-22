package tool

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
	"github.com/opskat/opskat/pkg/extension"
)

// parseExtCommand 把 `<extension> <tool> --flag value` 解析成扩展名、工具名与
// 按 manifest 声明类型转换过的 JSON 参数。
//
// 切词复用 internal/ai/cmdline（Plan B Task 1 建的引号感知 tokenizer），与 exec 侧
// 同一份实现——引号处理写第二遍必然会漂移，那份实现的三轮评审（黑名单漏字符导致
// 静默截断、Render 不引用 Verb、手抄的保留字表漏 8 个词）已经证明这块很难一次写对。
//
// --json 是逃生口：flag DSL 表达不了嵌套结构，而 manifest 允许声明它们；没有逃生口
// 就会出现"注册了却调不动"的工具。它与其它 flag 互斥——两者混用时哪个赢都是猜，
// 不如直接报错点名 --json，逼调用方要么全用 --json，要么全用具名 flag。
func parseExtCommand(command string, def extension.ToolDef) (string, string, []byte, error) {
	c, err := cmdline.Parse(command)
	if err != nil {
		return "", "", nil, fmt.Errorf("ext_exec: %w", err)
	}
	if len(c.Args) == 0 {
		return "", "", nil, fmt.Errorf("ext_exec: command %q names an extension but no tool; use `<extension> <tool> [--flags]`", command)
	}
	extName, toolName := c.Verb, c.Args[0]
	if len(c.Args) > 1 {
		// 位置参数无处可去：manifest 的 parameters 只有具名属性。静默丢弃会让模型
		// 以为传进去了。
		return "", "", nil, fmt.Errorf("ext_exec: unexpected positional argument %q; extension tools take named flags only", c.Args[1])
	}

	if raw, ok := c.Flags["json"]; ok {
		if len(c.Flags) > 1 {
			return "", "", nil, fmt.Errorf("ext_exec: --json cannot be combined with other flags")
		}
		if !json.Valid([]byte(raw)) {
			return "", "", nil, fmt.Errorf("ext_exec: --json value is not valid JSON")
		}
		return extName, toolName, []byte(raw), nil
	}

	props, _ := def.Parameters["properties"].(map[string]any)
	args := make(map[string]any, len(c.Flags))
	for name, raw := range c.Flags {
		prop, ok := props[name].(map[string]any)
		if !ok {
			return "", "", nil, fmt.Errorf("ext_exec: %s.%s has no parameter %q (declared: %s)",
				extName, toolName, name, strings.Join(declaredParamNames(props), ", "))
		}
		typ, _ := prop["type"].(string)
		value, cerr := convertExtFlag(raw, typ)
		if cerr != nil {
			return "", "", nil, fmt.Errorf("ext_exec: --%s: %w", name, cerr)
		}
		args[name] = value
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return "", "", nil, fmt.Errorf("ext_exec: marshal args: %w", err)
	}
	return extName, toolName, argsJSON, nil
}

// convertExtFlag 按 manifest 声明的类型转换 flag 值。类型不是可选的——
// pkg/extension 的加载期校验（Manifest.validateTools）保证每个属性都带 type，
// 所以这里的 default 分支只可能被将来新增的类型触发，报错而非静默透传字符串。
func convertExtFlag(raw, typ string) (any, error) {
	switch typ {
	case "string":
		return raw, nil
	case "integer":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", raw)
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return f, nil
	case "boolean":
		// 裸 `--flag` 经 cmdline.Parse 得到值 "true"，所以无需特判。
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a boolean", raw)
		}
		return b, nil
	case "array":
		// 加载期校验已保证只有 array<string>。逗号切分是 CLI 惯例；
		// 值里真需要逗号时走 --json。
		return strings.Split(raw, ","), nil
	default:
		return nil, fmt.Errorf("unsupported parameter type %q", typ)
	}
}

// declaredParamNames 返回排序后的属性名清单，供未声明 flag 的报错文案使用。
// 报错文案进模型上下文，map 迭代顺序随机会让同一个错误每次读起来都不一样。
func declaredParamNames(props map[string]any) []string {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
