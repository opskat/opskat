package helper

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// 通用资产（asset_entity.AssetTypeGeneric）只有一个执行器：它按资产所属自定义类型的
// 执行方式（custom_type_entity.ExecMode*）查 genericModes 分派，而不是在共享代码里按
// 类型字符串分支。每种执行方式在自己的文件里 RegisterGenericMode（HTTP 见
// generic_http.go）；尚未登记的执行方式报 errGenericModeUnsupported。

// GenericTarget 是一台已解析的通用资产：类型、连接配置与全部字段的明文值。
// Values 含解密后的密钥，不得写入日志、审计或任何输出。
type GenericTarget struct {
	Asset  *asset_entity.Asset
	Config *asset_entity.GenericConfig
	Type   *custom_type_entity.CustomType
	Values map[string]string
}

// GenericMode 是一种执行方式的实现。command 是 exec 传入的命令串（AI 原样传入；opsctl 把
// argv 逐个加引号拼成，见 permission.StreamExecFunc），argv 是 opsctl 保留边界的参数。
// exec 传入的内容一律按字面处理，模板只在类型配置里渲染（Design decision 7）。
type GenericMode struct {
	// Canonicalize 把命令规范成策略匹配、审批与审计使用的匹配对象，不需要字段值。
	Canonicalize func(command string) (string, error)
	// Describe 生成审批弹窗的展示补充（操作类型与目标），不得含注入值。
	Describe func(ctx context.Context, t *GenericTarget, command string) (string, error)
	// Exec 是 AI exec 的执行入口：output 是返回给模型的完整文本；auditResult 是写进审计
	// result 的摘要（HTTP：状态行；命令：exit N），与 permission.StreamResult.AuditResult
	// 同一约定，不得含注入值——output 可能回显注入值（例如服务把请求头写回响应体），
	// 所以审计绝不存 output。
	Exec func(ctx context.Context, t *GenericTarget, command string) (output, auditResult string, err error)
	// Stream 是 opsctl exec 的流式执行入口，契约见 permission.StreamExecFunc。
	Stream func(ctx context.Context, t *GenericTarget, argv []string, stdio permission.Stdio) (permission.StreamResult, error)
}

var genericModes = make(map[string]GenericMode)

// RegisterGenericMode 登记一种执行方式的实现。只在 init 期调用；重复或不完整的登记 panic。
func RegisterGenericMode(execMode string, m GenericMode) {
	if execMode == "" || m.Canonicalize == nil || m.Describe == nil || m.Exec == nil || m.Stream == nil {
		panic("helper: invalid generic mode registration " + execMode)
	}
	if _, exists := genericModes[execMode]; exists {
		panic("helper: duplicate generic mode registration " + execMode)
	}
	genericModes[execMode] = m
}

func genericModeFor(ct *custom_type_entity.CustomType) (GenericMode, error) {
	m, ok := genericModes[ct.ExecMode]
	if !ok {
		return GenericMode{}, fmt.Errorf("custom type %q uses exec mode %q, which is not supported yet", ct.Slug, ct.ExecMode)
	}
	return m, nil
}

// genericTypeOf 查通用资产所属的自定义类型与它的执行方式实现（不解析字段值）。
func genericTypeOf(ctx context.Context, asset *asset_entity.Asset) (*custom_type_entity.CustomType, GenericMode, error) {
	cfg, err := asset.GetGenericConfig()
	if err != nil {
		return nil, GenericMode{}, err
	}
	ct, err := custom_type_svc.CustomType().GetBySlug(ctx, cfg.CustomType)
	if err != nil {
		return nil, GenericMode{}, err
	}
	m, err := genericModeFor(ct)
	return ct, m, err
}

// resolveGenericTarget 解析字段值（含密钥解密）。解密失败或有必填字段缺值时报错——调用方
// 此时不得发出任何请求、启动任何进程（spec「通用资产」缺值与「HTTP 请求」）。
func resolveGenericTarget(ctx context.Context, asset *asset_entity.Asset) (*GenericTarget, GenericMode, error) {
	resolved, err := custom_type_svc.CustomType().ResolveAsset(ctx, asset)
	if err != nil {
		return nil, GenericMode{}, err
	}
	m, err := genericModeFor(resolved.Type)
	if err != nil {
		return nil, GenericMode{}, err
	}
	if len(resolved.Missing) > 0 {
		return nil, GenericMode{}, fmt.Errorf("asset %q is missing required field(s): %s — fill them in on the asset first",
			asset.Name, strings.Join(resolved.Missing, ", "))
	}
	cfg, err := asset.GetGenericConfig()
	if err != nil {
		return nil, GenericMode{}, err
	}
	return &GenericTarget{Asset: asset, Config: cfg, Type: resolved.Type, Values: resolved.Values}, m, nil
}

// ExecGenericOnAsset 是通用资产的 AI exec 执行入口（permission.ExecFunc）。scope 不适用。
// 返回值是给模型的完整输出；审计只拿执行方式给出的摘要，经 aictx.RecordAuditResult 写进
// 写审计的一方（runner 的 auditMiddleware、AI / opsctl batch 的每个条目）安装的槽。
func ExecGenericOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	t, m, err := resolveGenericTarget(ctx, asset)
	if err != nil {
		return "", err
	}
	output, auditResult, err := m.Exec(ctx, t, command)
	if err != nil {
		return "", err
	}
	aictx.RecordAuditResult(ctx, auditResult)
	return output, nil
}

// StreamGenericOnAsset 是通用资产的 opsctl 流式执行入口（permission.StreamExecFunc）。
func StreamGenericOnAsset(ctx context.Context, asset *asset_entity.Asset, argv []string, stdio permission.Stdio) (permission.StreamResult, error) {
	t, m, err := resolveGenericTarget(ctx, asset)
	if err != nil {
		return permission.StreamResult{}, err
	}
	return m.Stream(ctx, t, argv, stdio)
}

// CanonicalizeGenericCommand 是通用资产的 permission.CanonicalizeFunc。该签名没有 ctx，
// 而执行方式在数据库里的自定义类型上，因此用 context.Background() 查一次类型。
func CanonicalizeGenericCommand(asset *asset_entity.Asset, command string) (string, error) {
	_, m, err := genericTypeOf(context.Background(), asset)
	if err != nil {
		return "", err
	}
	return m.Canonicalize(command)
}

// PrecheckGeneric 是通用资产的 permission.PrecheckFunc：把"执行方式不支持 / 缺值 / 解密
// 失败"这些必然失败的情况挪到审批弹窗之前。
func PrecheckGeneric(ctx context.Context, asset *asset_entity.Asset) error {
	_, _, err := resolveGenericTarget(ctx, asset)
	return err
}

// DescribeGenericCommand 是通用资产的 permission.ApprovalDetailFunc。
func DescribeGenericCommand(ctx context.Context, asset *asset_entity.Asset, command string) (string, error) {
	t, m, err := resolveGenericTarget(ctx, asset)
	if err != nil {
		return "", err
	}
	return m.Describe(ctx, t, command)
}
