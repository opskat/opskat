package helper

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// MongoCommand 是 mongo 富命令串的结构形式。
//
// 格式：`<op> [collection] [--db=<database>] [--query=<json>]`
//
// 之所以让 collection 走 positional、database 走 flag：collection 是几乎每次调用
// 都要给的字段，正好也是最自然的 positional 用法。database **没有隐式默认**——
// mongoFind 等执行函数（internal/ai/helper/mongodb_helper.go:201 及同类）在
// database 为空时直接报错，这条路径也从不读取资产的 MongoDBConfig.Database
// （internal/model/entity/asset_entity/asset.go:251）。要不要在接入统一 exec 时
// 从资产配置注入默认 database，是下一个任务的决定（会进它的 CanonicalizeFunc，
// 参照 k8s 注入 --context/--namespace 的方式），这里不做。
type MongoCommand struct {
	Op         string
	Database   string
	Collection string
	Query      string
}

// ParseMongoCommand 解析富命令串。
func ParseMongoCommand(s string) (*MongoCommand, error) {
	parsed, err := cmdline.Parse(s)
	if err != nil {
		return nil, err
	}

	spec, ok := mongoOps[parsed.Verb]
	if !ok {
		return nil, fmt.Errorf("unsupported mongo operation %q; supported: %s",
			parsed.Verb, strings.Join(slices.Sorted(maps.Keys(mongoOps)), ", "))
	}

	c := &MongoCommand{Op: parsed.Verb}
	if len(parsed.Args) > 1 {
		return nil, fmt.Errorf("mongo commands take at most one positional argument (the collection); got %d", len(parsed.Args))
	}
	if len(parsed.Args) == 1 {
		if !spec.needsCollection {
			return nil, fmt.Errorf("operation %q does not take a collection argument; got %q", c.Op, parsed.Args[0])
		}
		c.Collection = parsed.Args[0]
	}
	if spec.needsCollection && c.Collection == "" {
		return nil, fmt.Errorf("operation %q requires a collection: %s <collection> [--query=...]", c.Op, c.Op)
	}

	// unknown 按名字排序后一次性报告：parsed.Flags 是 map，迭代顺序本身是随机的，
	// 只报第一个碰到的会让同样的输入（例如 `find u --a=1 --b=2`）在不同次调用里
	// 报出不同的 flag 名——这段文本是要发给模型的，不确定性只会让它瞎猜。
	var unknown []string
	for name, value := range parsed.Flags {
		switch name {
		case "db":
			c.Database = value
		case "query":
			c.Query = value
		default:
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, fmt.Errorf("unknown flag(s) --%s; mongo supports --db and --query", strings.Join(unknown, ", --"))
	}
	return c, nil
}

// Render 是 ParseMongoCommand 的逆函数（TestMongoCommand_RoundTrip 锁住）。
//
// 拒绝以 "--" 开头的 Collection。cmdline.Parse 只看词本身（Words 已经剥掉引号
// 之后）是否以 "--" 开头来判断它是 positional 还是 flag
// （internal/ai/cmdline/cmdline.go:162）——这个判断发生在引号信息已经丢失之后，
// 所以给 "--db=evil" 加引号并不能让它安全地回代成一个 positional：Render 出来的
// 串重新解析后，"--db=evil" 依然会被当成 --db=evil 这个 flag（Collection 变成
// 空字符串），单独一个 "--" 会被当成格式错误的 flag（"malformed flag: --"）。
// 这属于 cmdline 引号策略"位置无关"的已知局限，在 Verb 侧也有同样的记录
// （internal/ai/cmdline/cmdline.go:184-192），这里不重新设计 cmdline 的引号规则，
// 只在 mongo 这一层堵住它：ParseMongoCommand 产出的 Collection 永远不会以 "--"
// 开头（这种词在解析阶段就被当 flag 吃掉了），但 MongoCommand 是导出结构体，
// 统一 exec 那边会用结构化的 collection 工具参数直接构造它，绕开 Parse——
// 拒绝好过悄悄产出一个 Render 完语义就变了的串。
func (c *MongoCommand) Render() (string, error) {
	if strings.HasPrefix(c.Collection, "--") {
		return "", fmt.Errorf("collection %q cannot start with \"--\": it would be misread as a flag when the rendered command is re-parsed", c.Collection)
	}

	cmd := &cmdline.Command{Verb: c.Op, Flags: map[string]string{}}
	if c.Collection != "" {
		cmd.Args = []string{c.Collection}
	}
	if c.Database != "" {
		cmd.Flags["db"] = c.Database
	}
	if c.Query != "" {
		cmd.Flags["query"] = c.Query
	}
	return cmd.Render(), nil
}

// PolicyString 返回策略匹配用的串——**必须**是裸 operation token（下方注释说明理由）。
//
// mongo 的策略是 AllowTypes/DenyTypes 的精确匹配（policyValueMatches，
// internal/ai/policy/policy_effective.go:14-16），内置组 BuiltinMongoReadOnly 存的是
// "find" / "findOne" / "aggregate" / "countDocuments"。返回任何更丰富的形式都会让
// 内置组与全部存量 grant 静默失配（匹配失败不报错、不记日志，只是永远 NeedConfirm）。
//
// 代价是审批弹窗仍只显示操作名，看不到 collection 与 filter。审计不受此限——
// exec 的审计记录的是原始富命令（见 internal/ai/audit/extractor.go），比今天的
// 裸 token 信息更全。收窄审批展示粒度另开 issue，不在本 Plan。
func (c *MongoCommand) PolicyString() string { return c.Op }
