package system

import (
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/service/audit_svc"
)

// defaultAuditPageSize 是未指定每页条数时的默认值，与审计页默认页大小一致
const defaultAuditPageSize = 20

// AuditLogListResult 审计日志列表结果
type AuditLogListResult struct {
	Items []*audit_entity.AuditLog `json:"items"`
	Total int64                    `json:"total"`
}

// ListAuditLogs 按条件分页查询审计日志
func (s *System) ListAuditLogs(query audit_svc.Query) (*AuditLogListResult, error) {
	if query.Limit <= 0 {
		query.Limit = defaultAuditPageSize
	}
	items, total, err := audit_svc.List(i18n.Ctx(s.ctx, s.Lang()), query)
	if err != nil {
		return nil, err
	}
	return &AuditLogListResult{Items: items, Total: total}, nil
}

// ListAuditFacets 返回时间范围内出现过的工具与决策方分组，作为筛选项
func (s *System) ListAuditFacets(startTime, endTime int64) (*audit_svc.FacetsResult, error) {
	return audit_svc.Facets(i18n.Ctx(s.ctx, s.Lang()), startTime, endTime)
}

// ListAuditSessionGrants 返回会话的全部「始终允许」授权
func (s *System) ListAuditSessionGrants(sessionID string) ([]audit_svc.SessionGrant, error) {
	return audit_svc.SessionGrants(i18n.Ctx(s.ctx, s.Lang()), sessionID)
}

// ListAuditSessions 查询审计日志中的会话列表
func (s *System) ListAuditSessions(startTime int64) ([]audit_svc.SessionInfo, error) {
	return audit_svc.ListSessions(i18n.Ctx(s.ctx, s.Lang()), startTime)
}
