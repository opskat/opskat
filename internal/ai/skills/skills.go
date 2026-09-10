// Package skills 以 cago skill 格式（frontmatter + Markdown 正文）内嵌各资产类型的
// 用法文档。格式与 plugin/opsctl/skills/opsctl/SKILL.md 一致，不另造一套。
package skills

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"sync"

	"github.com/opskat/opskat/pkg/skillmd"
)

//go:embed */SKILL.md
var files embed.FS

var (
	mu       sync.RWMutex
	registry = map[string]skillmd.Skill{}
)

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

// RegisterDynamic 登记一个运行期才存在的资产类型的用法文档（扩展提供的类型）。
// body 由扩展的 SKILL.md 正文 + manifest 渲染出的工具/参数表拼成，description 是
// prompt 技能清单里的那一行。冲突返回错误——一个类型只能有一份用法文档。
func RegisterDynamic(assetType, body, description string) error {
	if assetType == "" || body == "" {
		return fmt.Errorf("skills: invalid registration for %q", assetType)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[assetType]; exists {
		return fmt.Errorf("skills: duplicate registration %q", assetType)
	}
	registry[assetType] = skillmd.Skill{Body: body, Description: description}
	return nil
}

// UnregisterDynamic 移除一个由 RegisterDynamic 登记的文档。
func UnregisterDynamic(assetType string) {
	mu.Lock()
	defer mu.Unlock()
	delete(registry, assetType)
}

// Get 返回该资产类型 SKILL.md 的正文（不含 frontmatter）。
func Get(assetType string) (string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := registry[assetType]
	return s.Body, ok
}

// Description 返回 frontmatter 中的一行描述，用于 prompt 里的技能清单。
func Description(assetType string) (string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := registry[assetType]
	return s.Description, ok
}

// Types 返回有用法文档的资产类型（内嵌的 + 运行期登记的），已排序。
func Types() []string {
	mu.RLock()
	defer mu.RUnlock()
	types := make([]string, 0, len(registry))
	for name := range registry {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}
