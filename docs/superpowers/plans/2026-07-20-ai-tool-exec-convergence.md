# AI 工具面收敛 · Plan B：mongo / etcd / kafka 接入 exec 并删除旧工具 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 mongodb / etcd / kafka 三类资产接入 Plan A 的统一 `exec(asset, command)`，然后删除 14 个按类型区分的旧工具，让 AI 工具面从 29 个收敛到 15 个。

**Architecture:** 复用 Plan A 已建好的 `CanonicalizeFunc` 缝——模型写**富命令串**（`topic create orders --partitions=3`），执行器解析它调用 typed service；canonicalizer 把它映射回**现有的**策略串（`topic.create orders`）。因此 `MatchKafkaRule` / `splitKafkaRule` / 7 个内置权限组 / 存量 grant / 前端 i18n 占位符**全部不动**。三类型各写一个 `<name>_command.go`（parse + render + round-trip 属性测试）、一个 `Exec<X>OnAsset`、一份 `SKILL.md`，在 `execimpl/register.go` 注册。

**Tech Stack:** Go 1.26、`mvdan.cc/sh/v3/syntax`（已在 go.mod:54，`parseK8sCommandArgs` 已在用）、gomock（`go.uber.org/mock/gomock`）、plain `testing`（**不用 goconvey**——统一 exec 缝上的所有文件都是 plain）。

## Global Constraints

- 模块路径：`github.com/opskat/opskat`。
- **策略串格式一个字节都不许改。** kafka 的 `<action> <resource>` 双 token、mongo 的裸 operation token、etcd 的 `op [key] [value]`，都是 canonicalizer 的**输出**，不是模型的输入。改它等于让存量 grant 与内置组静默失配——`MatchKafkaRule` 在 `splitKafkaRule` 失败时返回 `false` 且不记日志（`internal/ai/policy/kafka_policy.go:50-56`），deny 规则失配 = fail-open。
- **模型面文本一律英文，不走 i18n**：SKILL.md、解析错误、unsupported 文案全英文，与 Plan A 一致。前端 UI 文案仍需 en + zh-CN 双份且各语言地道表达。
- 后端验证用 `golangci-lint run`，**不用 `go vet`**（见 `docs/DEVELOP.md`）。
- 测试命令统一 `go test ./internal/... ./cmd/...`。
- 提交信息用 gitmoji；**仅在刻意关联 issue 时** subject 才带 `#123`。
- 仓内 mock 用法：`asset_repo.RegisterAsset(mock)` + `t.Cleanup` 还原，范本 `internal/ai/tool/tool_handlers_unified_test.go:48-60`（`setupUnified`）。
- 包外测试注册假类型后必须 `t.Cleanup(func() { permission.UnregisterExecutorForTest(fakeType) })`——`RegisterExecutor` 重复注册会 panic，`-count>1` 会撞上。
- 断言用 `t.Fatalf("got %q, want %q", got, want)`，不引断言库。
- **repo 内无属性测试框架**（无 `testing/quick` / `rapid` / `gopter`，已核实）。本 Plan 的"属性测试"= plain `testing` 里手写生成输入的循环，不新增依赖。

---

### Task 1: 共享命令行 tokenizer（`internal/ai/cmdline`）

三类型的富命令串都是 `<verb> [positional...] [--flag] [--flag=value]` 形态，值里可能含空格与 JSON（`--value='{"a": 1}'`）。`strings.Fields` 不够，必须引号感知。

仓内**已有**一份引号感知解析：`parseK8sCommandArgs`（`internal/ai/helper/k8s_helper.go:89-145`）+ `shellWordLiteral`（`:149`）+ `appendShellWordPart`（`:159`），基于 `mvdan.cc/sh/v3/syntax`。本任务把这段词法层抽到共享包，并让 k8s 调用它——**这是对生产者与其缝的重构，属于 in-scope**（AGENTS.md「Fix root causes — refactor over patch」），不是 drive-by：不抽的话本 Plan 会写出第二份引号解析，正是 AGENTS.md「Reuse first」要拦的漂移。

**Files:**
- Create: `internal/ai/cmdline/cmdline.go`
- Test: `internal/ai/cmdline/cmdline_test.go`
- Modify: `internal/ai/helper/k8s_helper.go:89-180`（改为调用 `cmdline.Words`，删除本地 `shellWordLiteral` / `appendShellWordPart`）

**Interfaces:**
- Consumes: `mvdan.cc/sh/v3/syntax`
- Produces:
  - `func Words(s string) ([]string, error)` — 引号感知切词，拒绝管道/重定向/变量赋值
  - `type Command struct { Verb string; Args []string; Flags map[string]string }`
  - `func Parse(s string) (*Command, error)`
  - `func (c *Command) Render() string`
  - `func QuoteIfNeeded(s string) string`

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/cmdline/cmdline_test.go`：

```go
package cmdline

import (
	"reflect"
	"testing"
)

func TestWords_QuoteAware(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`topic create orders`, []string{"topic", "create", "orders"}},
		{`message produce t --value='{"a": 1}'`, []string{"message", "produce", "t", `--value={"a": 1}`}},
		{`message produce t --value="hello world"`, []string{"message", "produce", "t", "--value=hello world"}},
		{`get /app/config --prefix`, []string{"get", "/app/config", "--prefix"}},
	}
	for _, c := range cases {
		got, err := Words(c.in)
		if err != nil {
			t.Fatalf("Words(%q) unexpected error: %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("Words(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestWords_RejectsShellControl(t *testing.T) {
	cases := []string{
		`topic list | grep x`,
		`topic list > /tmp/out`,
		`FOO=bar topic list`,
		`topic list; rm -rf /`,
	}
	for _, in := range cases {
		if _, err := Words(in); err == nil {
			t.Fatalf("Words(%q) = nil error, want rejection", in)
		}
	}
}

func TestParse_VerbArgsFlags(t *testing.T) {
	got, err := Parse(`topic create orders --partitions=3 --dry-run`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Verb != "topic" {
		t.Fatalf("Verb = %q, want %q", got.Verb, "topic")
	}
	if !reflect.DeepEqual(got.Args, []string{"create", "orders"}) {
		t.Fatalf("Args = %#v, want %#v", got.Args, []string{"create", "orders"})
	}
	want := map[string]string{"partitions": "3", "dry-run": "true"}
	if !reflect.DeepEqual(got.Flags, want) {
		t.Fatalf("Flags = %#v, want %#v", got.Flags, want)
	}
}

// TestParseRender_RoundTrip 是本 Plan 的核心属性：Parse(Render(c)) == c。
// 生成输入而非固定表——手写表只会覆盖作者想到的情形，而引号/空格/JSON 恰恰是想不到的地方。
func TestParseRender_RoundTrip(t *testing.T) {
	verbs := []string{"topic", "message", "get", "find"}
	argSets := [][]string{
		nil,
		{"create"},
		{"create", "orders"},
		{"produce", "orders-2024"},
	}
	flagSets := []map[string]string{
		nil,
		{"prefix": "true"},
		{"partitions": "3", "replication-factor": "2"},
		{"value": `{"a": 1, "b": "x y"}`},
		{"value": "hello world", "key": "k1"},
		{"filter": `{"name": "o'brien"}`},
	}

	for _, verb := range verbs {
		for _, args := range argSets {
			for _, flags := range flagSets {
				original := &Command{Verb: verb, Args: args, Flags: flags}
				rendered := original.Render()
				reparsed, err := Parse(rendered)
				if err != nil {
					t.Fatalf("Parse(Render(%#v)) = error %v (rendered: %s)", original, err, rendered)
				}
				if reparsed.Verb != verb {
					t.Fatalf("round-trip Verb = %q, want %q (rendered: %s)", reparsed.Verb, verb, rendered)
				}
				if len(reparsed.Args) != len(args) {
					t.Fatalf("round-trip Args = %#v, want %#v (rendered: %s)", reparsed.Args, args, rendered)
				}
				for i := range args {
					if reparsed.Args[i] != args[i] {
						t.Fatalf("round-trip Args[%d] = %q, want %q (rendered: %s)", i, reparsed.Args[i], args[i], rendered)
					}
				}
				if len(reparsed.Flags) != len(flags) {
					t.Fatalf("round-trip Flags = %#v, want %#v (rendered: %s)", reparsed.Flags, flags, rendered)
				}
				for k, v := range flags {
					if reparsed.Flags[k] != v {
						t.Fatalf("round-trip Flags[%q] = %q, want %q (rendered: %s)", k, reparsed.Flags[k], v, rendered)
					}
				}
			}
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/cmdline/ -v`
Expected: FAIL — `no required module provides package .../internal/ai/cmdline`（包还不存在）。

- [ ] **Step 3: 实现 cmdline.go**

创建 `internal/ai/cmdline/cmdline.go`：

```go
// Package cmdline 提供引号感知的命令行切词与 flag 解析，供统一 exec 下各资产类型的
// 命令 DSL 共用（mongo / etcd / kafka），以及 k8s 的 kubectl 参数解析。
//
// 只做词法层，不认识任何具体协议的动词：谁是 verb、哪些 flag 合法，由各类型的
// <name>_command.go 自己判断。
package cmdline

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Words 把命令串切成词，保留引号内的空格，并剥掉引号本身。
//
// 拒绝一切 shell 控制结构（管道、重定向、变量赋值、多语句）：这些串最终会被送去
// 执行 typed service 调用，不经过 shell，语义上没有"管道"这回事；容忍它们只会让
// 模型写出看似成功、实则参数被吃掉的命令。
func Words(s string) ([]string, error) {
	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(s), "")
	if err != nil {
		return nil, fmt.Errorf("invalid command: %w", err)
	}
	if len(file.Stmts) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	if len(file.Stmts) != 1 {
		return nil, fmt.Errorf("only a single command is supported")
	}

	stmt := file.Stmts[0]
	if len(stmt.Redirs) > 0 {
		return nil, fmt.Errorf("shell redirection is not supported")
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return nil, fmt.Errorf("only a simple command is supported")
	}
	if len(call.Assigns) > 0 {
		return nil, fmt.Errorf("shell variable assignments are not supported")
	}

	words := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		lit, err := wordLiteral(word)
		if err != nil {
			return nil, err
		}
		if lit != "" {
			words = append(words, lit)
		}
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return words, nil
}

func wordLiteral(word *syntax.Word) (string, error) {
	var b strings.Builder
	for _, part := range word.Parts {
		if err := appendWordPart(&b, part); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func appendWordPart(b *strings.Builder, part syntax.WordPart) error {
	switch x := part.(type) {
	case *syntax.Lit:
		b.WriteString(x.Value)
		return nil
	case *syntax.SglQuoted:
		b.WriteString(x.Value)
		return nil
	case *syntax.DblQuoted:
		for _, inner := range x.Parts {
			if err := appendWordPart(b, inner); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported shell construct in command")
	}
}

// Command 是切词后的结构：第一个词是 Verb，其余非 flag 词按序进 Args，
// `--k=v` 与裸 `--k`（值为 "true"）进 Flags。
type Command struct {
	Verb  string
	Args  []string
	Flags map[string]string
}

// Parse 解析富命令串。
func Parse(s string) (*Command, error) {
	words, err := Words(s)
	if err != nil {
		return nil, err
	}

	c := &Command{Verb: words[0], Flags: map[string]string{}}
	for _, w := range words[1:] {
		if !strings.HasPrefix(w, "--") {
			c.Args = append(c.Args, w)
			continue
		}
		name, value, found := strings.Cut(strings.TrimPrefix(w, "--"), "=")
		if name == "" {
			return nil, fmt.Errorf("malformed flag: %s", w)
		}
		if !found {
			value = "true"
		}
		if _, dup := c.Flags[name]; dup {
			return nil, fmt.Errorf("duplicate flag: --%s", name)
		}
		c.Flags[name] = value
	}
	return c, nil
}

// Render 把 Command 还原为命令串。Flags 按名称排序输出，保证同一个 Command
// 渲染结果稳定——Go map 迭代顺序随机，不排序的话 Render 不是函数。
func (c *Command) Render() string {
	parts := make([]string, 0, 1+len(c.Args)+len(c.Flags))
	parts = append(parts, c.Verb)
	for _, a := range c.Args {
		parts = append(parts, QuoteIfNeeded(a))
	}
	for _, name := range slices.Sorted(maps.Keys(c.Flags)) {
		if c.Flags[name] == "true" {
			parts = append(parts, "--"+name)
			continue
		}
		parts = append(parts, "--"+name+"="+QuoteIfNeeded(c.Flags[name]))
	}
	return strings.Join(parts, " ")
}

// QuoteIfNeeded 在值含空格或引号时加单引号；值本身含单引号时退化为双引号包裹。
// 与 Words 的剥引号逻辑互逆——两者必须一起改。
func QuoteIfNeeded(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`") {
		return s
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}
```

完整 import 块：

```go
import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)
```

`Render` 里按名称排序输出 flag 是必需的，不是美观问题：Go 的 map 迭代顺序随机，不排序的话 `Render` 就不是函数，`TestParseRender_RoundTrip` 会随机失败。已实测 `slices.Sorted(maps.Keys(...))` 在 go1.26.3 下对本 Plan 用到的两种 map 形状都成立。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/ai/cmdline/ -v`
Expected: PASS，4 个测试全绿。若 `TestParseRender_RoundTrip` 失败，报错里会带 `rendered:` 串——那就是 `QuoteIfNeeded` 与 `Words` 不互逆的具体输入。

- [ ] **Step 5: 让 k8s 复用它**

修改 `internal/ai/helper/k8s_helper.go`：把 `parseK8sCommandArgs` 的词法段替换为 `cmdline.Words`，删除本地的 `shellWordLiteral` 与 `appendShellWordPart`（`:149-180`）。替换后的函数体：

```go
func parseK8sCommandArgs(command string) ([]string, error) {
	args, err := cmdline.Words(command)
	if err != nil {
		return nil, fmt.Errorf("invalid kubectl command: %w", err)
	}

	if isKubectlProgram(args[0]) {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("missing kubectl subcommand")
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--kubeconfig":
			return nil, fmt.Errorf("do not pass --kubeconfig to exec; the asset kubeconfig is used automatically")
		case strings.HasPrefix(arg, "--kubeconfig="):
			return nil, fmt.Errorf("do not pass --kubeconfig to exec; the asset kubeconfig is used automatically")
		}
	}

	return args, nil
}
```

在 import 块加 `"github.com/opskat/opskat/internal/ai/cmdline"`；若 `syntax` 已无其他使用者则删除该 import。

注意错误文案从 `exec_k8s` 改成了 `exec`——旧工具在 Task 10 会被删掉，提前对齐；这是模型面文本，英文。

- [ ] **Step 6: 运行 k8s 测试确认无回归**

Run: `go test ./internal/ai/... -run 'K8s|k8s' -v`
Expected: PASS。特别关注 `BuildK8sCommandPlan` 相关测试——若有测试断言了旧的 `exec_k8s` 错误文案，改断言为 `exec`（这是本任务引入的有意变更）。

- [ ] **Step 7: 全量测试 + lint**

Run: `go test ./internal/... && golangci-lint run`
Expected: 0 FAIL，0 issues。

- [ ] **Step 8: 提交**

```bash
git add internal/ai/cmdline/ internal/ai/helper/k8s_helper.go
git commit -m "✨ 抽出引号感知命令行 tokenizer，k8s 改为复用"
```

---

### Task 2: etcd Format/Parse 真互逆 + round-trip 属性测试

`FormatEtcdCommand`（`internal/ai/helper/etcd_helper.go:72-91`）与 `etcd_svc.ParseCommand`（`internal/service/etcd_svc/command.go:35-123`）**当前不是互逆的**：

- Format **丢** `Limit` / `Revision` / `LeaseID` / `Args["ttl"]`
- Parse **认得** Format 从不输出的 `--limit=` / `--revision=` / `--lease=`(hex)
- Parse 只为 `get`/`del`/`put` 赋 `Key`/`Value`；`endpoint_status` 的 positional 被丢弃

本任务补齐两端，用属性测试锁死。**策略匹配不受影响**：`MatchRedisRule`（`internal/ai/policy/redis_policy.go:54-94`）在 `ruleArgs == "*"` 时于 `:80` 直接 return true，不会走到 `path.Match`；内置组 `get *` / `endpoint *` / `member list` / `lease list` 因此对 `get /app/config --limit=10` 仍然匹配（已实测确认）。

**Files:**
- Modify: `internal/ai/helper/etcd_helper.go:72-91`（`FormatEtcdCommand`）
- Modify: `internal/service/etcd_svc/command.go:35-123`（`ParseCommand`）
- Test: `internal/service/etcd_svc/command_roundtrip_test.go`（新建）

**Interfaces:**
- Consumes: `etcd_svc.ExecRequest`（`internal/service/etcd_svc/command.go:11-23`）
- Produces: `FormatEtcdCommand` 与 `ParseCommand` 满足 `ParseCommand(FormatEtcdCommand(req))` 等于 `req`（忽略 `AssetID`/`ApprovalID`/`Source` 三个非命令字段）

- [ ] **Step 1: 写失败测试**

创建 `internal/service/etcd_svc/command_roundtrip_test.go`：

```go
package etcd_svc

import "testing"

// formatForTest 复制 helper.FormatEtcdCommand 的调用形态。放在本包是为了避免
// etcd_svc -> helper 的反向导入；helper 侧有一个断言两者一致的测试（见 Task 2 Step 5）。
func formatForTest(req *ExecRequest) string { return FormatCommand(req) }

func TestParseFormat_RoundTrip(t *testing.T) {
	reqs := []*ExecRequest{
		{Op: "get", Key: "/app/config"},
		{Op: "get", Key: "/app/", Prefix: true},
		{Op: "get", Key: "/app/config", Limit: 10},
		{Op: "get", Key: "/app/config", Revision: 42},
		{Op: "get", Key: "/app/", Prefix: true, Limit: 5, Revision: 7},
		{Op: "put", Key: "/app/config", Value: "hello"},
		{Op: "put", Key: "/app/config", Value: "hello world"},
		{Op: "put", Key: "/app/config", Value: `{"a": 1}`},
		{Op: "put", Key: "/app/config", Value: "v", LeaseID: 0x694d5c0f},
		{Op: "del", Key: "/app/", Prefix: true},
		{Op: "lease_grant", Args: map[string]any{"ttl": int64(3600)}},
		{Op: "lease_revoke", LeaseID: 0x694d5c0f},
		{Op: "lease_list"},
		{Op: "member_list"},
		{Op: "endpoint_status"},
		{Op: "endpoint_health"},
	}

	for _, want := range reqs {
		rendered := formatForTest(want)
		got, err := ParseCommand(rendered)
		if err != nil {
			t.Fatalf("ParseCommand(%q) unexpected error: %v", rendered, err)
		}
		if got.Op != want.Op {
			t.Fatalf("[%s] Op = %q, want %q", rendered, got.Op, want.Op)
		}
		if got.Key != want.Key {
			t.Fatalf("[%s] Key = %q, want %q", rendered, got.Key, want.Key)
		}
		if got.Value != want.Value {
			t.Fatalf("[%s] Value = %q, want %q", rendered, got.Value, want.Value)
		}
		if got.Prefix != want.Prefix {
			t.Fatalf("[%s] Prefix = %v, want %v", rendered, got.Prefix, want.Prefix)
		}
		if got.Limit != want.Limit {
			t.Fatalf("[%s] Limit = %d, want %d", rendered, got.Limit, want.Limit)
		}
		if got.Revision != want.Revision {
			t.Fatalf("[%s] Revision = %d, want %d", rendered, got.Revision, want.Revision)
		}
		if got.LeaseID != want.LeaseID {
			t.Fatalf("[%s] LeaseID = %d, want %d", rendered, got.LeaseID, want.LeaseID)
		}
		wantTTL, hasTTL := ttlOf(want)
		gotTTL, gotHasTTL := ttlOf(got)
		if hasTTL != gotHasTTL || wantTTL != gotTTL {
			t.Fatalf("[%s] ttl = (%d,%v), want (%d,%v)", rendered, gotTTL, gotHasTTL, wantTTL, hasTTL)
		}
	}
}

func ttlOf(r *ExecRequest) (int64, bool) {
	if r.Args == nil {
		return 0, false
	}
	v, ok := r.Args["ttl"]
	if !ok {
		return 0, false
	}
	n, ok := v.(int64)
	return n, ok
}

// TestParseCommand_RejectsUnknownFlag 锁住"未知 flag 必须报错"——静默忽略会让
// 模型以为参数生效了。
func TestParseCommand_RejectsUnknownFlag(t *testing.T) {
	if _, err := ParseCommand("get /app --nonsense=1"); err == nil {
		t.Fatal("ParseCommand with unknown flag = nil error, want rejection")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/service/etcd_svc/ -run RoundTrip -v`
Expected: FAIL — `undefined: FormatCommand`。

- [ ] **Step 3: 把 FormatCommand 搬进 etcd_svc 并补齐字段**

在 `internal/service/etcd_svc/command.go` 末尾新增：

```go
// FormatCommand 把 ExecRequest 还原为命令串，是 ParseCommand 的逆函数
// （TestParseFormat_RoundTrip 锁住这条性质）。
//
// 三个消费者共用同一份格式：策略匹配、审计文本、SaveGrantPattern。
// 放在 etcd_svc 而不是 helper，是为了跟 ParseCommand 同文件——互逆的两个函数分居
// 两个包正是它们此前漂移（Format 丢 limit/revision/lease/ttl、Parse 认得 Format
// 不输出的 flag）的原因。
func FormatCommand(req *ExecRequest) string {
	op := strings.ReplaceAll(req.Op, "_", " ")
	parts := []string{op}
	if req.Key != "" {
		parts = append(parts, cmdline.QuoteIfNeeded(req.Key))
	}
	if req.Value != "" {
		parts = append(parts, cmdline.QuoteIfNeeded(req.Value))
	}
	if req.Prefix {
		parts = append(parts, "--prefix")
	}
	if req.Limit != 0 {
		parts = append(parts, "--limit="+strconv.FormatInt(req.Limit, 10))
	}
	if req.Revision != 0 {
		parts = append(parts, "--revision="+strconv.FormatInt(req.Revision, 10))
	}
	if req.LeaseID != 0 {
		parts = append(parts, "--lease="+strconv.FormatInt(req.LeaseID, 16))
	}
	if req.Args != nil {
		if ttl, ok := req.Args["ttl"].(int64); ok && ttl != 0 {
			parts = append(parts, "--ttl="+strconv.FormatInt(ttl, 10))
		}
	}
	return strings.Join(parts, " ")
}
```

import 块加 `"github.com/opskat/opskat/internal/ai/cmdline"`（`strings` / `strconv` 已在）。

- [ ] **Step 4: 补齐 ParseCommand 的缺口**

修改 `internal/service/etcd_svc/command.go` 的 `ParseCommand`：

第一，切词改用 `cmdline.Words`（引号感知，`put /k 'hello world'` 才能正确还原 Value）。把

```go
	tokens := strings.Fields(s)
	if len(tokens) == 0 {
		return nil, errors.New("empty command")
	}
```

替换为

```go
	tokens, err := cmdline.Words(s)
	if err != nil {
		return nil, err
	}
```

第二，flag switch 里新增 `ttl` 分支。在 `case "lease":` 之后、`default:` 之前插入：

```go
		case "ttl":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid --ttl: %s", val)
			}
			if req.Args == nil {
				req.Args = map[string]any{}
			}
			req.Args["ttl"] = n
```

第三，positional 赋值补上 `endpoint_status` / `endpoint_health` 与 `lease_revoke`。把

```go
	switch op {
	case "get", "del":
		if len(positional) >= 1 {
			req.Key = positional[0]
		}
	case "put":
		if len(positional) < 2 {
			return nil, errors.New("put requires key and value")
		}
		req.Key = positional[0]
		req.Value = strings.Join(positional[1:], " ")
	}
```

替换为

```go
	switch op {
	case "get", "del", "endpoint_status", "endpoint_health":
		if len(positional) >= 1 {
			req.Key = positional[0]
		}
	case "put":
		if len(positional) < 2 {
			return nil, errors.New("put requires key and value")
		}
		req.Key = positional[0]
		// 保留 Join：`put /msg hello world` 仍还原为 "hello world"，
		// 既有的 TestParseCommand_PutMultiWordValue（command_test.go:58）锁着这条契约。
		// round-trip 不受影响——FormatCommand 对含空格的值总是加引号，
		// 引号内的空格经 cmdline.Words 已收进单个 token，Join 一个元素是恒等。
		req.Value = strings.Join(positional[1:], " ")
	}
```

**不要**在这里加"多个 positional 就报错"的校验：它会打破上述既有测试，而 round-trip 并不需要它。

- [ ] **Step 5: 让 helper.FormatEtcdCommand 委托给它**

修改 `internal/ai/helper/etcd_helper.go:72-91`，把整个函数体替换为委托，保留导出名（`internal/ai/audit/extractor_default.go` 与既有调用方仍在用）：

```go
// FormatEtcdCommand 委托给 etcd_svc.FormatCommand——格式定义与其逆函数 ParseCommand
// 同住一处，避免二者再次漂移。保留本名是因为 helper 侧已有调用方。
func FormatEtcdCommand(req *etcd_svc.ExecRequest) string {
	return etcd_svc.FormatCommand(req)
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/service/etcd_svc/ ./internal/ai/... -v -run 'Etcd|etcd|RoundTrip'`
Expected: PASS。

`internal/ai/audit/extractor_default_test.go:49` 断言 `exec_etcd` 的审计提取；该提取器是 `extractor_default.go:21-36` 里**手抄的第二份 format**（为避开导入环）。本任务不动它——它在 Task 10 随 `exec_etcd` 一并删除。若该测试因新增 flag 而失败，说明它断言的输入含 limit/revision/lease/ttl；**不要**改手抄副本，改测试输入为不含这些字段的用例，并在测试里加一行注释指向 Task 10。

- [ ] **Step 7: 全量测试 + lint，提交**

```bash
go test ./internal/... && golangci-lint run
git add internal/service/etcd_svc/ internal/ai/helper/etcd_helper.go
git commit -m "🐛 etcd Format/Parse 补齐为真互逆并加 round-trip 属性测试"
```

---

### Task 3: etcd 接入 exec

**Files:**
- Create: `internal/ai/skills/etcd/SKILL.md`
- Create: `internal/ai/helper/etcd_exec.go`
- Modify: `internal/ai/execimpl/register.go`
- Test: `internal/ai/helper/etcd_exec_test.go`

**Interfaces:**
- Consumes: `etcd_svc.ParseCommand`、`etcd_svc.FormatCommand`（Task 2）、`permission.RegisterExecutor`
- Produces:
  - `func ExecEtcdOnAsset(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error)`
  - `func CanonicalizeEtcdCommand(asset *asset_entity.Asset, command string) (string, error)`

- [ ] **Step 1: 写 SKILL.md**

创建 `internal/ai/skills/etcd/SKILL.md`（目录名必须等于 `asset_entity.AssetTypeEtcd` 的值，`skills.Get` 直接按目录名取）：

```markdown
---
name: etcd
description: "Read and write etcd keys via exec, using an etcdctl-like command syntax."
---

# etcd assets

## Command syntax

`<op> [key] [value] [--flags]` — a subset of etcdctl.

- `get /app/config`
- `get /app/ --prefix`
- `get /app/config --limit=10 --revision=42`
- `put /app/config 'hello world'`
- `put /app/config '{"debug": true}' --lease=694d5c0f`
- `del /app/ --prefix`
- `lease grant --ttl=3600`
- `lease revoke --lease=694d5c0f`
- `lease list`
- `member list`
- `endpoint status`
- `endpoint health`

## Notes

- Quote any value containing spaces or JSON: `put /k '{"a": 1}'`. A `put` takes
  exactly one value — an unquoted multi-word value is an error, not a silent join.
- `--lease` is hexadecimal, matching etcdctl.
- Two-word ops (`lease grant`, `member list`, `endpoint status`) are written with
  a space, exactly as shown.
- Unknown flags are rejected rather than ignored.
- The `scope` parameter is not used by etcd assets.
```

- [ ] **Step 2: 写失败测试**

创建 `internal/ai/helper/etcd_exec_test.go`：

```go
package helper

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestCanonicalizeEtcdCommand_NormalizesToPolicyForm 锁住 canonicalizer 的契约：
// 输出必须是策略层认得的形式，且对内置组 "get *" 仍然匹配。
func TestCanonicalizeEtcdCommand_NormalizesToPolicyForm(t *testing.T) {
	cases := []struct{ in, want string }{
		{"get /app/config", "get /app/config"},
		{"GET /app/config", "get /app/config"},
		{"get /app/ --prefix", "get /app/ --prefix"},
		{"member list", "member list"},
		{"lease grant --ttl=3600", "lease grant --ttl=3600"},
		{"put /app/k 'hello world'", "put /app/k 'hello world'"},
	}
	for _, c := range cases {
		got, err := CanonicalizeEtcdCommand(&asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeEtcd}, c.in)
		if err != nil {
			t.Fatalf("CanonicalizeEtcdCommand(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("CanonicalizeEtcdCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalizeEtcdCommand_RejectsUnsupportedOp(t *testing.T) {
	_, err := CanonicalizeEtcdCommand(&asset_entity.Asset{ID: 1}, "nonsense /x")
	if err == nil {
		t.Fatal("CanonicalizeEtcdCommand with unsupported op = nil error, want rejection")
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/ai/helper/ -run Etcd -v`
Expected: FAIL — `undefined: CanonicalizeEtcdCommand`。

- [ ] **Step 4: 实现 etcd_exec.go**

创建 `internal/ai/helper/etcd_exec.go`：

```go
package helper

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/etcd_svc"
)

// ExecEtcdOnAsset 是不含权限检查的纯执行入口，供统一 exec 使用。
// HandleExecEtcd 在 Task 10 随 exec_etcd 工具一并删除；在那之前两条路径并存。
// scope 对 etcd 无意义，忽略。
func ExecEtcdOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	req, err := etcd_svc.ParseCommand(command)
	if err != nil {
		return "", err
	}
	req.AssetID = asset.ID

	svc := etcd_svc.New(getSSHPool(ctx))
	result, err := svc.Exec(ctx, req)
	if err != nil {
		return "", err
	}
	return marshalEtcdResult(result)
}

// CanonicalizeEtcdCommand 把模型给的命令规范化为策略匹配 / 审批展示 / 审计使用的形式：
// 走一遍 ParseCommand + FormatCommand，归一大小写、复合 op 的写法与 flag 顺序。
//
// 排在权限检查之前（见 internal/ai/tool/tool_handlers_unified.go 的 handleExec 顺序注释）：
// 语法错误的命令必然执行失败，必须在弹审批对话框之前就失败，否则用户先被打断、
// 批准之后命令才因为解析错误而没跑。
func CanonicalizeEtcdCommand(_ *asset_entity.Asset, command string) (string, error) {
	req, err := etcd_svc.ParseCommand(command)
	if err != nil {
		return "", err
	}
	return etcd_svc.FormatCommand(req), nil
}
```

`marshalEtcdResult` 复用 `HandleExecEtcd`（`internal/ai/helper/etcd_helper.go:22-70`）里既有的结果序列化逻辑——实施时读该函数末尾，把序列化那几行抽成 `marshalEtcdResult(result)` 并让两处都调它（**不要**复制一份）。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/ai/helper/ -run Etcd -v`
Expected: PASS。

- [ ] **Step 6: 注册执行器**

修改 `internal/ai/execimpl/register.go`，在 `init()` 末尾（k8s 之后）加：

```go
	etcdHelp, _ := skills.Get(asset_entity.AssetTypeEtcd)
	permission.RegisterExecutor(asset_entity.AssetTypeEtcd,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecEtcdOnAsset(ctx, asset, command, scope)
		}, etcdHelp, helper.CanonicalizeEtcdCommand)
```

- [ ] **Step 7: 缩短豁免清单**

修改 `internal/ai/execimpl/coverage_test.go`：删除 `"etcd": "Plan B",` 一行，并把 `maxExemptions` 从 `4` 改为 `3`。

- [ ] **Step 8: 运行测试确认通过，lint，提交**

```bash
go test ./internal/... && golangci-lint run
git add internal/ai/skills/etcd/ internal/ai/helper/etcd_exec.go internal/ai/execimpl/
git commit -m "✨ etcd 接入统一 exec"
```

---

### Task 4: mongo 命令 DSL（parse + render + round-trip）

mongo 今天**没有命令串格式**：`HandleExecMongo`（`internal/ai/helper/mongodb_helper.go:49-66`）把裸的 `operation` token 送去策略匹配，`database` / `collection` / `query` 三个字段全丢。后果是审批弹窗显示 `deleteMany`，用户看不到删的是哪个集合、什么条件。

本任务发明格式。**canonical 仍是裸 operation token**——`BuiltinMongoReadOnly` 用 `AllowTypes: find/findOne/aggregate/countDocuments`，匹配器 `policyValueMatches`（`internal/ai/policy/policy_effective.go:14-16`）是精确大小写不敏感比较，改 canonical 会让内置组与存量 grant 全部失配。

**Files:**
- Create: `internal/ai/helper/mongo_command.go`
- Test: `internal/ai/helper/mongo_command_test.go`

**Interfaces:**
- Consumes: `cmdline.Parse` / `cmdline.Command`（Task 1）
- Produces:
  - `type MongoCommand struct { Op, Database, Collection, Query string }`
  - `func ParseMongoCommand(s string) (*MongoCommand, error)`
  - `func (c *MongoCommand) Render() string`
  - `func (c *MongoCommand) PolicyString() string`

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/helper/mongo_command_test.go`：

```go
package helper

import "testing"

func TestParseMongoCommand(t *testing.T) {
	cases := []struct {
		in   string
		want MongoCommand
	}{
		{`find users`, MongoCommand{Op: "find", Collection: "users"}},
		{`find users --query='{"filter":{"age":{"$gt":21}}}'`,
			MongoCommand{Op: "find", Collection: "users", Query: `{"filter":{"age":{"$gt":21}}}`}},
		{`find users --db=analytics`,
			MongoCommand{Op: "find", Collection: "users", Database: "analytics"}},
		{`listDatabases`, MongoCommand{Op: "listDatabases"}},
		{`deleteMany logs --query='{"filter":{"level":"debug"}}'`,
			MongoCommand{Op: "deleteMany", Collection: "logs", Query: `{"filter":{"level":"debug"}}`}},
	}
	for _, c := range cases {
		got, err := ParseMongoCommand(c.in)
		if err != nil {
			t.Fatalf("ParseMongoCommand(%q) unexpected error: %v", c.in, err)
		}
		if *got != c.want {
			t.Fatalf("ParseMongoCommand(%q) = %#v, want %#v", c.in, *got, c.want)
		}
	}
}

func TestParseMongoCommand_RejectsUnknownOp(t *testing.T) {
	if _, err := ParseMongoCommand("dropEverything users"); err == nil {
		t.Fatal("ParseMongoCommand with unknown op = nil error, want rejection")
	}
}

func TestParseMongoCommand_RequiresCollectionWhereApplicable(t *testing.T) {
	if _, err := ParseMongoCommand("find"); err == nil {
		t.Fatal("ParseMongoCommand(\"find\") = nil error, want rejection (collection required)")
	}
}

func TestMongoCommand_RoundTrip(t *testing.T) {
	cmds := []MongoCommand{
		{Op: "find", Collection: "users"},
		{Op: "find", Collection: "users", Database: "analytics"},
		{Op: "find", Collection: "users", Query: `{"filter":{"a":1}}`},
		{Op: "aggregate", Collection: "events", Query: `{"pipeline":[{"$match":{"x":"a b"}}]}`},
		{Op: "insertOne", Collection: "users", Query: `{"document":{"name":"o'brien"}}`},
		{Op: "listDatabases"},
		{Op: "listCollections", Database: "analytics"},
	}
	for _, want := range cmds {
		rendered := want.Render()
		got, err := ParseMongoCommand(rendered)
		if err != nil {
			t.Fatalf("ParseMongoCommand(Render(%#v)) = error %v (rendered: %s)", want, err, rendered)
		}
		if *got != want {
			t.Fatalf("round-trip = %#v, want %#v (rendered: %s)", *got, want, rendered)
		}
	}
}

// TestMongoCommand_PolicyStringIsBareOp 锁住最要紧的兼容性约束：策略串必须仍是
// 裸 operation token。改它 = BuiltinMongoReadOnly 的 AllowTypes 与全部存量 grant 静默失配。
func TestMongoCommand_PolicyStringIsBareOp(t *testing.T) {
	c := MongoCommand{Op: "find", Collection: "users", Database: "analytics", Query: `{"filter":{"a":1}}`}
	if got := c.PolicyString(); got != "find" {
		t.Fatalf("PolicyString() = %q, want %q — changing this breaks BuiltinMongoReadOnly and stored grants", got, "find")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/helper/ -run Mongo -v`
Expected: FAIL — `undefined: ParseMongoCommand`。

- [ ] **Step 3: 实现 mongo_command.go**

创建 `internal/ai/helper/mongo_command.go`：

```go
package helper

import (
	"fmt"
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
```

import 块：

```go
import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
)
```

用 `slices.Sorted(maps.Keys(m))` 而不是自己写一个 `sortedKeys` 辅助函数：Task 6 的 kafka DSL 也在同一个 `helper` 包里需要同样的东西，各写一份就是 AGENTS.md「Reuse first」要拦的平行副本。stdlib 已经提供，两处都直接用它。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/ai/helper/ -run Mongo -v`
Expected: PASS，4 个测试全绿。

- [ ] **Step 5: lint + 提交**

```bash
go test ./internal/... && golangci-lint run
git add internal/ai/helper/mongo_command.go internal/ai/helper/mongo_command_test.go
git commit -m "✨ 新增 mongo 命令 DSL 与 round-trip 属性测试"
```

---

### Task 5: mongo 接入 exec

**Files:**
- Create: `internal/ai/skills/mongodb/SKILL.md`
- Create: `internal/ai/helper/mongo_exec.go`
- Modify: `internal/ai/execimpl/register.go`
- Modify: `internal/ai/execimpl/coverage_test.go`
- Test: `internal/ai/helper/mongo_exec_test.go`

**Interfaces:**
- Consumes: `ParseMongoCommand`（Task 4）、`ExecuteMongoDB`（`internal/ai/helper/mongodb_helper.go:132`）、`getOrDialMongoDB`（`:100`）
- Produces:
  - `func ExecMongoOnAsset(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error)`
  - `func CanonicalizeMongoCommand(asset *asset_entity.Asset, command string) (string, error)`

- [ ] **Step 1: 写 SKILL.md**

创建 `internal/ai/skills/mongodb/SKILL.md`（目录名必须等于 `asset_entity.AssetTypeMongoDB` 的值）：

```markdown
---
name: mongodb
description: "Query and modify MongoDB collections via exec, using an operation + collection + JSON query syntax."
---

# MongoDB assets

## Command syntax

`<operation> [collection] [--db=<database>] [--query=<json>]`

- `find users --query='{"filter":{"age":{"$gt":21}},"limit":10}'`
- `findOne users --query='{"filter":{"_id":"abc"}}'`
- `aggregate events --query='{"pipeline":[{"$match":{"type":"click"}}]}'`
- `countDocuments users`
- `insertOne users --query='{"document":{"name":"alice"}}'`
- `updateMany users --query='{"filter":{"active":false},"update":{"$set":{"archived":true}}}'`
- `deleteMany logs --query='{"filter":{"level":"debug"}}'`
- `listDatabases`
- `listCollections --db=analytics`

## Query sub-keys

`--query` is a single JSON object. Which sub-keys apply depends on the operation:

- `find` / `findOne`: `filter`, `sort`, `projection`, `limit`, `skip`
- `insertOne`: `document` · `insertMany`: `documents`
- `updateOne` / `updateMany`: `filter`, `update`
- `deleteOne` / `deleteMany`: `filter`
- `aggregate`: `pipeline`
- `countDocuments`: `filter`

## Notes

- Always single-quote `--query` — it is JSON and contains spaces and braces.
- `--db` overrides the asset's default database; omit it to use the default.
- `listDatabases` and `listCollections` take no collection.
- The `scope` parameter is not used by MongoDB assets; use `--db` instead.
```

- [ ] **Step 2: 写失败测试**

创建 `internal/ai/helper/mongo_exec_test.go`：

```go
package helper

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestCanonicalizeMongoCommand_YieldsBareOp 锁住 canonicalizer 输出 = 策略层认得的裸
// operation token。这是 mongo 接入 exec 而不破坏 BuiltinMongoReadOnly 的全部理由。
func TestCanonicalizeMongoCommand_YieldsBareOp(t *testing.T) {
	cases := []struct{ in, want string }{
		{`find users --query='{"filter":{"a":1}}'`, "find"},
		{`deleteMany logs --query='{"filter":{"level":"debug"}}'`, "deleteMany"},
		{`listDatabases`, "listDatabases"},
		{`aggregate events --db=analytics --query='{"pipeline":[]}'`, "aggregate"},
	}
	for _, c := range cases {
		got, err := CanonicalizeMongoCommand(&asset_entity.Asset{ID: 1}, c.in)
		if err != nil {
			t.Fatalf("CanonicalizeMongoCommand(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("CanonicalizeMongoCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalizeMongoCommand_RejectsBadSyntax(t *testing.T) {
	if _, err := CanonicalizeMongoCommand(&asset_entity.Asset{ID: 1}, "dropEverything users"); err == nil {
		t.Fatal("CanonicalizeMongoCommand with unsupported op = nil error, want rejection")
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/ai/helper/ -run CanonicalizeMongo -v`
Expected: FAIL — `undefined: CanonicalizeMongoCommand`。

- [ ] **Step 4: 实现 mongo_exec.go**

创建 `internal/ai/helper/mongo_exec.go`：

```go
package helper

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ExecMongoOnAsset 是不含权限检查的纯执行入口，供统一 exec 使用。
// HandleExecMongo 在 Task 10 随 exec_mongo 工具一并删除；在那之前两条路径并存。
// scope 对 mongo 忽略——库名走命令里的 --db，不走 scope（见 SKILL.md）。
func ExecMongoOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	c, err := ParseMongoCommand(command)
	if err != nil {
		return "", err
	}

	client, closer, err := getOrDialMongoDB(ctx, asset)
	if err != nil {
		return "", err
	}
	if closer != nil {
		defer func() {
			if err := closer.Close(); err != nil && !IsExpectedCloseErr(err) {
				logger.Default().Warn("close MongoDB connection", zap.Error(err))
			}
		}()
	}

	return ExecuteMongoDB(ctx, client, c.Database, c.Collection, c.Op, c.Query)
}

// CanonicalizeMongoCommand 把富命令串规范化为策略匹配用的裸 operation token。
//
// 排在权限检查之前（handleExec 的顺序，见 internal/ai/tool/tool_handlers_unified.go）：
// 语法错误必然导致执行失败，必须在弹审批对话框之前失败。
func CanonicalizeMongoCommand(_ *asset_entity.Asset, command string) (string, error) {
	c, err := ParseMongoCommand(command)
	if err != nil {
		return "", err
	}
	return c.PolicyString(), nil
}
```

import 块需含 `logger` 与 `zap`（与同包其他文件一致：`"github.com/cago-frame/cago/pkg/logger"`、`"go.uber.org/zap"`）——实施时照抄 `mongodb_helper.go` 的 import 写法。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/ai/helper/ -run Mongo -v`
Expected: PASS。

- [ ] **Step 6: 注册执行器并缩短豁免清单**

修改 `internal/ai/execimpl/register.go`，在 etcd 之后加：

```go
	mongoHelp, _ := skills.Get(asset_entity.AssetTypeMongoDB)
	permission.RegisterExecutor(asset_entity.AssetTypeMongoDB,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecMongoOnAsset(ctx, asset, command, scope)
		}, mongoHelp, helper.CanonicalizeMongoCommand)
```

修改 `internal/ai/execimpl/coverage_test.go`：删除 `"mongodb": "Plan B",`，`maxExemptions` 改为 `2`。

- [ ] **Step 7: 全量测试 + lint，提交**

```bash
go test ./internal/... && golangci-lint run
git add internal/ai/skills/mongodb/ internal/ai/helper/mongo_exec.go internal/ai/helper/mongo_exec_test.go internal/ai/execimpl/
git commit -m "✨ mongodb 接入统一 exec"
```

---

### Task 6: kafka 命令 DSL（parse + render + round-trip）

kafka 是本 Plan 最大的一块：7 个工具、~34 个 service 方法、最宽的 `kafka_message` 有 16 个参数。

**关键约束（读代码前先读这段）：** 策略串必须仍是 `<action> <resource>` **恰好两个 token**。`splitKafkaRule`（`internal/ai/policy/kafka_policy.go:50-56`）硬要求 `len(parts) == 2`，失败返回 `false` 且不记日志。加宽会导致：allowlist 路径 → 永远 `NeedConfirm`（弹窗风暴）；**denylist 路径 → deny 规则失配 = `BuiltinKafkaDangerousDeny` fail-open**；`isWildcardAll` 分支也会挂（`:19-22` 里 `*` 规则要求 command 能切成 2 段）。

所以 canonicalizer **复用现有的 7 个 `Kafka*Command` 函数**（`internal/ai/helper/kafka_command.go`），一个字节都不改它们。

**Files:**
- Create: `internal/ai/helper/kafka_dsl.go`
- Test: `internal/ai/helper/kafka_dsl_test.go`

**Interfaces:**
- Consumes: `cmdline.Parse`（Task 1）、现有 `KafkaClusterCommand` / `KafkaTopicCommand` / `KafkaConsumerGroupCommand` / `KafkaACLCommand` / `KafkaSchemaCommand` / `KafkaConnectCommand` / `KafkaMessageCommand`
- Produces:
  - `type KafkaCommand struct { Family, Verb, Target string; Flags map[string]string }`
  - `func ParseKafkaCommand(s string) (*KafkaCommand, error)`
  - `func (c *KafkaCommand) Render() string`
  - `func (c *KafkaCommand) PolicyString() (string, error)`

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/helper/kafka_dsl_test.go`：

```go
package helper

import (
	"reflect"
	"testing"
)

func TestParseKafkaCommand(t *testing.T) {
	cases := []struct {
		in   string
		want KafkaCommand
	}{
		{`topic list`, KafkaCommand{Family: "topic", Verb: "list", Flags: map[string]string{}}},
		{`topic describe orders`, KafkaCommand{Family: "topic", Verb: "describe", Target: "orders", Flags: map[string]string{}}},
		{`topic create orders --partitions=3 --replication-factor=2`,
			KafkaCommand{Family: "topic", Verb: "create", Target: "orders",
				Flags: map[string]string{"partitions": "3", "replication-factor": "2"}}},
		{`message produce orders --key=k1 --value='{"a": 1}'`,
			KafkaCommand{Family: "message", Verb: "produce", Target: "orders",
				Flags: map[string]string{"key": "k1", "value": `{"a": 1}`}}},
		{`consumer-group reset-offset mygroup --topic=orders --mode=earliest`,
			KafkaCommand{Family: "consumer-group", Verb: "reset-offset", Target: "mygroup",
				Flags: map[string]string{"topic": "orders", "mode": "earliest"}}},
	}
	for _, c := range cases {
		got, err := ParseKafkaCommand(c.in)
		if err != nil {
			t.Fatalf("ParseKafkaCommand(%q) unexpected error: %v", c.in, err)
		}
		if !reflect.DeepEqual(*got, c.want) {
			t.Fatalf("ParseKafkaCommand(%q) = %#v, want %#v", c.in, *got, c.want)
		}
	}
}

func TestParseKafkaCommand_RejectsUnknownFamilyOrVerb(t *testing.T) {
	cases := []string{
		`nonsense list`,
		`topic nonsense orders`,
		`topic`,
	}
	for _, in := range cases {
		if _, err := ParseKafkaCommand(in); err == nil {
			t.Fatalf("ParseKafkaCommand(%q) = nil error, want rejection", in)
		}
	}
}

func TestKafkaCommand_RoundTrip(t *testing.T) {
	cmds := []KafkaCommand{
		{Family: "topic", Verb: "list", Flags: map[string]string{}},
		{Family: "topic", Verb: "create", Target: "orders", Flags: map[string]string{"partitions": "3"}},
		{Family: "message", Verb: "produce", Target: "t", Flags: map[string]string{"value": `{"a": "x y"}`}},
		{Family: "message", Verb: "browse", Target: "t", Flags: map[string]string{"limit": "100", "partition": "0"}},
		{Family: "acl", Verb: "create", Flags: map[string]string{"principal": "User:alice", "resource-type": "topic"}},
		{Family: "schema", Verb: "register", Target: "subj", Flags: map[string]string{"schema": `{"type":"record"}`}},
		{Family: "connect", Verb: "restart", Target: "conn", Flags: map[string]string{}},
	}
	for _, want := range cmds {
		rendered := want.Render()
		got, err := ParseKafkaCommand(rendered)
		if err != nil {
			t.Fatalf("ParseKafkaCommand(Render(%#v)) = error %v (rendered: %s)", want, err, rendered)
		}
		if !reflect.DeepEqual(*got, want) {
			t.Fatalf("round-trip = %#v, want %#v (rendered: %s)", *got, want, rendered)
		}
	}
}

// TestKafkaCommand_PolicyStringIsUnchangedTwoTokenForm 是本 Plan 最要紧的测试。
//
// 它锁住：新 DSL 产出的策略串与今天 7 个 kafka_* 工具产出的**逐字节相同**。
// 不相同的后果不是报错，而是静默——splitKafkaRule 在非 2-token 输入上返回 false，
// MatchKafkaRule 随之返回 false，于是 BuiltinKafkaDangerousDeny 的 deny 规则不再匹配
// 任何东西（fail-open），而 allowlist 侧变成永远 NeedConfirm。两种都不会抛错、不会记日志。
//
// 右侧的 want 值直接抄自现有 Kafka*Command 的实现（internal/ai/helper/kafka_command.go）
// 与内置组（internal/model/entity/policy_group_entity/policy_group.go:305-408），
// 不是"跑一遍看看输出什么"填进来的——那样测试会跟着实现一起错。
func TestKafkaCommand_PolicyStringIsUnchangedTwoTokenForm(t *testing.T) {
	cases := []struct{ in, want string }{
		{`cluster overview`, "cluster.read *"},
		{`cluster brokers`, "broker.read *"},
		{`topic list`, "topic.list *"},
		{`topic describe orders`, "topic.read orders"},
		{`topic create orders --partitions=3`, "topic.create orders"},
		{`topic delete orders`, "topic.delete orders"},
		{`topic update-config orders --config=retention.ms=1000`, "topic.config.write orders"},
		{`topic increase-partitions orders --partition-count=6`, "topic.partitions.write orders"},
		{`topic delete-records orders --records='[]'`, "topic.records.delete orders"},
		{`consumer-group list`, "consumer_group.list *"},
		{`consumer-group describe mygroup`, "consumer_group.read mygroup"},
		{`consumer-group reset-offset mygroup --mode=earliest`, "consumer_group.offset.write mygroup"},
		{`consumer-group delete mygroup`, "consumer_group.delete mygroup"},
		{`acl list`, "acl.read *"},
		{`acl create --principal=User:alice`, "acl.write *"},
		{`acl delete --principal=User:alice`, "acl.write *"},
		{`schema list-subjects`, "schema.read *"},
		{`schema describe subj`, "schema.read subj"},
		{`schema register subj --schema='{}'`, "schema.write subj"},
		{`schema delete subj`, "schema.delete subj"},
		{`connect list-connectors`, "connect.read *"},
		{`connect describe conn`, "connect.read conn"},
		{`connect create conn --config='{}'`, "connect.write conn"},
		{`connect pause conn`, "connect.state.write conn"},
		{`connect delete conn`, "connect.delete conn"},
		{`message browse orders`, "message.read orders"},
		{`message produce orders --value=x`, "message.write orders"},
	}
	for _, c := range cases {
		parsed, err := ParseKafkaCommand(c.in)
		if err != nil {
			t.Fatalf("ParseKafkaCommand(%q) unexpected error: %v", c.in, err)
		}
		got, err := parsed.PolicyString()
		if err != nil {
			t.Fatalf("PolicyString() for %q unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("PolicyString() for %q = %q, want %q — a mismatch here silently fails BuiltinKafkaDangerousDeny open",
				c.in, got, c.want)
		}
		if fields := len(strings.Fields(got)); fields != 2 {
			t.Fatalf("PolicyString() for %q = %q has %d fields, want exactly 2 (splitKafkaRule requires it)",
				c.in, got, fields)
		}
	}
}
```

import 块需含 `"strings"`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/helper/ -run Kafka -v`
Expected: FAIL — `undefined: ParseKafkaCommand`。

- [ ] **Step 3: 实现 kafka_dsl.go**

创建 `internal/ai/helper/kafka_dsl.go`：

```go
package helper

import (
	"fmt"
	"sort"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// KafkaCommand 是 kafka 富命令串的结构形式。
//
// 格式：`<family> <verb> [target] [--flags]`
//
// family 对应今天的 7 个 kafka_* 工具，verb 对应各工具的 operation 参数，
// target 是资源名（topic / group / subject / connector），flags 承载其余全部参数。
type KafkaCommand struct {
	Family string
	Verb   string
	Target string
	Flags  map[string]string
}

// kafkaVerbs 列出每个 family 支持的 verb，以及该 verb 是否需要 target。
//
// verb 名与今天工具的 operation 值一一对应，但改用连字符（reset-offset 而非
// reset_offset），与命令行惯例一致；映射在 kafkaOperationFor 里做。
var kafkaVerbs = map[string]map[string]bool{
	"cluster": {
		"overview": false, "brokers": false,
		"broker-config": false, "cluster-configs": false,
	},
	"topic": {
		"list": false, "describe": true, "create": true, "delete": true,
		"update-config": true, "increase-partitions": true, "delete-records": true,
	},
	"consumer-group": {
		"list": false, "describe": true, "reset-offset": true, "delete": true,
	},
	"acl": {
		"list": false, "create": false, "delete": false,
	},
	"schema": {
		"list-subjects": false, "list-versions": true, "describe": true,
		"check-compatibility": true, "register": true, "delete": true,
	},
	"connect": {
		"list-clusters": false, "list-connectors": false, "describe": true,
		"create": true, "update-config": true,
		"pause": true, "resume": true, "restart": true, "delete": true,
	},
	"message": {
		"browse": true, "inspect": true, "produce": true,
	},
}

// kafkaOperationFor 把 DSL 的 verb 还原为现有 Kafka*Command 认得的 operation 值。
// 只有写法不同的才列在这里，其余原样透传。
var kafkaOperationFor = map[string]string{
	"broker-config":       "get_broker_config",
	"cluster-configs":     "list_cluster_configs",
	"update-config":       "update_config",
	"increase-partitions": "increase_partitions",
	"delete-records":      "delete_records",
	"reset-offset":        "reset_offset",
	"list-subjects":       "list_subjects",
	"list-versions":       "list_versions",
	"check-compatibility": "check_compatibility",
	"list-clusters":       "list_clusters",
	"list-connectors":     "list_connectors",
	"describe":            "describe",
	"brokers":             "brokers",
}

// ParseKafkaCommand 解析富命令串。
func ParseKafkaCommand(s string) (*KafkaCommand, error) {
	parsed, err := cmdline.Parse(s)
	if err != nil {
		return nil, err
	}

	verbs, ok := kafkaVerbs[parsed.Verb]
	if !ok {
		return nil, fmt.Errorf("unknown kafka resource family %q; supported: %s",
			parsed.Verb, strings.Join(slices.Sorted(maps.Keys(kafkaVerbs)), ", "))
	}
	if len(parsed.Args) == 0 {
		return nil, fmt.Errorf("missing verb for %q; supported: %s",
			parsed.Verb, strings.Join(slices.Sorted(maps.Keys(verbs)), ", "))
	}

	verb := parsed.Args[0]
	needsTarget, ok := verbs[verb]
	if !ok {
		return nil, fmt.Errorf("unknown verb %q for %q; supported: %s",
			verb, parsed.Verb, strings.Join(slices.Sorted(maps.Keys(verbs)), ", "))
	}
	if len(parsed.Args) > 2 {
		return nil, fmt.Errorf("kafka commands take at most two positional arguments (<family> <verb> [target]); got %d", len(parsed.Args)+1)
	}

	c := &KafkaCommand{Family: parsed.Verb, Verb: verb, Flags: parsed.Flags}
	if len(parsed.Args) == 2 {
		c.Target = parsed.Args[1]
	}
	if needsTarget && c.Target == "" {
		return nil, fmt.Errorf("verb %q requires a target: %s %s <name>", verb, c.Family, verb)
	}
	return c, nil
}

// Render 是 ParseKafkaCommand 的逆函数（TestKafkaCommand_RoundTrip 锁住）。
func (c *KafkaCommand) Render() string {
	cmd := &cmdline.Command{Verb: c.Family, Args: []string{c.Verb}, Flags: c.Flags}
	if c.Target != "" {
		cmd.Args = append(cmd.Args, c.Target)
	}
	return cmd.Render()
}

// PolicyString 返回策略匹配用的 "<action> <resource>" 串。
//
// **必须**恰好两个 token，且与今天 7 个 kafka_* 工具的输出逐字节相同——所以这里
// 直接复用现有的 Kafka*Command 函数（internal/ai/helper/kafka_command.go），不重写一遍。
// 理由见 TestKafkaCommand_PolicyStringIsUnchangedTwoTokenForm 的注释：
// splitKafkaRule 要求恰好 2 段，不满足时 MatchKafkaRule 静默返回 false，
// 于是 BuiltinKafkaDangerousDeny 的 deny 规则不再匹配任何命令（fail-open）。
func (c *KafkaCommand) PolicyString() (string, error) {
	op := c.operation()
	switch c.Family {
	case "cluster":
		return KafkaClusterCommand(op)
	case "topic":
		return KafkaTopicCommand(op, c.Target)
	case "consumer-group":
		return KafkaConsumerGroupCommand(op, c.Target)
	case "acl":
		return KafkaACLCommand(op)
	case "schema":
		return KafkaSchemaCommand(op, c.Target)
	case "connect":
		return KafkaConnectCommand(op, c.Target)
	case "message":
		return KafkaMessageCommand(op, c.Target)
	default:
		return "", fmt.Errorf("unknown kafka resource family %q", c.Family)
	}
}

// operation 返回现有 Kafka*Command 认得的 operation 值。
func (c *KafkaCommand) operation() string {
	if mapped, ok := kafkaOperationFor[c.Verb]; ok {
		return mapped
	}
	return c.Verb
}
```

import 块：

```go
import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
)
```

`slices.Sorted(maps.Keys(...))` 对 `kafkaVerbs`（`map[string]map[string]bool`）与单个 family 的 `verbs`（`map[string]bool`）都适用——泛型按 key 类型推导，两种 map 的 key 都是 `string`。Task 4 的 mongo DSL 用同一写法，两处不要各写一个排序辅助函数。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/ai/helper/ -run Kafka -v`
Expected: PASS。

`TestKafkaCommand_PolicyStringIsUnchangedTwoTokenForm` 若失败，**不要**改 want 值去迁就实现——want 抄自现有 `Kafka*Command` 与内置组，失败说明 `kafkaOperationFor` 的映射漏了或写错了，改映射。

- [ ] **Step 5: 用变异测试验证关键测试非空转**

把 `kafkaOperationFor` 里 `"reset-offset": "reset_offset"` 临时改为 `"reset-offset": "reset_offsets"`（多个 s），跑：

Run: `go test ./internal/ai/helper/ -run PolicyStringIsUnchanged -v`
Expected: **FAIL**，且报错指向 `consumer-group reset-offset`。确认后改回。

这一步不能跳：Plan A 里有四个测试"因为错误的理由通过"，全靠变异测试才发现。

- [ ] **Step 6: lint + 提交**

```bash
go test ./internal/... && golangci-lint run
git add internal/ai/helper/kafka_dsl.go internal/ai/helper/kafka_dsl_test.go
git commit -m "✨ 新增 kafka 命令 DSL，策略串复用现有双 token 格式"
```

---

### Task 7: kafka 执行器（DSL → kafka_svc typed 调用）

把 `KafkaCommand` 映射到 `kafka_svc.Service` 的 typed 方法。现有的 `internal/ai/helper/kafka_args.go` 提供了一整套 `Kafka*RequestFromArgs(assetID, args map[string]any)` 构造器——**复用它们**：把 `KafkaCommand.Flags`（`map[string]string`）转成 `map[string]any` 后直接喂进去，不重写请求构造逻辑。

**Files:**
- Create: `internal/ai/helper/kafka_exec.go`
- Test: `internal/ai/helper/kafka_exec_test.go`

**Interfaces:**
- Consumes: `ParseKafkaCommand`（Task 6）、`kafka_args.go` 的构造器、`kafkaServiceFromCtx`（`internal/ai/helper/kafka_helper.go:36`）
- Produces:
  - `func ExecKafkaOnAsset(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error)`
  - `func CanonicalizeKafkaCommand(asset *asset_entity.Asset, command string) (string, error)`

- [ ] **Step 1: 写失败测试**

创建 `internal/ai/helper/kafka_exec_test.go`：

```go
package helper

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestCanonicalizeKafkaCommand(t *testing.T) {
	cases := []struct{ in, want string }{
		{`topic describe orders`, "topic.read orders"},
		{`topic create orders --partitions=3`, "topic.create orders"},
		{`message produce orders --value=x`, "message.write orders"},
		{`acl delete --principal=User:alice`, "acl.write *"},
	}
	for _, c := range cases {
		got, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, c.in)
		if err != nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalizeKafkaCommand_RejectsBadSyntax(t *testing.T) {
	for _, in := range []string{`nonsense list`, `topic nonsense x`, `topic describe`} {
		if _, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, in); err == nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = nil error, want rejection", in)
		}
	}
}

// TestKafkaFlagsToArgs 锁住 flag→args 转换：数值 flag 必须以字符串进 map[string]any，
// 由既有的 aictx.ArgInt/ArgInt64 负责解析（它们有 string 分支），不在这里提前转型。
func TestKafkaFlagsToArgs(t *testing.T) {
	c := &KafkaCommand{Family: "topic", Verb: "create", Target: "orders",
		Flags: map[string]string{"partitions": "3"}}
	args := kafkaFlagsToArgs(c, 42)
	if args["asset_id"] != int64(42) {
		t.Fatalf("args[asset_id] = %#v, want int64(42)", args["asset_id"])
	}
	if args["topic"] != "orders" {
		t.Fatalf("args[topic] = %#v, want %q", args["topic"], "orders")
	}
	if args["partitions"] != "3" {
		t.Fatalf("args[partitions] = %#v, want %q", args["partitions"], "3")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/helper/ -run 'CanonicalizeKafka|KafkaFlagsToArgs' -v`
Expected: FAIL — `undefined: CanonicalizeKafkaCommand`。

- [ ] **Step 3: 实现 kafka_exec.go**

创建 `internal/ai/helper/kafka_exec.go`。核心是一张 `(family, verb) → 调用` 的分派表；每个分支复用 `kafka_args.go` 的构造器与 `kafka_helper.go` 里既有的 service 调用。

```go
package helper

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ExecKafkaOnAsset 是不含权限检查的纯执行入口，供统一 exec 使用。
// 7 个 kafka_* 工具在 Task 10 一并删除；在那之前两条路径并存。
// scope 对 kafka 无意义，忽略——目标资源写在命令的 target 位。
func ExecKafkaOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	c, err := ParseKafkaCommand(command)
	if err != nil {
		return "", err
	}

	args := kafkaFlagsToArgs(c, asset.ID)
	switch c.Family {
	case "cluster":
		return HandleKafkaCluster(ctx, args)
	case "topic":
		return HandleKafkaTopic(ctx, args)
	case "consumer-group":
		return HandleKafkaConsumerGroup(ctx, args)
	case "acl":
		return HandleKafkaACL(ctx, args)
	case "schema":
		return HandleKafkaSchema(ctx, args)
	case "connect":
		return HandleKafkaConnect(ctx, args)
	case "message":
		return HandleKafkaMessage(ctx, args)
	default:
		return "", fmt.Errorf("unknown kafka resource family %q", c.Family)
	}
}

// kafkaFlagsToArgs 把 KafkaCommand 摊平成既有 Handle* 认得的 map[string]any。
//
// 复用 Handle* 而不是直接调 kafka_svc：那 7 个函数里的 operation 分派、请求构造
// （kafka_args.go 的 ~20 个 *RequestFromArgs）与结果序列化已经写好且有测试覆盖，
// 重写一遍就是 AGENTS.md 说的"第二份实现"。
//
// 数值一律以 string 进 map：aictx.ArgInt / ArgInt64 有 string 分支，能正确解析；
// 在这里提前转 int 反而会绕开它们的既有校验。
//
// 前置条件：本任务 Step 3b 已摘掉 7 个 HandleKafka* 内部的 checkKafkaToolPermission
// 调用。权限检查由 handleExec 统一做（internal/ai/tool/tool_handlers_unified.go），
// 留着内部那次会让走 exec 的 kafka 命令被检查两次——用户若选的是一次性"允许"
// 而非"全部允许"，第二次检查会为同一条命令**再弹一次审批对话框**。
func kafkaFlagsToArgs(c *KafkaCommand, assetID int64) map[string]any {
	args := make(map[string]any, len(c.Flags)+3)
	args["asset_id"] = assetID
	args["operation"] = c.operation()
	for k, v := range c.Flags {
		args[normalizeKafkaFlagName(k)] = v
	}
	if c.Target != "" {
		args[kafkaTargetArgName(c.Family)] = c.Target
	}
	return args
}

// kafkaTargetArgName 返回各 family 里"资源名"对应的 args key，
// 与 kafka_helper.go 各 Handle* 读的 key 一致。
func kafkaTargetArgName(family string) string {
	switch family {
	case "consumer-group":
		return "group"
	case "schema":
		return "subject"
	case "connect":
		return "connector"
	default: // topic / message 都读 "topic"
		return "topic"
	}
}

// normalizeKafkaFlagName 把 DSL 的连字符 flag 名换成 args 的下划线 key。
func normalizeKafkaFlagName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// CanonicalizeKafkaCommand 把富命令串规范化为策略层的双 token 串。
// 排在权限检查之前：语法错误必然导致执行失败，必须在弹审批对话框之前失败。
func CanonicalizeKafkaCommand(_ *asset_entity.Asset, command string) (string, error) {
	c, err := ParseKafkaCommand(command)
	if err != nil {
		return "", err
	}
	return c.PolicyString()
}
```

import 块补 `"strings"`。

- [ ] **Step 3b: 摘掉 HandleKafka* 内部的权限检查**

修改 `internal/ai/helper/kafka_helper.go`：删除 7 处 `checkKafkaToolPermission` 调用（`:56` `:104` `:189` `:240` `:281` `:348` `:435`），随后 `checkKafkaToolPermission`（`:478-488`）成为死代码，一并删除。

**这一步不能推迟到 Task 10。** `ExecKafkaOnAsset` 从本任务起就走 `HandleKafka*`，而权限检查已由 `handleExec` 统一做（`internal/ai/tool/tool_handlers_unified.go`）。留着内部那次 = 同一条命令被检查两次；用户若选的是一次性"允许"而非"全部允许"，第二次检查**会再弹一次审批对话框**，并多写一条审计行。这与 Plan A spec §5「被批准的 == 被执行的」是同一类不变式。

删除后，7 个 `kafka_*` 旧工具在 Task 10 删除之前会**暂时无权限检查**（它们直连 `HandleKafka*`）。这是可接受的：旧工具在本分支上只存活到 Task 10，且 Task 8 起模型面已改用 `exec`（prompt 在 Task 11 才更新，但 `exec` 的描述已覆盖 kafka）。若评审认为这个窗口不可接受，替代方案是把 Task 10 的工具删除提前到本任务——代价是 Task 8 的端到端测试要跟着重排。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/ai/helper/ ./internal/ai/ -run Kafka -v`
Expected: PASS。`internal/ai/kafka_helper_test.go` 若有测试断言 `HandleKafka*` 在无权限时返回拒绝消息，它们会失败——那些断言随检查一起删除，并在提交信息里点名。

- [ ] **Step 5: lint + 提交**

```bash
go test ./internal/... && golangci-lint run
git add internal/ai/helper/
git commit -m "✨ kafka 执行器：DSL 分派到既有 Handle*，权限检查上移至 handleExec"
```

---

### Task 8: kafka 接入 exec + SKILL.md

**Files:**
- Create: `internal/ai/skills/kafka/SKILL.md`
- Modify: `internal/ai/execimpl/register.go`
- Modify: `internal/ai/execimpl/coverage_test.go`
- Test: `internal/ai/tool/tool_handlers_unified_kafka_test.go`

- [ ] **Step 1: 写 SKILL.md**

创建 `internal/ai/skills/kafka/SKILL.md`（目录名必须等于 `asset_entity.AssetTypeKafka` 的值）：

```markdown
---
name: kafka
description: "Manage Kafka topics, consumer groups, ACLs, schemas, connectors and messages via exec."
---

# Kafka assets

## Command syntax

`<family> <verb> [target] [--flags]`

### cluster

- `cluster overview`
- `cluster brokers`
- `cluster broker-config --broker-id=1`
- `cluster cluster-configs`

### topic

- `topic list [--include-internal] [--search=orders] [--page=1] [--page-size=50]`
- `topic describe orders`
- `topic create orders --partitions=3 --replication-factor=2 [--configs='{"retention.ms":"604800000"}']`
- `topic delete orders`
- `topic update-config orders --config-updates='[{"key":"retention.ms","value":"1000"}]'`
- `topic increase-partitions orders --partition-count=6`
- `topic delete-records orders --records='[{"partition":0,"offset":100}]'`

### consumer-group

- `consumer-group list`
- `consumer-group describe mygroup`
- `consumer-group reset-offset mygroup --topic=orders --mode=earliest`
- `consumer-group reset-offset mygroup --topic=orders --mode=offset --offset=1000 --partitions='[0,1]'`
- `consumer-group delete mygroup`

### acl

- `acl list [--resource-type=topic] [--resource-name=orders] [--principal=User:alice]`
- `acl create --resource-type=topic --resource-name=orders --principal=User:alice --acl-operation=read --permission=allow`
- `acl delete --resource-type=topic --resource-name=orders --principal=User:alice --acl-operation=read --permission=allow`

### schema

- `schema list-subjects`
- `schema list-versions mysubject`
- `schema describe mysubject [--version=3]`
- `schema check-compatibility mysubject --schema='{"type":"record","name":"X","fields":[]}'`
- `schema register mysubject --schema='{"type":"record","name":"X","fields":[]}' [--schema-type=AVRO]`
- `schema delete mysubject [--permanent]`

### connect

- `connect list-clusters`
- `connect list-connectors [--cluster=main] [--only-failed]`
- `connect describe myconnector [--include-tasks]`
- `connect create myconnector --config='{"connector.class":"..."}'`
- `connect update-config myconnector --config='{"tasks.max":"2"}'`
- `connect pause myconnector` · `connect resume myconnector` · `connect restart myconnector`
- `connect delete myconnector`

### message

- `message browse orders [--partition=0] [--start-mode=latest] [--limit=100] [--decode-mode=json]`
- `message inspect orders --offset=1000 [--partition=0]`
- `message produce orders --value='{"a":1}' [--key=k1] [--value-encoding=string] [--headers='[{"key":"h","value":"v"}]']`

## Notes

- Always single-quote any flag value containing JSON, spaces, or braces.
- Flag names use hyphens (`--replication-factor`), values are passed through verbatim.
- `acl create` and `acl delete` require the full principal/resource/operation set —
  omitting one does not default it, it fails.
- Approval for Kafka is granted at `<action> <resource>` granularity: approving
  `message.write orders` approves any payload to that topic, and `acl.write *`
  covers both granting and revoking ACLs.
- The `scope` parameter is not used by Kafka assets.
```

- [ ] **Step 2: 写失败测试**

创建 `internal/ai/tool/tool_handlers_unified_kafka_test.go`：

```go
package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestHandleExec_KafkaChecksTwoTokenPolicyString 端到端锁住：exec 对 kafka 资产做
// 权限检查时，送进 CheckForAsset 的是双 token 策略串，不是模型写的富命令串。
//
// 用审批回调观察 —— 它是 CommandPolicyChecker 唯一的注入点，也正是策略串会被展示
// 与持久化（SaveGrantPattern）的地方。命令特意选写操作（topic delete），因为
// BuiltinKafkaMetadataReadOnly 的 allowlist 只覆盖读操作；用读操作会直接 Allow，
// 回调根本不触发，断言就成了空转。
func TestHandleExec_KafkaChecksTwoTokenPolicyString(t *testing.T) {
	m := setupUnified(t)

	asset := &asset_entity.Asset{ID: 7, Name: "kafka-prod", Type: asset_entity.AssetTypeKafka}
	m.EXPECT().FindByName(gomock.Any(), "kafka-prod").Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(asset, nil).AnyTimes()

	var gotCheckCommand string
	confirm := func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
		if len(items) > 0 {
			gotCheckCommand = items[0].Command
		}
		return permission.ApprovalResponse{Decision: "deny"}
	}
	checker := permission.NewCommandPolicyChecker(confirm)

	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeKafka)

	_, err := handleExec(ctx, map[string]any{
		"asset":   "kafka-prod",
		"command": `topic delete orders`,
	})
	if err != nil {
		t.Fatalf("handleExec unexpected error: %v", err)
	}

	if gotCheckCommand != "topic.delete orders" {
		t.Fatalf("permission check saw %q, want %q", gotCheckCommand, "topic.delete orders")
	}
	if fields := len(strings.Fields(gotCheckCommand)); fields != 2 {
		t.Fatalf("permission check saw %d fields, want exactly 2 (splitKafkaRule requires it)", fields)
	}
}

// TestHandleExec_KafkaSyntaxErrorFailsBeforeApproval 锁住排序：语法错误必须在
// 审批回调触发之前返回，否则用户先被弹一次审批、批准之后命令才因为解析失败而没跑。
func TestHandleExec_KafkaSyntaxErrorFailsBeforeApproval(t *testing.T) {
	m := setupUnified(t)

	asset := &asset_entity.Asset{ID: 7, Name: "kafka-prod", Type: asset_entity.AssetTypeKafka}
	m.EXPECT().FindByName(gomock.Any(), "kafka-prod").Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(asset, nil).AnyTimes()

	checker, called := newRecordingChecker()

	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeKafka)

	_, err := handleExec(ctx, map[string]any{
		"asset":   "kafka-prod",
		"command": `topic nonsense orders`,
	})
	if err == nil {
		t.Fatal("handleExec with bad syntax = nil error, want rejection")
	}
	if *called {
		t.Fatal("approval callback fired for a command that cannot execute — canonicalize must run before the permission check")
	}
}
```

import 块补 `"go.uber.org/mock/gomock"`。

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/ai/tool/ -run KafkaChecks -v`
Expected: FAIL — kafka 尚未注册执行器，`handleExec` 返回 unsupported type 错误。

- [ ] **Step 4: 注册执行器**

修改 `internal/ai/execimpl/register.go`，在 mongo 之后加：

```go
	kafkaHelp, _ := skills.Get(asset_entity.AssetTypeKafka)
	permission.RegisterExecutor(asset_entity.AssetTypeKafka,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecKafkaOnAsset(ctx, asset, command, scope)
		}, kafkaHelp, helper.CanonicalizeKafkaCommand)
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/ai/tool/ -run Kafka -v`
Expected: PASS。

- [ ] **Step 6: 变异测试验证非空转**

把 `internal/ai/tool/tool_handlers_unified.go` 里权限检查那行的 `checkCommand` 临时改为 `command`（即送原始富命令串去检查），跑：

Run: `go test ./internal/ai/tool/ -run KafkaChecksTwoToken -v`
Expected: **FAIL**，报 `permission check saw "topic delete orders", want "topic.delete orders"`。确认后改回。

再把 `handleExec` 里 canonicalize 段整体移到权限检查之后，跑：

Run: `go test ./internal/ai/tool/ -run KafkaSyntaxError -v`
Expected: **FAIL**，报 approval callback fired。确认后改回。

- [ ] **Step 7: 豁免清单清零**

修改 `internal/ai/execimpl/coverage_test.go`：删除 `"kafka": "Plan B",`，`maxExemptions` 改为 `1`。此时清单只剩 `local`（spec §2 非目标，已由 issue #250 跟踪）。同步更新文件头注释里的类型列表。

- [ ] **Step 8: 全量测试 + lint，提交**

```bash
go test ./internal/... && golangci-lint run
git add internal/ai/skills/kafka/ internal/ai/execimpl/ internal/ai/tool/tool_handlers_unified_kafka_test.go
git commit -m "✨ kafka 接入统一 exec，豁免清单缩至 local"
```

---

### Task 9: 解开 exec → run_command 的审计别名

`internal/ai/audit/extractor.go:34-35` 有一句硬别名：

```go
	if toolName == "exec" {
		toolName = "run_command"
	}
```

统一 `exec` 的审计摘要是借 `run_command` 的提取器产出的。**Task 10 删掉 `run_command` 的提取器注册就会打断 `exec` 的审计**，所以必须先解开这个耦合。

**Files:**
- Modify: `internal/ai/audit/extractor.go:23-40`
- Modify: `internal/ai/audit/extractor_default.go`
- Test: `internal/ai/audit/extractor_default_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/ai/audit/extractor_default_test.go` 末尾追加：

```go
// TestExtractor_ExecHasItsOwnRegistration 锁住 exec 不再依赖 run_command 的提取器。
// 别名存在期间这个测试也能过（别名会把 exec 转成 run_command），所以它单独不够——
// 配套的 TestExtractor_ExecSurvivesRunCommandRemoval 才是真正的守卫。
func TestExtractor_ExecHasItsOwnRegistration(t *testing.T) {
	got := Extract("exec", map[string]any{"asset": "web-1", "command": "uptime"})
	if got != "uptime" {
		t.Fatalf("Extract(exec) = %q, want %q", got, "uptime")
	}
}

// TestExtractor_ExecSurvivesRunCommandRemoval 直接模拟 Task 10 的删除动作：
// 摘掉 run_command 的注册后，exec 的提取必须仍然工作。
func TestExtractor_ExecSurvivesRunCommandRemoval(t *testing.T) {
	restore := unregisterExtractorForTest("run_command")
	t.Cleanup(restore)

	got := Extract("exec", map[string]any{"asset": "web-1", "command": "uptime"})
	if got != "uptime" {
		t.Fatalf("Extract(exec) after removing run_command = %q, want %q — exec must not depend on run_command's extractor", got, "uptime")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/audit/ -run ExecSurvives -v`
Expected: FAIL — `undefined: unregisterExtractorForTest`。

- [ ] **Step 3: 实现**

在 `internal/ai/audit/extractor.go` 增加测试辅助：

```go
// unregisterExtractorForTest 摘掉一个已注册的提取器并返回还原函数。仅供测试：
// 用来验证某个工具的提取不依赖另一个工具的注册。
func unregisterExtractorForTest(toolName string) func() {
	old, existed := extractors[toolName]
	delete(extractors, toolName)
	return func() {
		if existed {
			extractors[toolName] = old
		}
	}
}
```

（`extractors` 的实际变量名以 `extractor.go` 现有实现为准，实施时读该文件确认。）

在 `internal/ai/audit/extractor_default.go` 里为 `exec` 单独注册，紧邻 `run_command` 那行：

```go
	// exec 与 run_command 读同一个 command 参数，但必须各自注册：
	// 曾经 exec 是靠 extractor.go 里的一句 toolName 别名借用 run_command 的提取器的，
	// 那让新工具的审计依赖旧工具的注册是否存在。run_command 在 Plan B 里被删除，
	// 别名随之移除。
	RegisterExtractor("exec", func(a map[string]any) string { return aictx.ArgString(a, "command") })
```

删除 `internal/ai/audit/extractor.go:34-35` 的别名分支，并同步删掉它上方 `:23-31` 那段解释别名的注释。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/ai/audit/ ./internal/ai/runner/ -v`
Expected: PASS。

`internal/ai/runner/audit_exec_asset_test.go:191` 有一条注释提到该别名——同步改掉，否则它会变成说谎的注释。

- [ ] **Step 5: 全量测试 + lint，提交**

```bash
go test ./internal/... && golangci-lint run
git add internal/ai/audit/ internal/ai/runner/
git commit -m "♻️ exec 独立注册审计提取器，解开对 run_command 的别名依赖"
```

---

### Task 10: 删除 14 个旧工具

删除清单（14 个）：`run_command`、`run_serial_command`、`exec_sql`、`exec_redis`、`exec_k8s`、`exec_mongo`、`exec_etcd`、`kafka_cluster`、`kafka_topic`、`kafka_consumer_group`、`kafka_acl`、`kafka_schema`、`kafka_connect`、`kafka_message`。

工具总数 29 → 15。`AllToolDefs()` 24 → 12（它本就不含 `run_serial_command` / `batch_command` / `exec` / `help`；本任务同时把 `exec` / `help` 加进去，见 Step 4）。

**Files:**
- Modify: `internal/ai/tool/tools_exec.go`、`tools_data.go`、`tools_kafka.go`（删除）、`tools.go:11`（容量）
- Modify: `internal/ai/tool/tool_registry.go:40-68`
- Modify: `internal/ai/helper/`：删除 `HandleExecSQL` / `HandleExecRedis` / `HandleExecMongo` / `HandleExecEtcd` / `HandleRunSerialCommand`，并摘掉 7 个 `HandleKafka*` 内部的 `checkKafkaToolPermission` 调用
- Modify: `internal/ai/tool/tool_handlers_exec.go`（删 `handleRunCommand`）、`tool_handler_k8s.go`（删 `handleExecK8s`）
- Modify: `internal/ai/audit/extractor_default.go`、`internal/ai/tool/audit_extractor_k8s.go`、`audit_extractor_kafka.go`
- Modify: `cmd/opsctl/command/db.go`、`batch.go`
- Modify: `frontend/src/components/ai/ToolBlock.tsx:23`
- Test: `internal/ai/tool/tools_test.go`、以及 §4.2 列出的 13 个测试文件

- [ ] **Step 1: 先改测试（本任务的"失败测试"就是更新后的清单）**

修改 `internal/ai/tool/tools_test.go:25-40` 的 `expected` 列表，删除 14 个名字，只留 15 个：

```go
		expected := []string{
			// asset
			"list_assets", "get_asset", "add_asset", "update_asset",
			"list_groups", "get_group", "add_group", "update_group",
			// exec
			"upload_file", "download_file", "batch_command", "request_permission",
			// extension
			"exec_tool",
			// unified
			"exec", "help",
		}
```

修改 `:49-56` 的 `serialNames`，删除已移除的名字，只留：

```go
		serialNames := []string{"exec_tool", "exec"}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/tool/ -run TestTools -v`
Expected: FAIL — 旧工具仍注册着，实际集合大于 expected。（`ShouldContainKey` 不检查穷尽性，所以这一步可能反常地**通过**；若如此，先加一条数量断言：）

```go
		if len(tools) != len(expected) {
			t.Fatalf("Tools() returned %d tools, want %d", len(tools), len(expected))
		}
```

这条数量断言应**保留**——研究发现现有测试从不检查穷尽性，registry 漂移（`run_serial_command` 注册了却不在任何清单里）因此一直没被发现。

- [ ] **Step 3: 删除工具定义**

- `internal/ai/tool/tools_exec.go`：删除 `run_command`（`:18`）与 `run_serial_command`（`:38`）两个 `tool.Tool` 定义。保留 `upload_file` / `download_file` / `batch_command` / `request_permission`。
- `internal/ai/tool/tools_data.go`：删除全部 5 个定义（`exec_sql` `:16`、`exec_redis` `:37`、`exec_mongo` `:58`、`exec_etcd` `:81`、`exec_k8s` `:112`）。文件若因此为空则整个删除，并从 `tools.go:10-19` 的聚合里移除对应调用。
- `internal/ai/tool/tools_kafka.go`：整个文件删除，并从 `tools.go` 的聚合里移除 `kafkaTools()`。
- `internal/ai/tool/tools.go:11`：容量 `29` 改为 `15`。

同时更新两处**模型可见**的旧工具名文案：
- `tools_exec.go:101`（`batch_command` 的 DescStr）：把 "Prefer this over looping run_command/exec_sql/exec_redis…" 改为 "Prefer this over looping exec calls…"
- `tools_exec.go:121`（`request_permission` 的 DescStr）：把 "subsequent run_command/exec_sql/exec_redis/exec_mongo/exec_k8s/kafka_* calls…" 改为 "subsequent exec calls…"

以及 `internal/ai/tool/local_tool_wrap.go:28` 的**模型可见拒绝串**：把 `"run_command / exec_sql / exec_redis / exec_mongo / exec_k8s. "` 改为 `"exec. "`；同步改 `:25` 的注释。

- [ ] **Step 4: 更新 AllToolDefs**

修改 `internal/ai/tool/tool_registry.go:40-68`：删除 12 个已移除工具的条目（`run_serial_command` 本就不在），并**新增** `exec` 与 `help`——opsctl 的 `db.go` / `batch.go` 要改用 `exec`（Step 6），必须能查到它。

```go
		{"exec", handleExec},
		{"help", handleHelp},
```

同步更新 `tool_registry.go:37-39` 的注释（原文说明 `AllToolDefs` 省略了哪些）与 `docs/ARCHITECTURE.md:97` 的对应描述。

- [ ] **Step 5: 删除 handler 与审计提取器**

- `internal/ai/tool/tool_handlers_exec.go`：删除 `handleRunCommand`（`:71-91`）。
- `internal/ai/tool/tool_handler_k8s.go`：删除 `handleExecK8s`（`:16-`）。文件若只剩它则整个删除。
- `internal/ai/helper/`：删除 `HandleExecSQL`（`database_helper.go:65`）、`HandleExecRedis`（`redis_helper.go:50`）、`HandleExecMongo`（`mongodb_helper.go:49`）、`HandleExecEtcd`（`etcd_helper.go:22`）、`HandleRunSerialCommand`（`serial_helper.go:29`）。对应的 `Exec*OnAsset` **保留**——它们才是现在的唯一执行路径。
- `internal/ai/helper/kafka_helper.go`：7 个 `HandleKafka*` **保留**（`ExecKafkaOnAsset` 在调用它们）。它们内部的 `checkKafkaToolPermission` 调用**已在 Task 7 Step 3b 删除**，本任务无需再动——若发现它们还在，说明 Task 7 没做完，先回去补。
- `internal/ai/audit/extractor_default.go`：删除 `run_command` `:11`、`exec_sql` `:18`、`exec_redis` `:19`、`exec_mongo` `:20`、`exec_etcd` `:21-36`（含手抄的 etcd format 副本）。**保留** Task 9 新增的 `exec` 注册。
- `internal/ai/tool/audit_extractor_k8s.go`、`audit_extractor_kafka.go`：整个删除。
- `internal/ai/audit/audit.go:3-4` 的 doc 注释：删掉列举旧工具名的那句。

- [ ] **Step 6: 更新 opsctl**

`cmd/opsctl/command/db.go` 与 `batch.go` 按名字查 `AllToolDefs()`，删注册项会让 CLI **运行时**断（不是编译期）。改法：把工具名换成 `"exec"`，并把结构化参数拼成命令串。

`db.go:66,78`（sql）：

```go
	// 旧：callHandler(ctx, "exec_sql", map[string]any{"asset_id": id, "sql": sqlText, "database": db})
	args := map[string]any{"asset": strconv.FormatInt(id, 10), "command": sqlText}
	if db != "" {
		args["scope"] = db
	}
	out, err := callHandler(ctx, "exec", args)
```

`db.go:120,132`（redis）同理，`scope` 用 db 序号。

`db.go:186,198`（mongo）：命令串改用 Task 4 的 DSL —— `(&helper.MongoCommand{Op: op, Collection: coll, Database: db, Query: query}).Render()`。

`batch.go:249,254,271` 与 `batch.go:506-510` 的 `batchAuditTool()` 类型→工具名映射：三个分支统一返回 `"exec"`，映射函数随之可简化为常量。

`cmd/opsctl/command/approval.go:48` 的注释提到 `run_command`——同步改掉。

- [ ] **Step 7: 更新前端图标映射**

`frontend/src/components/ai/ToolBlock.tsx:23`：把 `run_command` 换成 `exec`，并加一条 `help`：

```tsx
  exec: Terminal,
  help: BookOpen,
  request_permission: Shield,
```

`BookOpen` 需从 `lucide-react` 引入。其余工具本就走 `|| Terminal` 默认分支（`:44`），工具名原样渲染（`:78`），无 i18n 表需要改。

- [ ] **Step 8: 更新其余测试文件**

按研究清单逐个更新，把旧工具名换成 `exec`（或删除该用例）：`cmd/opsctl/command/handler_test.go`、`internal/ai/audit_test.go`、`internal/ai/kafka_helper_test.go`（`TestAllToolDefsContainsGroupedKafkaTools` 整个删除）、`internal/ai/runner/audit_hook_test.go`、`stream_event_test.go`、`message_convert_test.go`、`internal/ai/audit/audit_asset_ref_test.go`、`internal/ai/tool/local_tool_wrap_test.go`、`internal/ai/audit/extractor_default_test.go`、`internal/model/entity/conversation_entity/conversation_test.go`、`cmd/opsctl/command/batch_test.go`。

`internal/ai/kafka_helper_test.go:18-145` 断言 7 个 `Kafka*Command` 的输出串——**全部保留**，它们现在是 `PolicyString()` 的地基，是防止策略串漂移的第一道防线。

- [ ] **Step 9: 全量测试 + lint**

Run: `go test ./internal/... ./cmd/... && golangci-lint run && cd frontend && pnpm test && pnpm lint`
Expected: 全绿。

- [ ] **Step 10: 提交**

```bash
git add -A
git commit -m "🔥 删除 14 个按类型区分的旧 AI 工具，工具面 29 → 15"
```

---

### Task 11: 清理 prompt_builder 路由表与残留文案

**Files:**
- Modify: `internal/ai/runner/prompt_builder.go:41-44`、`:156-164`、`:181-183`
- Modify: `internal/ai/runner/system_template.go:13-15`
- Modify: `docs/ARCHITECTURE.md:97`
- Test: `internal/ai/runner/prompt_builder_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/ai/runner/prompt_builder_test.go` 追加：

```go
// TestPrompt_NamesNoRemovedTools 锁住 prompt 不再向模型宣传已删除的工具。
// 让模型调用一个不存在的工具，代价是一整轮无谓的往返加一条报错。
func TestPrompt_NamesNoRemovedTools(t *testing.T) {
	removed := []string{
		"run_command", "run_serial_command", "exec_sql", "exec_redis", "exec_k8s",
		"exec_mongo", "exec_etcd", "kafka_cluster", "kafka_topic",
		"kafka_consumer_group", "kafka_acl", "kafka_schema", "kafka_connect", "kafka_message",
	}
	builder := NewPromptBuilder("en", AIContext{})
	prompt := builder.Build()
	for _, name := range removed {
		if strings.Contains(prompt, name) {
			t.Errorf("system prompt still names removed tool %q", name)
		}
	}
}
```

（`NewPromptBuilder` / `Build` 的确切签名以 `prompt_builder.go` 现有实现为准，实施时读该文件对齐；`kafka_*` 的匹配注意 `kafka_topic` 是 `kafka_topic` 不是 `kafka.topic`。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/ai/runner/ -run NamesNoRemovedTools -v`
Expected: FAIL，列出 `prompt_builder.go:159` 与 `:161` 里仍在宣传的工具名。

- [ ] **Step 3: 重写 buildKnowledgeGuidance**

`prompt_builder.go:159` 整句替换为：

```
Running commands on an asset: use exec(asset, command), preceded by help(asset) the first time you touch a given asset type. exec dispatches on the asset's real type and covers SSH, serial, database, Redis, K8s, MongoDB, etcd and Kafka assets. upload_file / download_file handle SFTP transfer, and batch_command runs the same operation across several assets.
```

`:161`（local-vs-remote）里的旧工具枚举替换：

```
... you MUST reach it through a remote tool: exec(asset, command) for every remote asset type (use `cat`/`ls`/`grep` inside an SSH exec for file inspection), and upload_file / download_file for SFTP transfer. ...
```

`:41-44` 的 doc 注释（"token savings from collapsing 14 tools into exec/help"）改为陈述已完成的事实；`:181-183` 那句"只能退回按类型的旧工具——那正是本分支要收敛掉的用法"已经过时（旧工具不复存在），删除。

- [ ] **Step 4: 补 system_template 的 exec 条目**

`internal/ai/runner/system_template.go:13-15` 的能力列表里**没有** `exec` 条目（研究发现）。加一条，置于列表首位：

```
- exec(asset, command) / help(asset): run commands on any remote asset — servers, databases, Redis, Kafka, K8s and more. Call help first for a type you have not used in this conversation.
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/ai/runner/ -v`
Expected: PASS。

- [ ] **Step 6: 更新 ARCHITECTURE.md**

`docs/ARCHITECTURE.md:97` 的 `tool` 行：更新工具数（29 → 15）与 `AllToolDefs()` 的省略集合描述（现在只省略 `batch_command`）。

- [ ] **Step 7: 全量验证 + 提交**

```bash
go test ./internal/... ./cmd/... && golangci-lint run
git add internal/ai/runner/ docs/ARCHITECTURE.md
git commit -m "📝 prompt 与架构文档对齐 exec 单一入口"
```

---

## 收尾（不是任务，是交付前必须做的事）

- [ ] **GUI 可观测验证。** 后端测试覆盖不到审批弹窗与真实连接。最小通过集：
  1. 对一个 **kafka** 资产跑 `help` → `exec topic list`，确认返回真实 topic 列表；
  2. 对同一资产跑 `exec topic delete <某个测试 topic>`，确认**弹出审批**且弹窗里显示的是 `topic.delete <name>`（双 token），点"允许"后确实删除；
  3. 对一个 **etcd** 资产跑 `exec put /test/k 'hello world'` 再 `exec get /test/k`，确认值含空格且完整；
  4. 对一个 **mongodb** 资产跑 `exec find <collection> --query='{"filter":{},"limit":1}'`；
  5. 读 `logs/opskat.log` 与 `opskat.db` 的 `audit_logs`，确认每次调用的 `tool_name` 都是 `exec`、`command` 列是**富命令串**、`decision` 与 `decision_source` 符合预期。

  第 2 条是重点：它是唯一能观察到"策略串没漂移"的端到端路径。若弹窗显示的不是双 token，立即停止合并——那意味着 `BuiltinKafkaDangerousDeny` 已经 fail-open。

- [ ] **存量 grant 抽查。** 本 Plan 刻意不改策略串格式，所以存量 grant 应当继续生效。验证：在改动前对某 kafka 资产批一次 `topic.read orders`（勾选"全部允许"），改动后用 `exec topic describe orders` 确认**不再弹窗**。若弹了，说明 canonicalizer 的输出与旧工具不一致，回到 Task 6 Step 4。

## 已知不做（留给后续 issue）

- **审批粒度仍是策略串粒度。** kafka 批 `message.write orders` 等于批该 topic 的任意 payload；mongo 批 `deleteMany` 看不到 collection 与 filter。这与今天的行为**完全一致**，本 Plan 不构成回归，但也没改善。要改善需让审批弹窗展示原始富命令、同时仍按 canonical 串匹配——那是对 Plan A `ApprovalItem` 结构的改动，单独开 issue。
- **kafka 动作词仍有塌缩。** `acl create` 与 `acl delete` 都映射到 `acl.write *`，`pause`/`resume`/`restart` 都映射到 `connect.state.write`。拆开它们需要改内置组 + 存量 grant 迁移，属于真正的策略模型变更，不在本 Plan。
- **`broker.config.read *` 无内置组覆盖**（研究发现：`KafkaClusterCommand` 会产出它，但 `BuiltinKafkaMetadataReadOnly` 只有 `broker.read *` 与 `cluster.config.read *`）。这是既有缺口，`cluster broker-config` 因此总会走审批。单独开 issue 确认是有意还是遗漏。
- **`local` 类型仍无执行器**（豁免清单最后一条），由 issue #250 跟踪。
- **`batch_command` 仍然完全不可用**：`Asset string` → `handleGetAsset` → `aictx.ArgInt64` 无 string 分支，恒返回 0（Plan A 期间发现的既有缺陷）。它现在是唯一还在用旧式资产引用的工具；改用 `assetref.Resolve` 即可修复，属 Plan C。
