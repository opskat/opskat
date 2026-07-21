package execimpl

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/skills"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// SKILL.md 是**喂给模型**的那份文档：模型照着它写命令。在此之前没有任何测试读过它，
// 于是它可以独立于执行器漂移——helper 侧的 "AcceptsDocumentedCommands" 类测试把命令
// 硬编码在测试里，锁住的是执行器，锁不住文档。Plan B 的 kafka SKILL.md 草稿因此攒下
// 四条执行器一定会拒绝的示例命令（--only-failed 挂错 verb、acl delete 漏必填 --host、
// config-updates 用了会被 encoding/json 静默丢弃的 "key" 字段名）。
//
// 本文件把这条缝焊上：读每个类型的 SKILL.md 正文，抽出示例命令，逐条送进该类型
// **已注册**的 canonicalizer。canonicalizer 从 permission 注册表拿（也就是 handleExec
// 在生产里用的同一份），不在测试里手抄一张类型→函数表——那只会变成第三处真相。

// skillCommandSection 是示例命令所在的小节标题。只扫这一节：Notes 里的行内代码是
// 讲解片段（`--lease` 是十六进制、`get --prefix` 会报错），不是可执行命令。
const skillCommandSection = "## Command syntax"

// skillExampleRe 定义"可执行示例"的形状：`## Command syntax` 一节里、形如
// "- `<命令>`" 的列表项，取**第一个**反引号片段。
//
// 只取第一个是有意的：etcd 那条"整个 keyspace"的示例后面跟了一句括号说明，里面用
// 行内代码举了个**反例**（一条会报错的 get），抽进来会让测试要求它通过。
// 因此约定：一个列表项 = 一条完整可执行命令，多条命令分行写。
var skillExampleRe = regexp.MustCompile("^\\s*[-*]\\s+`([^`]+)`")

// skillPlaceholderRe 挡住占位符写法。约定是**示例命令必须字面可执行、不含占位符**：
// 可选 flag 不写成 `[--flag]`，而是另起一行写出带该 flag 的完整命令；`<name>` 这类
// 占位只允许出现在小节开头的语法模板段落（那不是列表项，抽取时本就不会命中）。
//
// 选"禁止"而不是"抽取时展开"：展开要么组合爆炸（一条 8 个可选 flag 的 browse 有 256
// 种组合），要么只测其中一种、剩下的仍然漂移；而占位符版本对模型也更差——它得自己
// 猜方括号是不是要照抄。
//
// 判定谓词写得窄，只认这两种真实出现过的写法：`[--` 开头的可选 flag（Plan 草稿的写法）
// 与 `<单词>` 形状的占位。JSON 值里的 `[{"partition":0}]` 不会命中前者。
var skillPlaceholderRe = regexp.MustCompile(`\[--|<[A-Za-z][A-Za-z0-9_.-]*>`)

// skillExampleAssetConfig 为 canonicalizer 会读取资产配置的类型准备最小可用配置。
//
// 这**不是**一张"类型→行为"的真相表（那种表禁止手抄，见文件头注释），只是测试夹具：
// 少一条不会静默跳过，而是让该类型的每条示例都带着 canonicalizer 自己的错误
// （"no kubeconfig configured" / "requires a database"）失败，指向该补什么。
var skillExampleAssetConfig = map[string]func(*asset_entity.Asset) error{
	asset_entity.AssetTypeK8s: func(a *asset_entity.Asset) error {
		// canonicalizeK8sCommand 只要求 Kubeconfig 非空（它不解析内容），
		// Context/Namespace 留空以免规范化后的串与文档里的原串出现无关差异。
		return a.SetK8sConfig(&asset_entity.K8sConfig{Kubeconfig: "fake-kubeconfig"})
	},
	asset_entity.AssetTypeMongoDB: func(a *asset_entity.Asset) error {
		// SKILL.md 的多数示例不写 --db，靠资产默认库补齐（resolveMongoCommand）。
		return a.SetMongoDBConfig(&asset_entity.MongoDBConfig{Database: "appdb"})
	},
}

// skillTypesWithoutCanonicalizer 是有 SKILL.md、但没有注册 canonicalizer 的类型：
// 它们的命令是自由文本（shell / SQL / Redis 协议 / 串口控制台），没有可规范化的结构，
// 因此本测试无法校验其示例，只能跳过。
//
// 写成断言而不是 t.Logf：`go test` 默认不打印 Logf，跳过就成了静默跳过——正是本测试
// 要消灭的那种"看起来在测、其实没测"。哪天给这些类型加了 canonicalizer（或反过来
// 有类型的 canonicalizer 被摘掉），这条断言会失败并要求同步这份清单。
var skillTypesWithoutCanonicalizer = []string{
	asset_entity.AssetTypeDatabase,
	asset_entity.AssetTypeRedis,
	asset_entity.AssetTypeSerial,
	asset_entity.AssetTypeSSH,
}

func TestSkillDocs_DocumentedExamplesAreCanonicalizable(t *testing.T) {
	types := skills.Types()
	if len(types) == 0 {
		t.Fatal("skills.Types() is empty; the embedded SKILL.md set went missing")
	}

	var skipped []string
	totalExamples := 0
	for _, assetType := range types {
		body, ok := skills.Get(assetType)
		if !ok {
			t.Fatalf("skills.Get(%q) = not found, but it is listed by skills.Types()", assetType)
		}

		examples := extractSkillExamples(body)
		// 每个类型都必须抽到东西：抽取规则一旦与文档写法错位（改了标题、改成代码块），
		// 静默地什么都不检查是最坏的结果——这条断言把"空转"变成失败。
		if len(examples) == 0 {
			t.Errorf("%s/SKILL.md: extracted 0 executable examples; they must live under %q as list items of the form \"- `<command>`\"",
				assetType, skillCommandSection)
			continue
		}
		totalExamples += len(examples)

		for _, cmd := range examples {
			if m := skillPlaceholderRe.FindString(cmd); m != "" {
				t.Errorf("%s/SKILL.md: example %q contains placeholder %q; examples must be literally executable — write each optional-flag variant out on its own line",
					assetType, cmd, m)
			}
		}

		canonicalize, ok := permission.CanonicalizeFor(assetType)
		if !ok {
			skipped = append(skipped, assetType)
			continue
		}

		asset := skillExampleAsset(t, assetType)
		for _, cmd := range examples {
			if _, err := canonicalize(asset, cmd); err != nil {
				t.Errorf("%s/SKILL.md documents %q, but the registered canonicalizer rejects it: %v",
					assetType, cmd, err)
			}
		}
	}

	if totalExamples == 0 {
		t.Fatal("no examples extracted from any SKILL.md")
	}
	t.Logf("checked %d documented examples across %d asset types", totalExamples, len(types))

	slices.Sort(skipped)
	want := slices.Clone(skillTypesWithoutCanonicalizer)
	slices.Sort(want)
	if !slices.Equal(skipped, want) {
		t.Errorf("types skipped for having no canonicalizer = %v, want %v; update skillTypesWithoutCanonicalizer to match the registry",
			skipped, want)
	}
}

// extractSkillExamples 按 skillExampleRe 的约定抽取示例命令。
// `### ` 子标题不终止小节（kafka 按 family 分了子节），只有下一个 `## ` 才终止。
func extractSkillExamples(body string) []string {
	var examples []string
	inSection := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.TrimSpace(line) == skillCommandSection
			continue
		}
		if !inSection {
			continue
		}
		if m := skillExampleRe.FindStringSubmatch(line); m != nil {
			examples = append(examples, m[1])
		}
	}
	return examples
}

func skillExampleAsset(t *testing.T, assetType string) *asset_entity.Asset {
	t.Helper()
	asset := &asset_entity.Asset{ID: 1, Name: assetType + "-doc", Type: assetType}
	if configure, ok := skillExampleAssetConfig[assetType]; ok {
		if err := configure(asset); err != nil {
			t.Fatalf("configure %s test asset: %v", assetType, err)
		}
	}
	return asset
}
