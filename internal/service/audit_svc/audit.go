// Package audit_svc 是审计日志的查询服务：把用户层面的筛选条件（决策方分组、成功/失败等）
// 翻译成仓储查询，并提供筛选项候选值与会话授权汇总。桌面端与 opsctl 共用。
package audit_svc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/audit_repo"
)

// 结果筛选值
const (
	ResultSuccess = "success"
	ResultFailed  = "failed"
)

// 可选值，供校验与 CLI 提示
var (
	Sources   = []string{"ai", "opsctl", "desktop"}
	Results   = []string{ResultSuccess, ResultFailed}
	Decisions = []string{"allow", "deny"}
)

// grantSubmitTool 是会话授权变更的审计工具名（见 internal/ai/audit WriteGrantSubmitAudit）
const grantSubmitTool = "grant_submit"

// Query 审计查询条件；零值字段表示不按该维度过滤
type Query struct {
	Keyword   string  `json:"keyword"`
	Source    string  `json:"source"`
	ToolName  string  `json:"tool_name"`
	AssetIDs  []int64 `json:"asset_ids"`
	Result    string  `json:"result"`   // success | failed
	Decision  string  `json:"decision"` // allow | deny
	Decider   string  `json:"decider"`  // aictx.Decider* 分组
	SessionID string  `json:"session_id"`
	StartTime int64   `json:"start_time"`
	EndTime   int64   `json:"end_time"`
	Offset    int     `json:"offset"`
	Limit     int     `json:"limit"`
}

// InvalidFilterError 表示某个筛选字段取了不认识的值
type InvalidFilterError struct {
	Field   string
	Value   string
	Allowed []string
}

func (e *InvalidFilterError) Error() string {
	return fmt.Sprintf("invalid %s %q: must be one of %s", e.Field, e.Value, strings.Join(e.Allowed, ", "))
}

func checkEnum(field, value string, allowed []string) error {
	if value == "" || slices.Contains(allowed, value) {
		return nil
	}
	return &InvalidFilterError{Field: field, Value: value, Allowed: allowed}
}

// options 把查询条件翻译成仓储选项
func (q Query) options() (audit_repo.ListOptions, error) {
	for _, err := range []error{
		checkEnum("source", q.Source, Sources),
		checkEnum("result", q.Result, Results),
		checkEnum("decision", q.Decision, Decisions),
		checkEnum("decider", q.Decider, aictx.DeciderGroups),
	} {
		if err != nil {
			return audit_repo.ListOptions{}, err
		}
	}
	opts := audit_repo.ListOptions{
		Source:    q.Source,
		ToolName:  q.ToolName,
		AssetIDs:  q.AssetIDs,
		SessionID: q.SessionID,
		Keyword:   q.Keyword,
		Decision:  q.Decision,
		StartTime: q.StartTime,
		EndTime:   q.EndTime,
		Offset:    q.Offset,
		Limit:     q.Limit,
	}
	if q.Result != "" {
		success := q.Result == ResultSuccess
		opts.Success = &success
	}
	if q.Decider != "" {
		opts.DecisionSources, _ = aictx.DeciderSources(q.Decider)
	}
	return opts, nil
}

// List 按条件分页查询审计记录，返回当前页与总数
func List(ctx context.Context, q Query) ([]*audit_entity.AuditLog, int64, error) {
	opts, err := q.options()
	if err != nil {
		return nil, 0, err
	}
	return audit_repo.Audit().List(ctx, opts)
}

// FacetsResult 筛选项候选值
type FacetsResult struct {
	Tools    []string `json:"tools"`    // 按出现次数降序
	Deciders []string `json:"deciders"` // 按 aictx.DeciderGroups 顺序
}

// Facets 返回时间范围内出现过的工具与决策方分组；未归组的决策来源不出现
func Facets(ctx context.Context, startTime, endTime int64) (*FacetsResult, error) {
	f, err := audit_repo.Audit().Facets(ctx, startTime, endTime)
	if err != nil {
		return nil, err
	}
	present := make(map[string]bool)
	for _, s := range f.DecisionSources {
		if group, ok := aictx.DeciderOf(s); ok {
			present[group] = true
		}
	}
	res := &FacetsResult{Tools: f.ToolNames, Deciders: []string{}}
	for _, g := range aictx.DeciderGroups {
		if present[g] {
			res.Deciders = append(res.Deciders, g)
		}
	}
	return res, nil
}

// SessionGrant 会话中一次「始终允许」授权
type SessionGrant struct {
	AssetID    int64  `json:"asset_id"`
	AssetName  string `json:"asset_name"`
	Patterns   string `json:"patterns"`
	Createtime int64  `json:"createtime"`
}

// SessionGrants 返回会话的全部授权记录（新到旧），与分页、其它筛选无关
func SessionGrants(ctx context.Context, sessionID string) ([]SessionGrant, error) {
	// 空会话 ID 在仓储里表示「不按会话过滤」，查出来会是所有会话的授权
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	logs, _, err := audit_repo.Audit().List(ctx, audit_repo.ListOptions{SessionID: sessionID, ToolName: grantSubmitTool})
	if err != nil {
		return nil, err
	}
	grants := make([]SessionGrant, 0, len(logs))
	for _, l := range logs {
		if l.Command == "" {
			continue
		}
		grants = append(grants, SessionGrant{AssetID: l.AssetID, AssetName: l.AssetName, Patterns: l.Command, Createtime: l.Createtime})
	}
	return grants, nil
}

// DeciderGroup 决策方分组及其包含的决策来源，前端据此把行内的决策来源归到分组
type DeciderGroup struct {
	ID      string   `json:"id"`
	Sources []string `json:"sources"`
}

// DeciderGroups 按展示顺序返回全部决策方分组
func DeciderGroups() []DeciderGroup {
	groups := make([]DeciderGroup, 0, len(aictx.DeciderGroups))
	for _, id := range aictx.DeciderGroups {
		sources, _ := aictx.DeciderSources(id)
		groups = append(groups, DeciderGroup{ID: id, Sources: sources})
	}
	return groups
}

// SessionInfo 会话摘要
type SessionInfo = audit_repo.SessionInfo

// ListSessions 列出时间范围起点之后出现过的会话
func ListSessions(ctx context.Context, startTime int64) ([]SessionInfo, error) {
	return audit_repo.Audit().ListSessions(ctx, startTime)
}
