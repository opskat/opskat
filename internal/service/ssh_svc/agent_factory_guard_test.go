package ssh_svc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// productDirs 是产品连接路径所在的目录（不含 ssh_svc 本身）：这些调用方只能经
// ssh_svc 的认证工厂接入 Agent，禁止自行构造 Agent 认证方法。
var productDirs = []string{
	"internal/service/credential_resolver",
	"internal/app/ssh",
	"internal/app/sshadapt",
	"internal/ai/helper",
	"cmd/opsctl",
}

// agentConstructionSignals 是"自行构造 Agent 认证方法"的信号：
//   - sshagent.Open(...) 打开传输；
//   - .AuthMethod(...) 精确选签名器；
//   - agent.NewClient(...) / ssh/agent 直接建立协议客户端。
//
// 任何产品路径出现这些调用都意味着绕过了认证工厂。
var agentConstructionSignals = []string{"sshagent.Open", ".AuthMethod(", "agent.NewClient("}

func TestNoProductPathConstructsAgentAuth(t *testing.T) {
	repoRoot := repoRoot(t)
	for _, dir := range productDirs {
		full := filepath.Join(repoRoot, dir)
		err := filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel, rerr := filepath.Rel(repoRoot, path)
			if rerr != nil {
				return rerr
			}
			checkAgentConstruction(t, path, rel)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

// repoRoot 从当前包目录向上找到包含 go.mod 的仓库根。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

// checkAgentConstruction 解析单个 Go 文件，断言它不构造 Agent 认证方法。
// 允许导入 sshagent（用于 MFA 挑战接口类型），但禁止调用其认证构造入口。
func checkAgentConstruction(t *testing.T, path, rel string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		text := ""
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if id, ok := fun.X.(*ast.Ident); ok {
				text = id.Name + "." + fun.Sel.Name
			}
		case *ast.Ident:
			text = fun.Name
		}
		for _, sig := range agentConstructionSignals {
			if strings.Contains(text, sig) {
				pos := fset.Position(call.Pos())
				t.Fatalf("%s:%d: 产品路径 %s 自行构造 Agent 认证方法（%q），必须经 ssh_svc 认证工厂",
					rel, pos.Line, rel, sig)
			}
		}
		return true
	})
}
