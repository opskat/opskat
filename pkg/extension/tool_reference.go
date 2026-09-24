package extension

import (
	"fmt"
	"sort"
	"strings"
)

// ToolReference renders the tools the guest reported through describe() as the
// command-syntax section of the extension's help document.
//
// tools[].parameters used to be read by exactly one consumer — the flag DSL that turns
// `--flag=value` into typed JSON — and by nothing the model could see. The model had to
// guess flag names and types from the extension's prose, which is exactly the failure
// mode the built-in types' SKILL.md avoids by spelling out their syntax. Rendering the
// declaration the parser already enforces means the two can never disagree — and since
// that declaration is now reflected from the handler's own argument type, neither can
// drift from the code that runs.
//
// Call it on a Localized manifest so the i18n keys in tools[].i18n.description are
// resolved; on a raw manifest the keys are printed as-is, which is still honest but
// unhelpful.
func (m *Manifest) ToolReference() string {
	if len(m.Tools) == 0 {
		return ""
	}
	tools := append([]ToolDef(nil), m.Tools...)
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	var b strings.Builder
	b.WriteString("## exec syntax\n\n")
	b.WriteString("`exec(asset=<asset>, command=\"<tool> [--flag=value ...]\")`. ")
	b.WriteString("The first token is the tool name. Flags are `--flag=value` or a bare `--flag` (true) — ")
	b.WriteString("not space separated. `array` parameters take a comma-separated value (`--keys=a,b,c`). ")
	b.WriteString("Use `--json='{...}'` (single-quoted) when a value the flag syntax cannot express is needed; ")
	b.WriteString("`--json` replaces the whole argument object and cannot be combined with other flags.\n\n")
	b.WriteString("## tools\n")

	for _, t := range tools {
		b.WriteString("\n### ")
		b.WriteString(t.Name)
		b.WriteString("\n")
		if desc := strings.TrimSpace(t.I18n.Description); desc != "" {
			b.WriteString("\n")
			b.WriteString(desc)
			b.WriteString("\n")
		}
		params := renderToolParams(t)
		b.WriteString("\n")
		b.WriteString(params)
	}
	return b.String()
}

func renderToolParams(t ToolDef) string {
	props, _ := t.Parameters["properties"].(map[string]any)
	if len(props) == 0 {
		return "Takes no parameters.\n"
	}
	required := make(map[string]struct{})
	if raw, ok := t.Parameters["required"].([]any); ok {
		for _, item := range raw {
			if name, ok := item.(string); ok {
				required[name] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("| flag | type | required | description |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, name := range names {
		prop, _ := props[name].(map[string]any)
		typ, _ := prop["type"].(string)
		if typ == "array" {
			typ = "array<string>"
		}
		req := "no"
		if _, ok := required[name]; ok {
			req = "yes"
		}
		desc, _ := prop["description"].(string)
		if desc == "" {
			desc, _ = prop["title"].(string)
		}
		fmt.Fprintf(&b, "| `--%s` | %s | %s | %s |\n", name, typ, req, escapeTableCell(desc))
	}
	return b.String()
}

// escapeTableCell keeps a description containing "|" or a newline from breaking the
// markdown row it is rendered into.
func escapeTableCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", "\\|")
}
