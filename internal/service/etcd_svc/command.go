package etcd_svc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// ExecRequest 是 etcd 服务层操作请求,既给 IPC 用,也给 Dispatch 用。
type ExecRequest struct {
	AssetID    int64
	Op         string
	Key        string
	Value      string
	Prefix     bool
	Limit      int64
	Revision   int64
	LeaseID    int64
	Args       map[string]any
	ApprovalID string
	Source     string
}

// supportedOps 是 ParseCommand 与 Dispatch 共用的合法 op 集合。
// 复合命令(member list / endpoint status 等)在解析阶段已经被规范化为下划线形式。
// 新增 op 时必须同步在 Dispatch 中加分支,守护测试 TestSupportedOpsAreDispatchable 会校验。
var supportedOps = map[string]bool{
	"get": true, "put": true, "del": true,
	"lease_grant": true, "lease_revoke": true, "lease_list": true,
	"endpoint_status": true, "endpoint_health": true,
	"member_list": true,
}

// ParseCommand 解析 etcd 命令串,是 FormatCommand 的逆函数
// （TestParseFormat_RoundTrip 锁住这条性质）。目前全仓没有生产调用方——只被本包
// 测试直接调用；接入统一 exec 工具是 docs/superpowers/plans/2026-07-20-ai-tool-exec-convergence.md
// 的 Task 3（etcd 接入 exec）的范围。
// 不追求 etcdctl 完全兼容,只识别支持的子集:
//
//	<op> [key] [value...] [--flag] [--flag=val]
//
// 复合命令 "member list" / "endpoint status" / "lease grant" 自动归一为下划线形式。
func ParseCommand(s string) (*ExecRequest, error) {
	tokens, err := cmdline.Words(s)
	if err != nil {
		return nil, err
	}

	op := strings.ToLower(tokens[0])
	rest := tokens[1:]

	// 二词复合命令归一
	if len(rest) > 0 {
		switch op {
		case "member", "endpoint":
			combined := op + "_" + strings.ToLower(rest[0])
			if supportedOps[combined] {
				op = combined
				rest = rest[1:]
			}
		case "lease":
			combined := "lease_" + strings.ToLower(rest[0])
			if supportedOps[combined] {
				op = combined
				rest = rest[1:]
			}
		}
	}
	if !supportedOps[op] {
		return nil, fmt.Errorf("unsupported op: %s", op)
	}

	req := &ExecRequest{Op: op}
	positional := []string{}
	for _, t := range rest {
		if !strings.HasPrefix(t, "--") {
			positional = append(positional, t)
			continue
		}
		flag := strings.TrimPrefix(t, "--")
		name, val := flag, ""
		if eq := strings.Index(flag, "="); eq >= 0 {
			name = flag[:eq]
			val = flag[eq+1:]
		}
		switch name {
		case "prefix":
			req.Prefix = true
		case "limit":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid --limit: %s", val)
			}
			req.Limit = n
		case "revision":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid --revision: %s", val)
			}
			req.Revision = n
		case "lease":
			n, err := strconv.ParseInt(val, 16, 64) // lease id 一般为 hex
			if err != nil {
				return nil, fmt.Errorf("invalid --lease: %s", val)
			}
			req.LeaseID = n
		case "ttl":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid --ttl: %s", val)
			}
			if req.Args == nil {
				req.Args = map[string]any{}
			}
			req.Args["ttl"] = n
		default:
			return nil, fmt.Errorf("unknown flag: --%s", name)
		}
	}

	switch op {
	case "get", "del":
		// 要求非空 key，而不是像 endpoint_status/endpoint_health 那样把 key 当可选：
		// 一个形如 "--prefix" 的 key（FormatCommand 对它不加引号，因为 "-" 本身在
		// safeUnquotedWord 允许表里）在这里如果被当成可选参数放过，会与"没给 key、
		// 只给了 --prefix flag"这个完全不同的请求渲染成同一个字符串——对 del 来说，
		// 后者是"用空串前缀删除整个 key 空间"，是本任务范围内能做的最安全选择：
		// 拒绝到底，把静默的灾难性误解析变成一次响亮的拒绝，而不是猜测意图。
		if len(positional) == 0 {
			if bad := dashPrefixedRestToken(rest); bad != "" {
				return nil, fmt.Errorf("%s requires a key; %q was parsed as a flag, not a key — etcd keys starting with \"-\" are not addressable through this command syntax", op, bad)
			}
			return nil, fmt.Errorf("%s requires a key", op)
		}
		req.Key = positional[0]
	case "endpoint_status", "endpoint_health":
		if len(positional) >= 1 {
			req.Key = positional[0]
		}
	case "put":
		if len(positional) < 2 {
			if bad := dashPrefixedRestToken(rest); bad != "" {
				return nil, fmt.Errorf("put requires key and value; %q was parsed as a flag, not positional data — etcd keys/values starting with \"-\" are not addressable through this command syntax", bad)
			}
			return nil, errors.New("put requires key and value")
		}
		req.Key = positional[0]
		// 保留 Join：`put /msg hello world` 仍还原为 "hello world"，
		// 既有的 TestParseCommand_PutMultiWordValue（command_test.go:58）锁着这条契约。
		// round-trip 不受影响——FormatCommand 对含空格的值总是加引号，
		// 引号内的空格经 cmdline.Words 已收进单个 token，Join 一个元素是恒等。
		req.Value = strings.Join(positional[1:], " ")
	}
	return req, nil
}

// dashPrefixedRestToken 返回 rest 中第一个以 '-' 开头的原始 token（没有则返回空串）。
// 走到调用处时，它必然已经通过了上面的 flag 循环——未识别的 --xxx 在那里已经直接
// 报错——所以这里找到的一定是一个被成功识别为 flag 的 token，用来在"必需的位置参数
// 缺失"错误里指出真正原因：它很可能是调用方想要的 key/value，只是形似 flag 被当 flag
// 吃掉了。
func dashPrefixedRestToken(rest []string) string {
	for _, t := range rest {
		if strings.HasPrefix(t, "-") {
			return t
		}
	}
	return ""
}

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
	// put 恒定需要两个位置参数（key、value）——etcd 允许空值，所以 value 不能像其他
	// op 那样用"非空才输出"当判断：那样 {Op:"put", Value:""} 会被渲染成只有一个位置
	// 参数的 "put /k"，ParseCommand 再读回来变成"put requires key and value"，round
	// trip 直接断裂。其余 op 的 Value 恒为空字符串，这条件对它们等价于原来的
	// req.Value != ""，行为不变。
	if req.Value != "" || req.Op == "put" {
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
		if ttl, ok := ttlFromArgs(req.Args); ok && ttl != 0 {
			parts = append(parts, "--ttl="+strconv.FormatInt(ttl, 10))
		}
	}
	return strings.Join(parts, " ")
}

// ttlFromArgs 从 Args["ttl"] 里取出 ttl 秒数。ExecRequest 的字段注释写明它"既给 IPC
// 用，也给 Dispatch 用"——不能假定 Args 里一定是 ParseCommand/HandleExecEtcd 内部
// 产出的规范 int64：兼容 int（Go 测试里常见的无类型字面量）与 float64（JSON 解码
// tool 参数的默认数值类型），其余类型按坏数据处理——记警告后跳过，而不是被
// req.Args["ttl"].(int64) 的类型断言默默吃掉、不留任何痕迹。
func ttlFromArgs(args map[string]any) (int64, bool) {
	v, ok := args["ttl"]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		logger.Default().Warn("etcd FormatCommand: Args[\"ttl\"] has unexpected type, dropping",
			zap.String("type", fmt.Sprintf("%T", v)))
		return 0, false
	}
}
