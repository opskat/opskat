// Package skills 以 cago skill 格式（frontmatter + Markdown 正文）内嵌各资产类型的
// 用法文档。格式与 plugin/opsctl/skills/opsctl/SKILL.md 一致，不另造一套。
package skills

import (
	"embed"
	"fmt"
	"path"
	"sort"

	"github.com/opskat/opskat/pkg/skillmd"
)

//go:embed */SKILL.md
var files embed.FS

var registry = map[string]skillmd.Skill{}

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
		s, err := skillmd.Parse(string(raw))
		if err != nil {
			panic(fmt.Sprintf("skills: parse %s/SKILL.md: %v", entry.Name(), err))
		}
		registry[entry.Name()] = s
	}
}

// Get 返回该资产类型 SKILL.md 的正文（不含 frontmatter）。
func Get(assetType string) (string, bool) {
	s, ok := registry[assetType]
	return s.Body, ok
}

// Description 返回 frontmatter 中的一行描述，用于 prompt 里的技能清单。
func Description(assetType string) (string, bool) {
	s, ok := registry[assetType]
	return s.Description, ok
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
