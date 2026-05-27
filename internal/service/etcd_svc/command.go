package etcd_svc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
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

// ParseCommand 解析查询面板的字符串命令。
// 不追求 etcdctl 完全兼容,只识别支持的子集:
//
//	<op> [key] [value...] [--flag] [--flag=val]
//
// 复合命令 "member list" / "endpoint status" / "lease grant" 自动归一为下划线形式。
func ParseCommand(s string) (*ExecRequest, error) {
	tokens := strings.Fields(s)
	if len(tokens) == 0 {
		return nil, errors.New("empty command")
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
		default:
			return nil, fmt.Errorf("unknown flag: --%s", name)
		}
	}

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
	return req, nil
}
