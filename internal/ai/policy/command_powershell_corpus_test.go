package policy

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// powershellCorpus 是 testdata/powershell_corpus.json.gz：一批命令（gen_powershell_corpus.py）交给
// Windows PowerShell 5.1 与 PowerShell 7 自身的解析器（analyze_powershell_corpus.ps1）得到的结果。
// 这是与本地识别器无关的另一份事实来源。
type powershellCorpus struct {
	Entries []struct {
		Command string           `json:"command"`
		PS51    powershellParsed `json:"ps51"`
		PS7     powershellParsed `json:"ps7"`
	} `json:"entries"`
}

type powershellParsed struct {
	Errors   int `json:"errors"`
	Commands []struct {
		Text     string `json:"text"`
		Name     string `json:"name"`
		Op       string `json:"op"`
		Elements []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Param string `json:"param"`
		} `json:"elements"`
	} `json:"commands"`
	Issues []string `json:"issues"`
}

func loadPowerShellCorpus(t *testing.T) powershellCorpus {
	t.Helper()
	f, err := os.Open("testdata/powershell_corpus.json.gz")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	var c powershellCorpus
	require.NoError(t, json.NewDecoder(gz).Decode(&c))
	require.NotEmpty(t, c.Entries)
	return c
}

// TestPowerShellCompleteMatchesPowerShellParser：凡是本地判为拆全（Complete）的命令，PowerShell 自己
// 解析出的每一次命令调用都必须原样出现在子命令里，且不能有子集之外会执行代码的结构。识别器对
// 一条命令判为拆不全，只会让它去模型审核或人工，所以只检查这一个方向。
func TestPowerShellCompleteMatchesPowerShellParser(t *testing.T) {
	corpus := loadPowerShellCorpus(t)
	complete := 0
	for _, e := range corpus.Entries {
		parsed, err := ParsePowerShellCommand(e.Command)
		if err != nil || !parsed.Complete {
			continue
		}
		complete++
		units := make(map[string]bool, len(parsed.SubCommands))
		for _, u := range parsed.SubCommands {
			units[u] = true
		}
		for version, ps := range map[string]powershellParsed{"5.1": e.PS51, "7": e.PS7} {
			if ps.Errors > 0 {
				// 解析失败的脚本 PowerShell 一行也不执行（如 5.1 不认 && / ||），没有可漏的命令。
				continue
			}
			for _, c := range ps.Commands {
				assert.Truef(t, units[strings.TrimSpace(c.Text)],
					"PowerShell %s 调用了 %q，本地子命令 %q 里没有：%q", version, c.Text, parsed.SubCommands, e.Command)
				assert.Truef(t, c.Op == "" || c.Op == "Unknown", "调用运算符 %s：%q", c.Op, e.Command)
				if isForEachObject(c.Name) {
					requireForEachScriptBlocksOnly(t, version, e.Command, c.Elements)
				}
			}
			for _, issue := range ps.Issues {
				assert.Truef(t, corpusIssueAllowed(issue), "PowerShell %s 发现 %s，本地却判为拆全：%q", version, issue, e.Command)
			}
		}
	}
	// 语料里应当有相当一部分被判为拆全，否则这条测试什么也没检查。
	require.Greater(t, complete, 200, "complete entries")
	t.Logf("%d / %d corpus commands are complete", complete, len(corpus.Entries))
}

func requireForEachScriptBlocksOnly(t *testing.T, version, command string, elements []struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Param string `json:"param"`
}) {
	t.Helper()
	values := 0
	for _, el := range elements[1:] {
		switch {
		case values > 0:
			values--
		case el.Type == "ScriptBlockExpressionAst":
		case el.Type == "CommandParameterAst":
			n, ok := psForEachParams[strings.ToLower(el.Param)]
			assert.Truef(t, ok, "PowerShell %s：ForEach-Object 参数 -%s：%q", version, el.Param, command)
			values = n
		default:
			assert.Failf(t, "ForEach-Object 接了非脚本块参数", "PowerShell %s：%s %q：%q", version, el.Type, el.Text, command)
		}
	}
}

// corpusIssueAllowed 报告 PowerShell 解析出的一个可能执行代码的结构是否在子集允许的范围内。
func corpusIssueAllowed(issue string) bool {
	kind, detail, _ := strings.Cut(issue, ":")
	lower := strings.ToLower(detail)
	switch kind {
	case "member":
		return psSafeMethods[lower]
	case "static", "staticprop":
		typ, member, _ := strings.Cut(lower, "::")
		allowed, ok := psSafeStatics[strings.TrimPrefix(typ, "system.")]
		if !ok {
			return false
		}
		if kind == "static" {
			return allowed.anyMethod || allowed.methods[member]
		}
		return allowed.anyProp || allowed.props[member]
	case "convert":
		return psSafeCasts[strings.TrimPrefix(lower, "system.")]
	case "redirect":
		return lower == "$null"
	case "assign":
		return psAssignableVariable(detail)
	}
	return false
}
