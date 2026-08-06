package ssh_agent_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/sshagent"
	"go.uber.org/zap"
)

// IdentitySummary 是有界身份摘要：规范指纹、密钥类型、清理后的备注，以及当前
// 资产使用数量。它绝不携带公钥 blob。
type IdentitySummary struct {
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`
	Comment     string `json:"comment,omitempty"`
	Usages      int64  `json:"usages"`
}

// InspectResult 是检查一个已保存来源返回的有界结果。
type InspectResult struct {
	SourceID   int64             `json:"source_id"`
	Usages     int64             `json:"usages"`
	Identities []IdentitySummary `json:"identities"`
}

// inspectIdentities 打开传输、列出并校验身份、返回前关闭传输。ListIdentities 在
// 错误路径已自行关闭传输；成功路径由这里显式关闭。后端身份缓存不跨操作存活。
func inspectIdentities(ctx context.Context, src sshagent.Source) ([]sshagent.Identity, error) {
	ag, err := sshagent.Open(ctx, src)
	if err != nil {
		return nil, err
	}
	ids, err := ag.ListIdentities(ctx)
	if err != nil {
		return nil, err
	}
	if err := ag.Close(); err != nil {
		logger.Ctx(ctx).Warn("close ssh agent transport after inspect", zap.Error(err))
	}
	return ids, nil
}

// Inspect 检查已保存来源：打开→列出→校验→关闭，返回有界身份摘要与来源的使用数。
func Inspect(ctx context.Context, id int64) (*InspectResult, error) {
	src, err := sourceOrErr(ctx, id)
	if err != nil {
		return nil, err
	}
	ids, err := inspectIdentities(ctx, sshagent.Source{Type: sshagent.EndpointType(src.EndpointType), Value: src.Endpoint})
	if err != nil {
		// 可达但无身份是合法观察态，不是错误（与 Probe 返回 ProbeEmpty 一致）：
		// 按空身份继续，使用数仍按引用资产数计算。其余类型化错误如实上报。
		if code, ok := sshagent.CodeOf(err); !ok || code != sshagent.CodeEmpty {
			return nil, err
		}
		ids = nil
	}
	usages, err := asset_repo.Asset().CountAgentAuthBySourceID(ctx, id)
	if err != nil {
		return nil, err
	}
	result := &InspectResult{
		SourceID:   id,
		Usages:     usages,
		Identities: make([]IdentitySummary, 0, len(ids)),
	}
	for _, ident := range ids {
		result.Identities = append(result.Identities, IdentitySummary{
			Fingerprint: ident.Fingerprint,
			Type:        ident.Type,
			Comment:     ident.Comment,
			Usages:      usages,
		})
	}
	return result, nil
}

// ProbeStatus 是端点运行状态。
type ProbeStatus string

const (
	// ProbeOK 可用且包含身份。
	ProbeOK ProbeStatus = "ok"
	// ProbeEmpty 可用但为空。
	ProbeEmpty ProbeStatus = "empty"
	// ProbeUnavailable 不可用（无法打开或协议/载荷错误）。
	ProbeUnavailable ProbeStatus = "unavailable"
	// ProbeUnsupported 当前平台不支持该端点类型。
	ProbeUnsupported ProbeStatus = "unsupported"
)

// ProbeResult 是探测候选或已保存来源的结果，只含运行状态与身份数，不持久化、
// 不返回身份内容。
type ProbeResult struct {
	Status        ProbeStatus `json:"status"`
	IdentityCount int         `json:"identity_count,omitempty"`
}

// Probe 打开给定端点，列出并校验身份，返回前关闭传输。用于发现候选项与已保存
// 来源的运行状态判断。
func Probe(ctx context.Context, endpointType, endpoint string) (ProbeResult, error) {
	src := sshagent.Source{Type: sshagent.EndpointType(endpointType), Value: endpoint}
	ids, err := inspectIdentities(ctx, src)
	if err != nil {
		if code, ok := sshagent.CodeOf(err); ok {
			switch code {
			case sshagent.CodePlatformUnsupported:
				return ProbeResult{Status: ProbeUnsupported}, nil
			case sshagent.CodeEmpty:
				return ProbeResult{Status: ProbeEmpty}, nil
			case sshagent.CodeEnvUnset, sshagent.CodeEndpointUnavailable,
				sshagent.CodeProtocolError, sshagent.CodePayloadInvalid, sshagent.CodeCancelled:
				return ProbeResult{Status: ProbeUnavailable}, nil
			}
		}
		return ProbeResult{}, err
	}
	return ProbeResult{Status: ProbeOK, IdentityCount: len(ids)}, nil
}

// CopyPublicKey 按来源 ID 和指纹重新读取来源、验证公钥 blob，返回不含 Agent
// 备注的 ssh.MarshalAuthorizedKey(publicKey) 行。公钥响应不进入模块级长期状态。
func CopyPublicKey(ctx context.Context, id int64, fingerprint string) (string, error) {
	src, err := sourceOrErr(ctx, id)
	if err != nil {
		return "", err
	}
	if fingerprint == "" {
		return "", newSvcError("ssh_agent_identity_missing", "fingerprint must not be empty")
	}
	return sshagent.CopyPublicKey(ctx, sshagent.Source{Type: sshagent.EndpointType(src.EndpointType), Value: src.Endpoint}, fingerprint)
}
