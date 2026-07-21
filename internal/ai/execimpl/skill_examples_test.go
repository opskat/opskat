package execimpl

import (
	"fmt"
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
// 五条执行器一定会拒绝的示例命令（--only-failed / --include-tasks 挂错 verb、
// acl delete 漏必填 --host、config-updates 用了会被 encoding/json 静默丢弃的 "key"
// 字段名、message inspect 漏必填 --partition）。
//
// 本文件把这条缝焊上：读每个类型的 SKILL.md 正文，抽出示例命令，逐条送进该类型
// **已注册**的 canonicalizer。canonicalizer 从 permission 注册表拿（也就是 handleExec
// 在生产里用的同一份），不在测试里手抄一张类型→函数表——那只会变成第三处真相。
//
// 抽取按"**默认失败**"设计，这是第一版评审拍出来的关键修正：第一版只认一种写法、
// 认不出的行**静默跳过**，于是围栏代码块、有序列表、`- Example: <cmd>`、同一条列表项
// 里的第二个反引号片段、以及另起一个 `## More examples` 小节，五种写法都能夹带
// 一条执行器会拒绝的命令而测试全绿——同时输出还理直气壮地写着 "checked N examples"。
// 现在的规则是：文档里每一处**看起来像示例**的地方都必须被抽取器认出来，认不出就报错
// 并指名文件、行号与那一行的形状（见 scanSkillDoc）。

// skillCommandSection 是示例命令所在的小节标题。示例只能写在这一节里：Notes 里的
// 行内代码是讲解片段与**反例**，不是可执行命令。
const skillCommandSection = "## Command syntax"

// skillExampleShape 定义唯一被承认的"可执行示例"写法：`## Command syntax` 一节里、
// 整行就是一个列表项 + 一个反引号命令，后面最多再跟一个括号说明。
//
// 括号说明里可以再出现反引号（etcd 那条"整个 keyspace"示例的括号里举了个会报错的
// 反例），但命令本身只取第一个片段——所以一个列表项只能写一条命令。
var skillExampleShape = regexp.MustCompile("^\\s*[-*+]\\s+`([^`]+)`\\s*(\\(.*\\))?\\s*$")

// skillListItem 匹配任意列表项（含有序列表），用来把"不是示例形状的列表项"揪出来。
var skillListItem = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+`)

// skillSpanOnlyLine 匹配"整行只有一个反引号片段"的非列表行。这种行在
// `## Command syntax` 一节里只允许是语法模板（必须含占位符），否则就是一条不带列表
// 标记、因而不会被抽取的示例。
var skillSpanOnlyLine = regexp.MustCompile("^`([^`]+)`$")

// skillPlaceholders 列出被当作"占位符"的写法，以及报错时给人看的名字。
//
// 约定是**示例命令必须字面可执行、不含占位符**：可选 flag 不写成 `[--flag]`，而是
// 另起一行写出带该 flag 的完整命令；`<name>` 这类占位只允许出现在小节开头的语法
// 模板里（模板必须含占位符，正是靠这一点与示例区分）。
//
// 选"禁止"而不是"抽取时展开"：展开要么组合爆炸（一条 8 个可选 flag 的 browse 有 256
// 种组合），要么只测其中一种、剩下的仍然漂移；而占位符版本对模型也更差——它得自己
// 猜方括号是不是要照抄。
//
// 判定按空白切词后逐词做，这样 JSON 值里的 `[{"partition":0}]`（`--records=` 开头的
// 一个词）不会被误判成方括号占位。刻意不去猜 TOPIC_NAME 这类全大写占位：SQL 与
// Redis 的示例里全大写词是真命令（SELECT / HGETALL），猜错的代价比漏判大——所以
// 报错文案只声明这里真正拦下的这几种形状，不吹成"任何占位符"。
var skillPlaceholders = []struct {
	name string
	re   *regexp.Regexp
}{
	{"[bracketed] optional/placeholder token", regexp.MustCompile(`^\[.*\]$`)},
	{"{braced} placeholder token", regexp.MustCompile(`^\{.*\}$`)},
	{"<angled> placeholder", regexp.MustCompile(`<[A-Za-z][A-Za-z0-9_.-]*>`)},
	{"[--flag] optional marker", regexp.MustCompile(`\[--`)},
	{"... elision", regexp.MustCompile(`^\.\.\.$`)},
}

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

		examples, violations := scanSkillDoc(body)
		// 形状违规逐条报出：它们不是"抽不到"，而是"文档里有东西看起来像示例、
		// 抽取器不认识"——静默放过就等于给漂移开了后门。
		for _, v := range violations {
			t.Errorf("%s/SKILL.md (body line %d): %s", assetType, v.line, v.message)
		}
		// 每个类型都必须抽到东西：抽取规则一旦与文档写法整体错位（改了标题），
		// 静默地什么都不检查是最坏的结果——这条断言把"空转"变成失败。
		if len(examples) == 0 {
			t.Errorf("%s/SKILL.md: extracted 0 executable examples; they must live under %q, one per list item, written as \"- `<command>`\"",
				assetType, skillCommandSection)
			continue
		}
		totalExamples += len(examples)

		for _, ex := range examples {
			if name, match := skillPlaceholderIn(ex.command); match != "" {
				t.Errorf("%s/SKILL.md (body line %d): example %q contains %s (%q); examples must be literally executable — write each optional-flag variant out on its own line. "+
					"Note this check only recognizes [bracketed] / {braced} / <angled> tokens and a bare \"...\"; a placeholder spelled as a plain word (TOPIC_NAME) passes it, so the rule is on you, not on the check",
					assetType, ex.line, ex.command, name, match)
			}
		}

		canonicalize, ok := permission.CanonicalizeFor(assetType)
		if !ok {
			skipped = append(skipped, assetType)
			continue
		}

		asset := skillExampleAsset(t, assetType)
		for _, ex := range examples {
			if _, err := canonicalize(asset, ex.command); err != nil {
				t.Errorf("%s/SKILL.md (body line %d) documents %q, but the registered canonicalizer rejects it: %v",
					assetType, ex.line, ex.command, err)
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

type skillExample struct {
	line    int
	command string
}

type skillViolation struct {
	line    int
	message string
}

// scanSkillDoc 扫描整份 SKILL.md 正文，返回可执行示例与"看起来像示例但抽取器不认识"
// 的违规行。行号是**正文**行号（skills.Get 已剥掉 frontmatter），报错时一并带上原文，
// 定位不必靠行号。
//
// 默认失败：只要一行有可能被读者当成示例、又不是被承认的那一种形状，就报违规，
// 而不是跳过。报错文案本身就是写给贡献者看的规则说明——规则只写在注释里等于没写，
// 下一个写 SKILL.md 的人看不到。
//
// 认下面这些为"看起来像示例"：
//   - `## Command syntax` 一节里的任何列表项（含 1. 有序列表、`- Example: <cmd>`
//     这种带前缀的、以及一项里写两条命令的）
//   - 该节里不带列表标记、整行只有一个反引号片段的行（语法模板除外——模板必须含占位符）
//   - 该节**之外**任何恰好长成示例形状的列表项（另起 `## More examples` 塞示例，
//     或把示例混进 Notes，都会被这条挡下）
//   - 任何位置的围栏代码块（``` ）——本抽取器不读围栏块，允许它存在就是允许夹带
func scanSkillDoc(body string) ([]skillExample, []skillViolation) {
	var (
		examples   []skillExample
		violations []skillViolation
		inSection  bool
		inFence    bool
	)
	for i, line := range strings.Split(body, "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				violations = append(violations, skillViolation{lineNo, fmt.Sprintf(
					"fenced code block (%q) is not a recognized shape — this scanner does not read fenced blocks, so any command inside one would never be checked; write each command as a list item \"- `<command>`\" under %q",
					trimmed, skillCommandSection)})
			}
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		if strings.HasPrefix(line, "## ") {
			inSection = trimmed == skillCommandSection
			continue
		}

		switch {
		case skillListItem.MatchString(line):
			m := skillExampleShape.FindStringSubmatch(line)
			switch {
			case inSection && m != nil:
				examples = append(examples, skillExample{lineNo, m[1]})
			case inSection:
				violations = append(violations, skillViolation{lineNo, fmt.Sprintf(
					"unrecognized example shape %q; under %q every list item must be exactly \"- `<command>`\" (one command per item, optionally followed by a parenthetical note)",
					trimmed, skillCommandSection)})
			case m != nil:
				violations = append(violations, skillViolation{lineNo, fmt.Sprintf(
					"example-shaped list item %q outside %q — it would never be checked; move it into that section, or reword it so it reads as prose",
					trimmed, skillCommandSection)})
			}
		case inSection && skillSpanOnlyLine.MatchString(trimmed):
			// 语法模板（`<op> [key] [value]`）与示例的唯一区别就是占位符，
			// 所以不含占位符的裸片段行按"漏写列表标记的示例"处理。
			if _, match := skillPlaceholderIn(trimmed); match == "" {
				violations = append(violations, skillViolation{lineNo, fmt.Sprintf(
					"bare code span %q is not a recognized shape; an example must be a list item (\"- `<command>`\"), while the syntax template must contain a placeholder such as <name>",
					trimmed)})
			}
		}
	}
	return examples, violations
}

func skillPlaceholderIn(command string) (name, match string) {
	for _, field := range strings.Fields(command) {
		for _, p := range skillPlaceholders {
			if m := p.re.FindString(field); m != "" {
				return p.name, m
			}
		}
	}
	return "", ""
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
