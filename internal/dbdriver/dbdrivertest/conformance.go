package dbdrivertest

import (
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/opskat/opskat/internal/dbdriver"
)

// RunConformance 是每个注册的驱动都必须通过的一致性测试。驱动须已注册。
//
// 目前检查的是描述：完整、自洽、只用共享代码与前端认识的 Tab / 字段 / 功能，并且注册表
// 确实能按它声明的 id 与别名找到它。
func RunConformance(t *testing.T, d dbdriver.Driver) {
	t.Helper()
	for _, problem := range descriptorProblems(d.Descriptor()) {
		t.Errorf("descriptor: %s", problem)
	}
	for _, problem := range registrationProblems(d) {
		t.Errorf("registration: %s", problem)
	}
}

// 名字会出现在命令行（--type <name>）与存储的配置里，只允许小写单词。
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func descriptorProblems(desc dbdriver.Descriptor) []string {
	var problems []string
	report := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if !namePattern.MatchString(string(desc.ID)) {
		report("id %q must match %s", desc.ID, namePattern)
	}
	if desc.Name == "" {
		report("display name is empty")
	}
	if desc.Icon == "" {
		report("icon is empty")
	}
	// 展示顺序必须由驱动自己声明：留着零值的驱动会不声不响地排到所有驱动前面。
	if desc.Order <= 0 {
		report("order %d is not declared; it must be positive", desc.Order)
	}

	// 切片必须非 nil：描述原样序列化给前端，nil 会变成 null 而不是空数组。
	if desc.Aliases == nil {
		report("aliases is nil; declare an empty list when there are none")
	}
	seenNames := map[string]bool{string(desc.ID): true}
	for _, alias := range desc.Aliases {
		if !namePattern.MatchString(alias) {
			report("alias %q must match %s", alias, namePattern)
		}
		if seenNames[alias] {
			report("alias %q repeats the id or another alias", alias)
		}
		seenNames[alias] = true
	}

	if len(desc.Form) == 0 {
		report("form declares no tabs")
	}
	seenTabs := map[dbdriver.FormTab]bool{}
	seenFields := map[dbdriver.FormField]bool{}
	for _, tab := range desc.Form {
		if !slices.Contains(dbdriver.FormTabs(), tab.Tab) {
			report("form tab %q is not a known tab", tab.Tab)
		}
		if seenTabs[tab.Tab] {
			report("form tab %q is listed more than once", tab.Tab)
		}
		seenTabs[tab.Tab] = true
		if len(tab.Fields) == 0 {
			report("form tab %q declares no fields", tab.Tab)
		}
		for _, field := range tab.Fields {
			if !slices.Contains(dbdriver.FormFields(), field) {
				report("form field %q is not a known field", field)
			}
			if seenFields[field] {
				report("form field %q is used more than once", field)
			}
			seenFields[field] = true
		}
	}

	// 默认端口与端口字段必须一起出现：表单切到这个驱动时用它预填端口。
	if desc.UsesField(dbdriver.FieldPort) {
		if desc.DefaultPort < 1 || desc.DefaultPort > 65535 {
			report("default port %d is not a valid port, but the form uses the port field", desc.DefaultPort)
		}
	} else if desc.DefaultPort != 0 {
		report("default port %d is declared, but the form has no port field", desc.DefaultPort)
	}

	if desc.Capabilities == nil {
		report("capabilities is nil; declare an empty list when there are none")
	}
	seenCaps := map[dbdriver.Capability]bool{}
	for _, c := range desc.Capabilities {
		if !slices.Contains(dbdriver.Capabilities(), c) {
			report("capability %q is not a known capability", c)
		}
		if seenCaps[c] {
			report("capability %q is declared more than once", c)
		}
		seenCaps[c] = true
	}
	return problems
}

func registrationProblems(d dbdriver.Driver) []string {
	var problems []string
	desc := d.Descriptor()
	if got, err := dbdriver.Lookup(desc.ID); err != nil {
		problems = append(problems, fmt.Sprintf("Lookup(%q): %v", desc.ID, err))
	} else if got != d {
		problems = append(problems, fmt.Sprintf("Lookup(%q) returns a different driver", desc.ID))
	}
	for _, name := range desc.Names() {
		if got, ok := dbdriver.ByName(name); !ok || got != d {
			problems = append(problems, fmt.Sprintf("ByName(%q) does not resolve to this driver", name))
		}
	}
	return problems
}
