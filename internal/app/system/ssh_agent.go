package system

// SSH Agent 来源的 Wails IPC 边界（任务 7）。
//
// 按使用方划分的操作，而不是一个包含全部信息的巨大来源对象；绑定保持薄：全部
// 委托给 internal/service/ssh_agent_svc（来源服务）与 internal/sshagent（传输 +
// 精确签名器选择）。本文件不导入仓库 / db（internal/app 的 archtest 约束）。
//
// 隐私规则：
//   - 完整端点只出现在来源编辑与探测界面：ListAgentSources 返回摘要（不含端点），
//     GetAgentSource / ProbeAgentSource / ProbeSavedAgentSource 才接触端点；
//   - 资产详情（GetAgentAssetDetail）不暴露端点与公钥；
//   - 本文件不持有任何模块级状态，也不保留签名、私钥或挑战答案超过请求边界。

import (
	"crypto/rand"
	"strings"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/service/ssh_agent_svc"
	"github.com/opskat/opskat/internal/sshagent"
)

// AgentSourceSummary 是来源列表的摘要：名称、类型与描述，不含完整端点。来源行
// 的运行时状态由前端按需 Probe 获得，不随列表携带。
type AgentSourceSummary struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	EndpointType string `json:"endpoint_type"`
	Description  string `json:"description,omitempty"`
}

// AgentAssetDetail 是为资产详情读取所选 Agent 信息的有界视图：来源名、已存指纹与
// 当前可用性；仅当所选身份当前可用时携带密钥类型与清理后的备注。绝不携带端点或
// 公钥（隐私规则：资产详情不暴露端点路径）。
type AgentAssetDetail struct {
	SourceID     int64  `json:"source_id"`
	SourceName   string `json:"source_name"`
	Fingerprint  string `json:"fingerprint"`
	Availability string `json:"availability"` // ok | empty | unavailable | unsupported | missing
	Type         string `json:"type,omitempty"`
	Comment      string `json:"comment,omitempty"`
}

// ListAgentSources 列出来源摘要（不含完整端点）。
func (s *System) ListAgentSources() ([]AgentSourceSummary, error) {
	ctx := i18n.Ctx(s.ctx, s.Lang())
	sources, err := ssh_agent_svc.List(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]AgentSourceSummary, 0, len(sources))
	for _, src := range sources {
		summaries = append(summaries, AgentSourceSummary{
			ID:           src.ID,
			Name:         src.Name,
			EndpointType: src.EndpointType,
			Description:  src.Description,
		})
	}
	return summaries, nil
}

// GetAgentSource 读取单个来源定义（完整端点，仅供来源编辑 / 探测界面使用）。
func (s *System) GetAgentSource(id int64) (*ssh_agent_source_entity.SSHAgentSource, error) {
	return ssh_agent_svc.Get(i18n.Ctx(s.ctx, s.Lang()), id)
}

// CreateAgentSource 创建来源。只做结构校验后持久化，不要求探测成功。
func (s *System) CreateAgentSource(in ssh_agent_svc.SourceInput) (*ssh_agent_source_entity.SSHAgentSource, error) {
	return ssh_agent_svc.Create(i18n.Ctx(s.ctx, s.Lang()), in)
}

// UpdateAgentSource 更新来源（端点变更触发连接失效回调）。
func (s *System) UpdateAgentSource(id int64, in ssh_agent_svc.SourceInput) (*ssh_agent_source_entity.SSHAgentSource, error) {
	return ssh_agent_svc.Update(i18n.Ctx(s.ctx, s.Lang()), id, in)
}

// DeleteAgentSource 删除来源；被活动 Agent 认证资产引用时返回 ssh_agent_source_in_use。
func (s *System) DeleteAgentSource(id int64) error {
	return ssh_agent_svc.Delete(i18n.Ctx(s.ctx, s.Lang()), id)
}

// ProbeAgentSource 探测候选或显式端点，返回运行状态与身份数，不持久化。
func (s *System) ProbeAgentSource(endpointType, endpoint string) (ssh_agent_svc.ProbeResult, error) {
	return ssh_agent_svc.Probe(i18n.Ctx(s.ctx, s.Lang()), endpointType, endpoint)
}

// ProbeSavedAgentSource 按 ID 探测已保存来源，端点只在探测内部解析，不随结果返回。
func (s *System) ProbeSavedAgentSource(id int64) (ssh_agent_svc.ProbeResult, error) {
	ctx := i18n.Ctx(s.ctx, s.Lang())
	src, err := ssh_agent_svc.Get(ctx, id)
	if err != nil {
		return ssh_agent_svc.ProbeResult{}, err
	}
	return ssh_agent_svc.Probe(ctx, src.EndpointType, src.Endpoint)
}

// InspectAgentSource 检查已保存来源，返回有界身份摘要与使用数。
func (s *System) InspectAgentSource(id int64) (*ssh_agent_svc.InspectResult, error) {
	return ssh_agent_svc.Inspect(i18n.Ctx(s.ctx, s.Lang()), id)
}

// TestAgentSourceFingerprint 测试指定来源与指纹组合：按精确指纹选一签名器并真实
// 签名一次（经 VerifySigner 本地验签）。这是 ssh_svc Agent 认证工厂同一签名器选择
// 语义的签名可用性验证；来源没有目标主机，因此不做完整 SSH 握手。请求结束即关闭
// 传输，签名只存活于本次请求内存。
func (s *System) TestAgentSourceFingerprint(sourceID int64, fingerprint string) error {
	ctx := i18n.Ctx(s.ctx, s.Lang())
	src, err := ssh_agent_svc.Get(ctx, sourceID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(fingerprint) == "" {
		return &sshagent.Error{Code: sshagent.CodeIdentityMissing, Message: "fingerprint must not be empty"}
	}
	ag, err := sshagent.Open(ctx, sshagent.Source{Type: sshagent.EndpointType(src.EndpointType), Value: src.Endpoint})
	if err != nil {
		return err
	}
	// AuthMethod 失败时自行关闭传输；成功时保持打开，由这里统一关闭（Close 幂等）。
	defer func() { _ = ag.Close() }()
	aa, err := ag.AuthMethod(ctx, fingerprint)
	if err != nil {
		return err
	}
	if _, err := aa.Signer().Sign(rand.Reader, []byte("opskat-ssh-agent-test")); err != nil {
		return err
	}
	return nil
}

// CopyAgentSourcePublicKey 显式复制指定公钥（不含 Agent 备注），供用户粘贴。
func (s *System) CopyAgentSourcePublicKey(id int64, fingerprint string) (string, error) {
	return ssh_agent_svc.CopyPublicKey(i18n.Ctx(s.ctx, s.Lang()), id, fingerprint)
}

// GetAgentSourceUsage 读取来源使用数（引用该来源的活动 SSH 资产数）。经 Inspect
// 获得；来源不可达时随 Inspect 报错，前端可用 ProbeSavedAgentSource 的运行时状态
// 覆盖展示。
func (s *System) GetAgentSourceUsage(id int64) (int64, error) {
	res, err := ssh_agent_svc.Inspect(i18n.Ctx(s.ctx, s.Lang()), id)
	if err != nil {
		return 0, err
	}
	return res.Usages, nil
}

// GetAgentAssetDetail 为资产详情读取所选 Agent 信息（来源名 / 已存指纹 / 当前可用
// 性 / 可用时的类型与备注）。不暴露端点与公钥。
func (s *System) GetAgentAssetDetail(sourceID int64, fingerprint string) (AgentAssetDetail, error) {
	ctx := i18n.Ctx(s.ctx, s.Lang())
	src, err := ssh_agent_svc.Get(ctx, sourceID)
	if err != nil {
		return AgentAssetDetail{}, err
	}
	detail := AgentAssetDetail{
		SourceID:    sourceID,
		SourceName:  src.Name,
		Fingerprint: fingerprint,
	}
	res, err := ssh_agent_svc.Inspect(ctx, sourceID)
	if err != nil {
		// 类型化传输错误映射为运行状态；真正的内部错误（如 DB 故障）如实上报。
		code, ok := ssh_agent_svc.CodeOf(err)
		if !ok {
			return AgentAssetDetail{}, err
		}
		detail.Availability = agentAvailabilityOfCode(code)
		return detail, nil
	}
	for _, ident := range res.Identities {
		if ident.Fingerprint == fingerprint {
			detail.Availability = "ok"
			detail.Type = ident.Type
			detail.Comment = ident.Comment
			return detail, nil
		}
	}
	// 指纹不在当前身份中：来源可达但为空 → empty；持有身份但无匹配 → missing。
	if len(res.Identities) == 0 {
		detail.Availability = "empty"
	} else {
		detail.Availability = "missing"
	}
	return detail, nil
}

// agentAvailabilityOfCode 把类型化 Agent 错误码映射为资产详情的运行状态：仅平台
// 不支持区分 unsupported，其余一律 unavailable。详情是展示视图，不向上抛传输级错误。
// CodeEmpty 不在此出现：Inspect 已把可达但为空归一为空身份结果（非错误），由调用方
// 的成功路径区分 empty 与 missing。
func agentAvailabilityOfCode(code string) string {
	if code == sshagent.CodePlatformUnsupported {
		return "unsupported"
	}
	return "unavailable"
}
