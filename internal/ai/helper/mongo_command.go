package helper

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// mongoOps 是支持的操作及其是否需要 collection。
// 与 ExecuteMongoDB 的 switch（internal/ai/helper/mongodb_helper.go:139-177）同源——
// 新增操作时两处必须一起改，TestParseMongoCommand_RejectsUnknownOp 会挡住只改一处。
var mongoOps = map[string]bool{
	"find":            true,
	"findOne":         true,
	"insertOne":       true,
	"insertMany":      true,
	"updateOne":       true,
	"updateMany":      true,
	"deleteOne":       true,
	"deleteMany":      true,
	"aggregate":       true,
	"countDocuments":  true,
	"listDatabases":   false,
	"listCollections": false,
}

// MongoCommand 是 mongo 富命令串的结构形式。
//
// 格式：`<op> [collection] [--db=<database>] [--query=<json>]`
//
// 之所以让 collection 走 positional、database 走 flag：绝大多数调用只需要 collection
// （库用资产默认库），把最常用的字段放 positional 让命令短且可读。
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

	needsCollection, ok := mongoOps[parsed.Verb]
	if !ok {
		return nil, fmt.Errorf("unsupported mongo operation %q; supported: %s",
			parsed.Verb, strings.Join(slices.Sorted(maps.Keys(mongoOps)), ", "))
	}

	c := &MongoCommand{Op: parsed.Verb}
	if len(parsed.Args) > 1 {
		return nil, fmt.Errorf("mongo commands take at most one positional argument (the collection); got %d", len(parsed.Args))
	}
	if len(parsed.Args) == 1 {
		c.Collection = parsed.Args[0]
	}
	if needsCollection && c.Collection == "" {
		return nil, fmt.Errorf("operation %q requires a collection: %s <collection> [--query=...]", c.Op, c.Op)
	}

	for name, value := range parsed.Flags {
		switch name {
		case "db":
			c.Database = value
		case "query":
			c.Query = value
		default:
			return nil, fmt.Errorf("unknown flag --%s; mongo supports --db and --query", name)
		}
	}
	return c, nil
}

// Render 是 ParseMongoCommand 的逆函数（TestMongoCommand_RoundTrip 锁住）。
func (c *MongoCommand) Render() string {
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
	return cmd.Render()
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
