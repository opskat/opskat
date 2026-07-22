// Package skillmd 解析 cago skill 格式的 SKILL.md：`---` 包裹的 YAML 头 + Markdown 正文。
// 内置资产类型文档（internal/ai/skills）与扩展文档（pkg/extension）共用本包——
// 此前两侧各有一套：内置的是私有函数，扩展侧压根不解析（裸字符串 + 4 KiB 上限）。
//
// 不引入 YAML 依赖：格式是我们自己写的，键值对形态固定，且严格性本身是特性——
// 解析不了要响亮失败，而不是退化成一段进了 prompt 的原始 frontmatter 噪音。
package skillmd

import (
	"fmt"
	"strings"
)

// Skill 是一份解析后的 SKILL.md。Name 可选（内置侧的目录名、扩展侧的扩展名才是权威
// 标识），Description 必填——它是 prompt 里那份类型清单的唯一内容来源，缺了就等于
// 这份文档对模型不可发现。
type Skill struct {
	Name        string
	Description string
	Body        string
}

// Parse 解析一份 SKILL.md 原始内容。缺 frontmatter 分隔符、frontmatter 未闭合、
// 或缺 description 字段都会返回 error。
func Parse(raw string) (Skill, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, "---\n") {
		return Skill{}, fmt.Errorf("missing frontmatter opening delimiter")
	}
	rest := raw[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return Skill{}, fmt.Errorf("missing frontmatter closing delimiter")
	}
	head := rest[:end]

	s := Skill{Body: strings.TrimLeft(rest[end+len("\n---\n"):], "\n")}
	for _, line := range strings.Split(head, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "name":
			s.Name = value
		case "description":
			s.Description = value
		}
	}
	if s.Description == "" {
		return Skill{}, fmt.Errorf("frontmatter has no description")
	}
	return s, nil
}
