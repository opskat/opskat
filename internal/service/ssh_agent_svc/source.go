package ssh_agent_svc

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/sshagent"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SourceInput 是用户显式提交来源对话框的负载。只有显式提交才持久化；发现、探测、
// 检查、连接测试和候选项“添加”都不会写入 ssh_agent_sources。
type SourceInput struct {
	Name         string `json:"name"`
	EndpointType string `json:"endpoint_type"`
	Endpoint     string `json:"endpoint"`
	Description  string `json:"description,omitempty"`
}

// validateInput 只做结构校验，不探测、不检查平台支持：当前平台不支持的端点类型
// 仍可保存（导入保留 unsupported 可见状态）。端点离线也允许保存。
func validateInput(in SourceInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("来源名称不能为空")
	}
	src := sshagent.Source{Type: sshagent.EndpointType(in.EndpointType), Value: in.Endpoint}
	return src.Validate()
}

// sourceOrErr 读取来源，并把“不存在”映射为稳定的 ssh_agent_source_not_found。
func sourceOrErr(ctx context.Context, id int64) (*ssh_agent_source_entity.SSHAgentSource, error) {
	src, err := ssh_agent_source_repo.SSHAgentSource().Find(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, newSvcError(CodeSourceNotFound, "ssh agent source not found")
	}
	if err != nil {
		return nil, err
	}
	return src, nil
}

// Create 持久化一个新来源。结构校验通过即保存，不要求探测成功。
func Create(ctx context.Context, in SourceInput) (*ssh_agent_source_entity.SSHAgentSource, error) {
	if err := validateInput(in); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	src := &ssh_agent_source_entity.SSHAgentSource{
		Name:         strings.TrimSpace(in.Name),
		EndpointType: in.EndpointType,
		Endpoint:     in.Endpoint,
		Description:  in.Description,
		Createtime:   now,
		Updatetime:   now,
	}
	if err := ssh_agent_source_repo.SSHAgentSource().Create(ctx, src); err != nil {
		return nil, err
	}
	logger.Ctx(ctx).Info("ssh agent source created", zap.Int64("sourceID", src.ID), zap.String("endpointType", src.EndpointType))
	return src, nil
}

// Update 更新来源。修改端点类型或端点值属于连接配置变更：通过产品连接失效边界
// （assetconn.InvalidateAsset）使直接引用该来源的 SSH 资产不再获得旧连接。
// 只改名称或描述不触发连接失效。
func Update(ctx context.Context, id int64, in SourceInput) (*ssh_agent_source_entity.SSHAgentSource, error) {
	if err := validateInput(in); err != nil {
		return nil, err
	}
	existing, err := sourceOrErr(ctx, id)
	if err != nil {
		return nil, err
	}
	endpointChanged := existing.EndpointType != in.EndpointType || existing.Endpoint != in.Endpoint

	existing.Name = strings.TrimSpace(in.Name)
	existing.EndpointType = in.EndpointType
	existing.Endpoint = in.Endpoint
	existing.Description = in.Description
	existing.Updatetime = time.Now().Unix()

	if err := ssh_agent_source_repo.SSHAgentSource().Update(ctx, existing); err != nil {
		return nil, err
	}

	if endpointChanged {
		assets, err := asset_repo.Asset().ListAgentAuthBySourceID(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, asset := range assets {
			assetconn.InvalidateAsset(ctx, asset.ID)
		}
		logger.Ctx(ctx).Info("ssh agent source endpoint changed, invalidated referencing assets",
			zap.Int64("sourceID", id), zap.Int("assets", len(assets)))
	}
	return existing, nil
}

// Delete 删除来源。活动 Agent 认证 SSH 资产引用该来源时拒绝删除。
func Delete(ctx context.Context, id int64) error {
	if _, err := sourceOrErr(ctx, id); err != nil {
		return err
	}
	inUse, err := asset_repo.Asset().CountAgentAuthBySourceID(ctx, id)
	if err != nil {
		return err
	}
	if inUse > 0 {
		return newSvcError(CodeSourceInUse, "source is referenced by active agent assets")
	}
	if err := ssh_agent_source_repo.SSHAgentSource().Delete(ctx, id); err != nil {
		return err
	}
	logger.Ctx(ctx).Info("ssh agent source deleted", zap.Int64("sourceID", id))
	return nil
}

// Get 读取单个来源定义（完整端点，仅供来源编辑/探测界面使用）。
func Get(ctx context.Context, id int64) (*ssh_agent_source_entity.SSHAgentSource, error) {
	return sourceOrErr(ctx, id)
}

// RequireSourceExists 校验来源存在，供 SSH 资产保存边界做引用完整性检查：活动
// Agent 认证资产不能引用不存在的来源。缺失返回稳定的 ssh_agent_source_not_found。
func RequireSourceExists(ctx context.Context, id int64) error {
	_, err := sourceOrErr(ctx, id)
	return err
}

// List 列出全部来源。
func List(ctx context.Context) ([]*ssh_agent_source_entity.SSHAgentSource, error) {
	return ssh_agent_source_repo.SSHAgentSource().List(ctx)
}

// Candidate 是发现流程产出的、尚未持久化的端点候选项。
type Candidate struct {
	EndpointType string `json:"endpoint_type"`
	Endpoint     string `json:"endpoint"`
}

// Discover 返回已知且数量有限的候选项：
//   - 当前进程环境中非空的 SSH_AUTH_SOCK；
//   - Windows 默认 OpenSSH pipe \\.\pipe\openssh-ssh-agent。
//
// 发现流程不扫描文件系统。候选项按规范端点身份去重，并排除已保存来源。
func Discover(ctx context.Context) ([]Candidate, error) {
	var out []Candidate
	seen := map[string]bool{}
	add := func(endpointType, endpoint string) {
		key := endpointType + "\x00" + endpoint
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Candidate{EndpointType: endpointType, Endpoint: endpoint})
	}

	if v := os.Getenv("SSH_AUTH_SOCK"); v != "" {
		add(string(sshagent.EndpointTypeEnvironment), "SSH_AUTH_SOCK")
	}
	if runtime.GOOS == "windows" {
		add(string(sshagent.EndpointTypeWindowsNamedPipe), `\\.\pipe\openssh-ssh-agent`)
	}
	if len(out) == 0 {
		return out, nil
	}

	saved, err := ssh_agent_source_repo.SSHAgentSource().List(ctx)
	if err != nil {
		return nil, err
	}
	savedKeys := make(map[string]bool, len(saved))
	for _, s := range saved {
		savedKeys[s.EndpointType+"\x00"+s.Endpoint] = true
	}
	filtered := out[:0]
	for _, c := range out {
		if !savedKeys[c.EndpointType+"\x00"+c.Endpoint] {
			filtered = append(filtered, c)
		}
	}
	return filtered, nil
}
