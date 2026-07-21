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

// opRequiresKey 是"key 位置参数必需"的 op 集合，ParseCommand 与 FormatCommand 共用：
// 前者对它们在缺少位置参数时报错，后者对它们无条件输出 key（空 key 渲染为一对单引号
// 包起来的空词）。
// 两边共用一份定义，round trip 才不会因为一侧改了判断而漂移。
var opRequiresKey = map[string]bool{"get": true, "put": true, "del": true}

// ParseCommand 解析 etcd 命令串,是 FormatCommand 的逆函数
// （TestParseFormat_RoundTrip 锁住这条性质）。生产调用方是 internal/ai/helper/etcd_exec.go
// 的 ExecEtcdOnAsset / CanonicalizeEtcdCommand（统一 exec 工具的 etcd 执行器与规范化钩子）。
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

	// opRequiresKey 的 op 必须给出 key 位置参数，而不是像 endpoint_status/endpoint_health
	// 那样把 key 当可选：一个形如 "--prefix" 的 key（FormatCommand 对它不加引号，因为
	// "-" 本身在 safeUnquotedWord 允许表里）在这里如果被当成可选参数放过，会与"没给
	// key、只给了 --prefix flag"这个完全不同的请求渲染成同一个字符串——对 del 来说，
	// 后者是"用空串前缀删除整个 key 空间"，是本任务范围内能做的最安全选择：拒绝到底，
	// 把静默的灾难性误解析变成一次响亮的拒绝，而不是猜测意图。
	// 注意这里要求的是"位置参数存在"，不是"key 非空"：显式空词 ''（FormatCommand 对
	// 空 key 的渲染）是一个合法的位置参数，读回来就是空 key。
	if opRequiresKey[op] && len(positional) == 0 {
		if bad := dashPrefixedRestToken(rest); bad != "" {
			return nil, fmt.Errorf("%s requires a key; %q was parsed as a flag, not a key — etcd keys starting with \"-\" are not addressable through this command syntax", op, bad)
		}
		return nil, fmt.Errorf("%s requires a key", op)
	}
	switch op {
	case "get", "del":
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
	case "lease_grant":
		// 与 put/get/del 的位置参数校验同一原则：lease_grant/lease_revoke 缺少必需参数
		// 时必然在 dispatch 阶段失败（ops.go 的 dispatchLeaseGrant/dispatchLeaseRevoke
		// 各有一份等价检查，是 IPC 路径——它直接构造 ExecRequest,不经过 ParseCommand——
		// 的边界防线,继续保留)。这里提前拒绝是为了让 CanonicalizeEtcdCommand（统一 exec
		// 工具的规范化钩子）在权限检查、审批弹窗之前就能识别"语法合法但注定失败"的命令,
		// 不然模型会先被弹一次审批,批准后命令才因为这个检查失败。
		if ttl, ok := ttlFromArgs(req.Args); !ok || ttl <= 0 {
			return nil, errors.New("lease_grant requires positive ttl")
		}
	case "lease_revoke":
		if req.LeaseID == 0 {
			return nil, errors.New("lease_revoke requires lease id")
		}
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
	// get/del/put 恒定需要 key 这个位置参数——etcd 允许空 key（`etcdctl get "" --prefix`
	// 就是列出整个 key 空间），所以 key 不能用"非空才输出"当判断：那样
	// {Op:"get", Prefix:true} 会渲染成 "get --prefix"，ParseCommand 再读回来变成
	// "get requires a key"，round trip 直接断裂。空串经 QuoteIfNeeded 变成显式空词
	// ''，位置参数仍在，"del --prefix"（真的没给 key）继续被拒。
	// endpoint_status/endpoint_health 的 key 是可选的，不在这个集合里。
	if req.Key != "" || opRequiresKey[req.Op] {
		parts = append(parts, cmdline.QuoteIfNeeded(req.Key))
	}
	// put 恒定需要两个位置参数（key、value）——etcd 允许空值，所以 value 同理不能用
	// "非空才输出"当判断。其余 op 的 Value 恒为空字符串，这条件对它们等价于原来的
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
	if ttl, ok := ttlFromArgs(req.Args); ok && ttl != 0 {
		parts = append(parts, "--ttl="+strconv.FormatInt(ttl, 10))
	}
	return strings.Join(parts, " ")
}

// ttlFromArgs 是 Args["ttl"] 的唯一读取入口：FormatCommand（渲染给审批对话框与审计
// 日志看的命令文本）与 dispatchLeaseGrant（真正执行的那一侧）都只经过它。
//
// 两侧各自做类型断言是上一轮的缺陷：FormatCommand 认 float64、执行侧只认 int64，
// 于是一个 JSON 解码来的 ttl 会让用户批准 "--ttl=3600"、实际执行 ttl=0——正是
// "批准的文本 ≠ 执行的动作"这类分叉。ExecRequest 的字段注释写明 Args"既给 IPC 用，
// 也给 Dispatch 用"：Wails IPC 的 JSON 解码给的是 float64，ParseCommand/
// HandleExecEtcd 给的是 int64，Go 调用方手写字面量给的是 int，三者都是真实来源，
// 都要认；其余类型按坏数据处理——记警告后跳过，而不是被类型断言默默吃掉、不留
// 任何痕迹。
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
