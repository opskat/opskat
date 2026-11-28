package runner

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/skills"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// AssetTypeSkills 返回全部有用法文档的资产类型（skills.Types()——内嵌的 exec 类型 +
// rdp/vnc/oss/local 等 doc-only 类型，见 permission.RegisterHelpDoc，外加已启用扩展在加载期
// 登记的类型）的一行技能描述（skills.Description），用于技能清单（PromptBuilder 与
// ToolGuidance）。
//
// 无条件全量返回，不看 openTabs：这份清单是**发现**用的，让模型知道 help 存在、以及
// exec 覆盖了哪些类型。doc-only 类型（有 help 无 exec）也混在这同一份返回值里——
// 拆成"exec 真的覆盖"与"仅有配置文档"两段是 buildAssetTypeSkills 的职责（按
// permission.ExecutorFor 拆分），本函数只负责提供全量的 类型→一行描述 映射。
//
// 它**不**满足 exec 的门禁——门禁只认模型显式调用过 help（见 tool.DocGate 的注释）。
// 扩展提供的类型在加载期经 internal/extreg 登记进同一张 skills 表，因此和内置类型一样
// 只在这里露一行、正文靠 help(asset) 取。
func AssetTypeSkills() map[string]string {
	out := make(map[string]string)
	for _, assetType := range skills.Types() {
		if desc, ok := skills.Description(assetType); ok {
			out[assetType] = desc
		}
	}
	return out
}

// DefinedCustomTypes returns slug+name pairs for every currently defined custom type —
// the "Defined custom types" discovery section of the skill list (spec "帮助、技能与门禁").
// Like AssetTypeSkills it is a pure discovery aid, not a doc-gate signal.
//
// A listing failure degrades to an empty (nil) result instead of failing the whole
// prompt build: the custom-type list is a convenience for the model to spot slugs it
// has never seen an asset of, not a required part of the prompt.
func DefinedCustomTypes(ctx context.Context) []CustomTypeSummary {
	types, err := custom_type_svc.CustomType().List(ctx)
	if err != nil {
		logger.Ctx(ctx).Warn("list custom types for system prompt", zap.Error(err))
		return nil
	}
	out := make([]CustomTypeSummary, 0, len(types))
	for _, ct := range types {
		out = append(out, CustomTypeSummary{Slug: ct.Slug, Name: ct.Name})
	}
	return out
}
