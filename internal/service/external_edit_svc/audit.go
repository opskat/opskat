package external_edit_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/pkg/jsonscalar"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"go.uber.org/zap"
)

func (s *Service) writeAudit(session *Session, toolName string, success bool, request any, result any, actionErr error) {
	repo := s.auditRepo
	if repo == nil {
		repo = audit_repo.Audit()
	}
	if repo == nil || session == nil {
		return
	}

	errText := ""
	if actionErr != nil {
		errText = actionErr.Error()
	}

	entry := &audit_entity.AuditLog{
		Source:     "desktop",
		ToolName:   toolName,
		AssetID:    session.AssetID,
		AssetName:  session.AssetName,
		Command:    session.RemotePath,
		Request:    marshalAuditPayload(request, 4096),
		Result:     marshalAuditPayload(result, 8192),
		Error:      truncateText(errText, 2048),
		Success:    boolToSuccess(success),
		SessionID:  session.ID,
		Createtime: s.now().Unix(),
	}
	// 投影之后的 request/result/command/error 按原值逐字落库：字段白名单由下方
	// sanitizeAuditPayload 的 producer DTO 承担，不再做任何值替换或 JSON 重编码。
	// desktop 审计既要给 QA/SEC 还原状态机，又不能把本地工作区路径、编辑器安装路径等敏感环境信息带进数据库。
	if err := repo.Create(context.Background(), entry); err != nil {
		logger.Default().Warn("write external edit audit log", zap.Error(err))
	}
}

// recordAutoSaveAudit 对自动保存成功采样：
// 每 autoSaveAuditWindow 窗口只写一条汇总记录（含窗口内成功次数），
// 失败由调用方直接走 writeAudit 写详细记录，不经过此函数。
func (s *Service) recordAutoSaveAudit(session *Session, toolName string, result *SaveResult) {
	if session == nil {
		return
	}
	key := session.DocumentKey
	now := s.now().Unix()

	s.mu.Lock()
	c := s.autoSaveCounters[key]
	if c == nil {
		c = &autoSaveCounter{}
		s.autoSaveCounters[key] = c
	}
	c.count++
	windowExpired := now-c.lastAt >= int64(autoSaveAuditWindow.Seconds())
	count := c.count
	if windowExpired {
		c.count = 0
		c.lastAt = now
	}
	s.mu.Unlock()

	if !windowExpired {
		return
	}
	s.writeAudit(session, toolName, true,
		map[string]any{"auto": true, "windowSaves": count},
		result, nil)
}

func marshalAuditPayload(payload any, limit int) string {
	if payload == nil {
		return ""
	}
	data, err := json.Marshal(sanitizeAuditPayload(payload))
	if err != nil {
		return ""
	}
	return truncateText(string(data), limit)
}

func sanitizeAuditPayload(payload any) any {
	// 审计字段白名单收敛在统一入口，而不是调用方各自删字段：
	// 新增审计场景时不会因为忘记省略本地路径/哈希而把环境细节写入库表。
	// 白名单只负责省略字段，保留下来的投影值按原样序列化，不再做值替换。
	switch value := payload.(type) {
	case nil:
		return nil
	case OpenRequest:
		return sanitizeAuditOpenRequest(value)
	case *OpenRequest:
		if value == nil {
			return nil
		}
		return sanitizeAuditOpenRequest(*value)
	case SaveResult:
		return sanitizeAuditSaveResult(&value)
	case *SaveResult:
		return sanitizeAuditSaveResult(value)
	case Session:
		return sanitizeAuditSession(&value)
	case *Session:
		return sanitizeAuditSession(value)
	case map[string]any:
		return sanitizeAuditMap(value)
	default:
		// fail-closed：白名单之外的顶层类型（切片/数组、具体类型 map、结构体/指针、裸标量）
		// 一律整体省略，不原样透传，避免复合值里藏本地路径/凭据绕过字段白名单。
		return nil
	}
}

func sanitizeAuditOpenRequest(req OpenRequest) map[string]any {
	return map[string]any{
		"assetId":    req.AssetID,
		"remotePath": req.RemotePath,
		"editorId":   req.EditorID,
	}
}

func sanitizeAuditSaveResult(result *SaveResult) *auditSaveResultPayload {
	if result == nil {
		return nil
	}
	return &auditSaveResultPayload{
		Status:  result.Status,
		Message: result.Message,
		Session: sanitizeAuditSession(result.Session),
	}
}

func sanitizeAuditSession(session *Session) *auditSessionPayload {
	if session == nil {
		return nil
	}
	return &auditSessionPayload{
		ID:                    session.ID,
		AssetID:               session.AssetID,
		AssetName:             session.AssetName,
		DocumentKey:           session.DocumentKey,
		RemotePath:            session.RemotePath,
		RemoteRealPath:        session.RemoteRealPath,
		EditorID:              session.EditorID,
		EditorName:            session.EditorName,
		OriginalSize:          session.OriginalSize,
		OriginalModTime:       session.OriginalModTime,
		OriginalEncoding:      session.OriginalEncoding,
		OriginalBOM:           session.OriginalBOM,
		Dirty:                 session.Dirty,
		State:                 session.State,
		RecordState:           session.RecordState,
		SaveMode:              session.SaveMode,
		Hidden:                session.Hidden,
		Expired:               session.Expired,
		SourceSessionID:       session.SourceSessionID,
		SupersededBySessionID: session.SupersededBySessionID,
		CreatedAt:             session.CreatedAt,
		UpdatedAt:             session.UpdatedAt,
		LastLaunchedAt:        session.LastLaunchedAt,
		LastSyncedAt:          session.LastSyncedAt,
	}
}

// auditMapAllowedFields 是 external-edit 自由 map metadata 的 fail-closed 字段白名单：
// 只有这 11 个批准字段能进入审计，未知字段（含本地路径/哈希/样本与 bakeupPath）一律省略。
// 这是 external_edit_svc 自己的 producer 契约，不引入通用敏感字段注册表；允许键的值只能是
// JSON 标量（string/bool/数字/nil，含 json.Number 与命名标量别名）按原样序列化，复合值整体
// 省略，不做递归放行；非有限 float（NaN/±Inf）与非法 json.Number 也逐键省略，避免让整个
// payload 的 json.Marshal 失败。
var auditMapAllowedFields = map[string]struct{}{
	"auto":         {},
	"windowSaves":  {},
	"rebuild":      {},
	"resolution":   {},
	"status":       {},
	"remoteBytes":  {},
	"remoteSha256": {},
	"bytes":        {},
	"documentKey":  {},
	"readOnly":     {},
	"reuse":        {},
}

func sanitizeAuditMap(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	sanitized := make(map[string]any, len(payload))
	for key, value := range payload {
		if _, ok := auditMapAllowedFields[key]; !ok {
			continue
		}
		// 允许键的值只能是 JSON 标量（string/bool/数字/nil，含 json.Number 与命名标量别名），
		// 内容逐字保留；map[string]string、具体类型 map、切片/数组、结构体等复合值一律整体省略，
		// 防止 bakeupPath/password 藏在复合值里绕过字段白名单。非有限 float（NaN/±Inf）与非法
		// json.Number 会破坏整个 payload 的 json.Marshal，同样逐键省略，不让一个坏值清空其余字段。
		if !jsonscalar.IsScalar(value) {
			continue
		}
		sanitized[key] = value
	}
	return sanitized
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}
