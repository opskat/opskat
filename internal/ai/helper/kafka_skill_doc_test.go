package helper

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/skills"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestKafkaSkillDoc_FlagReferenceMatchesFlagRules 把 SKILL.md 的 "## Flag reference"
// 一节与 kafkaFlagRules 逐条对齐——**两个方向都对**：文档少写一个 flag，那个参数在
// 统一 exec 下对模型不可发现；文档多写一个，模型会写出被 validateKafkaFlags 拒掉的
// 命令；必填标注错了，模型要么漏参数（批准后失败）要么以为可选项是必填。
//
// 这条测试是评审拍出来的：第一版只有一句人工核对过的散文"以下 flag 未出现在示例里"，
// 而人工核对是按 flag **名**做的，于是 `schema delete --version` 藏了过去——`--version`
// 在 describe 的示例里出现过，名字层面"已覆盖"，但 delete 上它的缺失才是要命的那个
// （不带 --version 的 schema delete 删掉整个 subject）。粒度必须是 (family, verb, flag)，
// 且必须由机器来核，散文清单只会再漂一次。
//
// execimpl 的 TestSkillDocs_DocumentedExamplesAreCanonicalizable 管的是另一半：
// 文档里的**示例命令**能不能过 canonicalize。两者互补——那边不知道"哪些 flag 存在"，
// 这边不知道"示例写得对不对"。
func TestKafkaSkillDoc_FlagReferenceMatchesFlagRules(t *testing.T) {
	body, ok := skills.Get(asset_entity.AssetTypeKafka)
	if !ok {
		t.Fatal("kafka SKILL.md is not embedded")
	}

	documented := parseKafkaFlagReference(t, body)
	if len(documented) == 0 {
		t.Fatal(`no entries parsed from the "## Flag reference" section; each line must read ` +
			"\"- `<family> <verb>`: `--flag`, **`--required-flag`**, …\"")
	}

	// 期望值直接由 kafkaFlagRules 生成，不手抄。无 flag 的 verb 期望"不出现在文档里"，
	// 这样文档给一个空 verb 编出 flag 也会被抓到。
	want := map[string]map[string]bool{}
	for family, verbs := range kafkaFlagRules {
		for verb, spec := range verbs {
			if len(spec.required)+len(spec.optional) == 0 {
				continue
			}
			flags := map[string]bool{}
			for name := range spec.optional {
				flags[name] = false
			}
			for name := range spec.required {
				flags[name] = true
			}
			want[family+" "+verb] = flags
		}
	}

	for _, key := range slices.Sorted(maps.Keys(want)) {
		got, listed := documented[key]
		if !listed {
			t.Errorf("kafka SKILL.md: %q takes flags (%s) but is missing from the flag reference; "+
				"a flag that is not documented is unreachable to the model", key, formatKafkaFlagSet(want[key]))
			continue
		}
		if !maps.Equal(got, want[key]) {
			t.Errorf("kafka SKILL.md: flag reference for %q = %s, want %s (** ** marks required)",
				key, formatKafkaFlagSet(got), formatKafkaFlagSet(want[key]))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(documented)) {
		if _, ok := want[key]; !ok {
			t.Errorf("kafka SKILL.md: flag reference documents %q, which either takes no flags or does not exist; "+
				"validateKafkaFlags would reject every flag shown for it", key)
		}
	}
}

// kafkaFlagReferenceLine 匹配 "## Flag reference" 一节的条目：
// "- `<family> <verb>`: <flag 列表>"。
var kafkaFlagReferenceLine = regexp.MustCompile("^- `([a-z-]+) ([a-z-]+)`: (.+)$")

// kafkaFlagReferenceFlag 匹配列表里的单个 flag，必填的写成 **`--flag`**。
var kafkaFlagReferenceFlag = regexp.MustCompile("(\\*\\*)?`--([a-z0-9-]+)`(\\*\\*)?")

// parseKafkaFlagReference 解析 "## Flag reference" 一节，返回 "<family> <verb>" →
// flag 名 → 是否必填。
func parseKafkaFlagReference(t *testing.T, body string) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	inSection := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.TrimSpace(line) == "## Flag reference"
			continue
		}
		if !inSection {
			continue
		}
		m := kafkaFlagReferenceLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1] + " " + m[2]
		if _, dup := out[key]; dup {
			t.Errorf("kafka SKILL.md: flag reference lists %q twice", key)
		}
		flags := map[string]bool{}
		for _, f := range kafkaFlagReferenceFlag.FindAllStringSubmatch(m[3], -1) {
			flags[f[2]] = f[1] == "**" && f[3] == "**"
		}
		out[key] = flags
	}
	return out
}

func formatKafkaFlagSet(flags map[string]bool) string {
	parts := make([]string, 0, len(flags))
	for _, name := range slices.Sorted(maps.Keys(flags)) {
		if flags[name] {
			parts = append(parts, "**--"+name+"**")
			continue
		}
		parts = append(parts, "--"+name)
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, " "))
}
