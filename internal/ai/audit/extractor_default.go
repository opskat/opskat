package audit

import (
	"strconv"

	"github.com/opskat/opskat/internal/ai/aictx"
)

// 注册工具名 → 命令摘要提取器。每个工具各自注册，不互相借用：exec 曾经靠
// extractor.go 里一句 toolName 别名借用 run_command 的提取器，于是新工具的审计
// 取决于旧工具的注册是否还在——run_command 现已删除，别名与它一起没了，而 exec
// 的提取因为有自己的注册而毫发无损（TestExtractor_ExecDoesNotBorrowAnotherToolsExtractor）。
func init() {
	RegisterResultAssetTool("put_asset")
	RegisterExtractor("exec", func(a map[string]any) string { return aictx.ArgString(a, "command") })
	// "exec"'s args["command"] is the target asset type's own exec DSL, so
	// runner.resolveAssetForAudit's canonicalize step (k8s --context/--namespace
	// injection, etcd/mongo/kafka DSL round-trip, an extension's flag DSL) is
	// meaningful for it. Registration stays an allow-list rather than a
	// `toolName == "exec"` branch — see canonicalizingTools' doc in extractor.go.
	RegisterCanonicalizingTool("exec")
	// cp 是传输面的唯一工具名，两个入口（AI 工具与 opsctl cp）用同一个 src/dst 参数形状。
	// upload_file / download_file 的两条 RegisterToolAlias 随工具一起退役：别名存在的唯一
	// 理由就是把它们归到 cp 这个面上，工具真的叫 cp 之后别名就是纯噪音。
	RegisterExtractor("cp", func(a map[string]any) string {
		return "cp " + aictx.ArgString(a, "src") + " → " + aictx.ArgString(a, "dst")
	})
	RegisterExtractor("request_permission", func(a map[string]any) string {
		v := aictx.ArgString(a, "items")
		if reason := aictx.ArgString(a, "reason"); reason != "" {
			return "grant: " + v + " reason: " + reason
		}
		return "grant: " + v
	})
	RegisterExtractor("delete_asset", func(a map[string]any) string {
		return "delete asset " + aictx.ArgString(a, "asset")
	})
	RegisterExtractor("delete_group", func(a map[string]any) string {
		// id 在 args 里是数字（JSON number → float64），不是 ArgString 认得的字符串——
		// 这里必须走 ArgInt64，否则摘要永远是 "delete group "，id 部分静默丢失。
		s := "delete group " + strconv.FormatInt(aictx.ArgInt64(a, "id"), 10)
		if aictx.ArgBool(a, "delete_assets") {
			s += " (with assets)"
		}
		return s
	})

	// get_group/put_group/delete_group 都用 args["id"] 装分组 id——get_asset 恰好也用
	// args["id"] 装资产 id，同一个键名在不同工具里指两种不同实体。不注册的话，
	// WriteToolCall 的通用兜底会把分组 id 误当资产 id 去查 asset_repo，写出一条指向
	// 无关资产的审计行（Important 4）。见 [RegisterGroupScopedTool] 的文档注释。
	RegisterGroupScopedTool("get_group")
	RegisterGroupScopedTool("put_group")
	RegisterGroupScopedTool("delete_group")
}
