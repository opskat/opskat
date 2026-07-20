// Package skills 以 cago skill 格式（frontmatter + Markdown 正文）内嵌各资产类型的
// 用法文档。格式与 plugin/opsctl/skills/opsctl/SKILL.md 一致，不另造一套。
package skills

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

//go:embed */SKILL.md
var files embed.FS

type skill struct {
	description string
	body        string
}

var registry = map[string]skill{}

func init() {
	entries, err := files.ReadDir(".")
	if err != nil {
		panic(fmt.Sprintf("skills: read embedded dir: %v", err))
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := files.ReadFile(path.Join(entry.Name(), "SKILL.md"))
		if err != nil {
			panic(fmt.Sprintf("skills: read %s/SKILL.md: %v", entry.Name(), err))
		}
		desc, body, err := parseFrontmatter(string(raw))
		if err != nil {
			panic(fmt.Sprintf("skills: parse %s/SKILL.md: %v", entry.Name(), err))
		}
		registry[entry.Name()] = skill{description: desc, body: body}
	}
}

// parseFrontmatter 解析 `---` 包裹的 YAML 头，只取需要的 description 字段。
// 不引入 YAML 依赖：格式是我们自己写的，键值对形态固定。
func parseFrontmatter(raw string) (description, body string, err error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, "---\n") {
		return "", "", fmt.Errorf("missing frontmatter opening delimiter")
	}
	rest := raw[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", "", fmt.Errorf("missing frontmatter closing delimiter")
	}
	head := rest[:end]
	body = strings.TrimLeft(rest[end+len("\n---\n"):], "\n")

	for _, line := range strings.Split(head, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "description" {
			continue
		}
		description = strings.Trim(strings.TrimSpace(value), `"`)
	}
	if description == "" {
		return "", "", fmt.Errorf("frontmatter has no description")
	}
	return description, body, nil
}

// Get 返回该资产类型 SKILL.md 的正文（不含 frontmatter）。
func Get(assetType string) (string, bool) {
	s, ok := registry[assetType]
	return s.body, ok
}

// Description 返回 frontmatter 中的一行描述，用于 prompt 里的技能清单。
func Description(assetType string) (string, bool) {
	s, ok := registry[assetType]
	return s.description, ok
}

// Types 返回已内嵌文档的资产类型，已排序。
func Types() []string {
	types := make([]string, 0, len(registry))
	for name := range registry {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}
