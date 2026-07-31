package helper

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// flagCheck 校验单个 flag 值的形状；nil 表示自由文本。
//
// 每个非平凡的形状都应**直接调用真正会消费这个值的那个构造器**，而不是另写一遍解析：
// 形状校验与实际解析必须不可能分叉，否则会出现"校验放行、构造器报错"——正是这层
// 要消灭的"批准之后必然失败"。kafka_exec.go 里的各个 check（kafkaConfigUpdatesJSON
// 等）是这条规则的范本，代价是构造器被调用两次（一次校验、一次真执行），都是纯字符串
// 解析，无副作用。
//
// kafka 与 oss 的 exec DSL 共用这一套机件（flagCheck / flags / flagSpec /
// validateFlags / supportedFlagList）：两边"一个 (family, verb) 认得哪些 flag、
// 每个值该长什么样"这层校验逐字节同形，只是各自的 per-verb 表（kafkaFlagRules /
// ossFlagRules）与 flag 名等价关系（normalize）不同。per-verb 表不在这里——它是
// 各协议独有的知识，留在 kafka_exec.go / oss_dsl.go 各自的文件里。
type flagCheck func(value string) error

// flags 把 flag 名（各 DSL 自己的拼写）映射到值的形状校验。
type flags = map[string]flagCheck

// flagSpec 是某个 (family, verb) 认得的 flag 集合，以及每个 flag 值的形状。
// required/optional 各自"收什么"是协议特有的判断，由调用方的 per-verb 表决定。
type flagSpec struct {
	required flags
	optional flags
}

// supportedFlagList 把一个 flagSpec 渲染成报错文本里 "supported: --a, --b" 那一段。
func supportedFlagList(spec flagSpec) string {
	all := slices.Concat(slices.Collect(maps.Keys(spec.required)), slices.Collect(maps.Keys(spec.optional)))
	if len(all) == 0 {
		return "this verb takes no flags"
	}
	slices.Sort(all)
	return "--" + strings.Join(all, ", --")
}

// providedFlag 记住模型实际写下的拼写：normalize 把多种拼写折叠到同一个 key 时
// （kafka 的连字符/下划线），报重复要把两种写法都还给它，只报归一化后的名字会让
// 它看不出自己写了哪两个。
type providedFlag struct {
	name  string
	value string
}

// validateFlags 拒绝这一层——也只有这一层——能判定的坏 flag：未知 flag、
// （normalize 非恒等时）折叠到同一 key 的重复拼写、缺失的必填 flag、坏形状的值。
// per-verb 的合法 flag 集合是各协议执行器独有的知识，调用方按自己的表查出 spec、把
// family/verb（供错误文本用）、given（命令解析出的原始 flag）与 normalize 一并传入。
//
// normalize 是这个 DSL 的 flag 名等价关系：kafka 把连字符与下划线两种写法折叠成同一个
// 参数（DSL 写 --replication-factor，args key 是 replication_factor），因此
// `--replication-factor` 与 `--replication_factor` 一起给必须被当成重复，而不是悄悄
// 后写的盖掉先写的（normalizeKafkaFlagName，kafka_exec.go）。oss 没有这种别名，传 nil
// （等价于恒等函数）——cmdline.Parse 本身已经按原始拼写去重，恒等 normalize 下
// provided 的 key 集合与 given 的 key 集合一一对应，"重复拼写"分支永远不会触发，行为
// 退化为逐字面量匹配（ParseOSSCommand 就是靠这一点让 `--max_keys` 仍是未知 flag，
// 而不是 `--max-keys` 的另一种写法）。
func validateFlags(family, verb string, given map[string]string, spec flagSpec, normalize func(string) string) error {
	if normalize == nil {
		normalize = func(s string) string { return s }
	}

	known := make(flags, len(spec.required)+len(spec.optional))
	for name, check := range spec.required {
		known[normalize(name)] = check
	}
	for name, check := range spec.optional {
		known[normalize(name)] = check
	}

	// unknown 排序后一次性报出：given 是 map，只报"碰到的第一个"会让同一条命令在
	// 不同次调用里报出不同的 flag 名，而这段文本是发给模型的。
	var unknown []string
	provided := make(map[string]providedFlag, len(given))
	for name, value := range given {
		normalized := normalize(name)
		if _, ok := known[normalized]; !ok {
			unknown = append(unknown, name)
			continue
		}
		// 连字符与下划线两种写法归一后是同一个 key，后写的会盖掉先写的，cmdline 的
		// 重名检查看的是原始拼写、拦不住这一对（normalize 恒等时这个分支永远不触发，
		// 因为 given 的 key 本身已经是唯一的）。报错时把两个拼写排序后输出，否则同一条
		// 命令每次报出的顺序都不一样（map 迭代顺序随机）。
		if prev, dup := provided[normalized]; dup {
			names := []string{prev.name, name}
			slices.Sort(names)
			return fmt.Errorf("flags --%s and --%s are the same parameter %q; pass it once",
				names[0], names[1], normalized)
		}
		provided[normalized] = providedFlag{name: name, value: value}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("unknown flag(s) --%s for %q %q; supported: %s",
			strings.Join(unknown, ", --"), family, verb, supportedFlagList(spec))
	}

	// 必填先于形状：一个 flag 完全没给和给了个坏值是两条不同的提示，先报缺失更准。
	var missing []string
	for name := range spec.required {
		if strings.TrimSpace(provided[normalize(name)].value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("%q %q requires --%s", family, verb, strings.Join(missing, ", --"))
	}

	// 名字排序遍历，保证同一条命令每次报出同一个 flag。
	for _, normalized := range slices.Sorted(maps.Keys(provided)) {
		check := known[normalized]
		if check == nil {
			continue
		}
		flag := provided[normalized]
		if err := check(flag.value); err != nil {
			return fmt.Errorf("invalid value for --%s: %w", flag.name, err)
		}
	}
	return nil
}
