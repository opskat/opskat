package audit_repo

import (
	"context"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/audit_entity"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
)

// SessionInfo 会话摘要信息
type SessionInfo struct {
	SessionID string `json:"session_id"`
	FirstTime int64  `json:"first_time"`
	LastTime  int64  `json:"last_time"`
	Count     int64  `json:"count"`
}

// AuditRepo 审计日志数据访问接口
type AuditRepo interface {
	Create(ctx context.Context, log *audit_entity.AuditLog) error
	List(ctx context.Context, opts ListOptions) ([]*audit_entity.AuditLog, int64, error)
	ListSessions(ctx context.Context, startTime int64) ([]SessionInfo, error)
	// Facets 返回时间范围内实际出现过的工具名与决策来源，供筛选项使用
	Facets(ctx context.Context, startTime, endTime int64) (*Facets, error)
}

// ListOptions 列表查询选项；零值字段表示不按该维度过滤
type ListOptions struct {
	Source         string
	ToolName       string
	AssetIDs       []int64 // 任一资产命中即可
	ConversationID int64
	SessionID      string
	// Keyword 对命令与资产名做不区分大小写的子串匹配，% _ 等按字面匹配
	Keyword  string
	Success  *bool
	Decision string // "allow" | "deny"
	// DecisionSources 命中其中任一决策来源；可含空串（表示无决策来源）
	DecisionSources []string
	StartTime       int64 // unix timestamp, 0 表示不限制
	EndTime         int64 // unix timestamp, 0 表示不限制
	Offset          int
	Limit           int
}

// Facets 筛选项的候选值
type Facets struct {
	ToolNames       []string // 按出现次数降序
	DecisionSources []string
}

var defaultAudit AuditRepo

// Audit 获取 AuditRepo 实例
func Audit() AuditRepo {
	return defaultAudit
}

// RegisterAudit 注册 AuditRepo 实现
func RegisterAudit(i AuditRepo) {
	defaultAudit = i
}

// auditRepo 默认实现
type auditRepo struct{}

// NewAudit 创建默认实现
func NewAudit() AuditRepo {
	return &auditRepo{}
}

func (r *auditRepo) Create(ctx context.Context, log *audit_entity.AuditLog) error {
	return db.Ctx(ctx).Create(log).Error
}

func (r *auditRepo) List(ctx context.Context, opts ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	var logs []*audit_entity.AuditLog
	var total int64

	query := db.Ctx(ctx).Model(&audit_entity.AuditLog{})
	if opts.Source != "" {
		query = query.Where("source = ?", opts.Source)
	}
	if opts.ToolName != "" {
		query = query.Where("tool_name = ?", opts.ToolName)
	}
	if len(opts.AssetIDs) > 0 {
		query = query.Where("asset_id IN ?", opts.AssetIDs)
	}
	if opts.ConversationID > 0 {
		query = query.Where("conversation_id = ?", opts.ConversationID)
	}
	if opts.SessionID != "" {
		query = query.Where("session_id = ?", opts.SessionID)
	}
	if opts.Keyword != "" {
		like := "%" + escapeLike(opts.Keyword) + "%"
		query = query.Where(`(command LIKE ? ESCAPE '\' OR asset_name LIKE ? ESCAPE '\')`, like, like)
	}
	if opts.Success != nil {
		success := 0
		if *opts.Success {
			success = 1
		}
		query = query.Where("success = ?", success)
	}
	if opts.Decision != "" {
		query = query.Where("decision = ?", opts.Decision)
	}
	if len(opts.DecisionSources) > 0 {
		query = query.Where("COALESCE(decision_source, '') IN ?", opts.DecisionSources)
	}
	query = withTimeRange(query, opts.StartTime, opts.EndTime)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("id DESC").Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

func (r *auditRepo) ListSessions(ctx context.Context, startTime int64) ([]SessionInfo, error) {
	var sessions []SessionInfo
	query := db.Ctx(ctx).Model(&audit_entity.AuditLog{}).
		Select("session_id, MIN(createtime) as first_time, MAX(createtime) as last_time, COUNT(*) as count").
		Where("session_id != ''")
	if startTime > 0 {
		query = query.Where("createtime >= ?", startTime)
	}
	if err := query.Group("session_id").Order("last_time DESC").Find(&sessions).Error; err != nil {
		return nil, err
	}
	return sessions, nil
}

// Facets 在时间范围内对工具名与决策来源去重。
func (r *auditRepo) Facets(ctx context.Context, startTime, endTime int64) (*Facets, error) {
	f := &Facets{}
	tools := withTimeRange(db.Ctx(ctx).Model(&audit_entity.AuditLog{}), startTime, endTime).
		Select("tool_name").Where("tool_name != ''").
		Group("tool_name").Order("COUNT(*) DESC, tool_name")
	if err := tools.Pluck("tool_name", &f.ToolNames).Error; err != nil {
		return nil, err
	}
	sources := withTimeRange(db.Ctx(ctx).Model(&audit_entity.AuditLog{}), startTime, endTime).
		Distinct("COALESCE(decision_source, '')")
	if err := sources.Pluck("COALESCE(decision_source, '')", &f.DecisionSources).Error; err != nil {
		return nil, err
	}
	return f, nil
}

func withTimeRange(query *gorm.DB, startTime, endTime int64) *gorm.DB {
	if startTime > 0 {
		query = query.Where("createtime >= ?", startTime)
	}
	if endTime > 0 {
		query = query.Where("createtime <= ?", endTime)
	}
	return query
}

// escapeLike 转义 LIKE 通配符，配合 ESCAPE '\' 让关键字按字面匹配。
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
