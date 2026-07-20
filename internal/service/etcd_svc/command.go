package etcd_svc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

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

// ParseCommand 解析 exec 工具传入的 etcd 命令串,是 FormatCommand 的逆函数
// （TestParseFormat_RoundTrip 锁住这条性质）。
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
	return req, nil
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
