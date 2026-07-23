package command

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestRuntimeCommandsUseResolvedDataDir(t *testing.T) {
	files := []string{
		"ext.go",
		"batch.go",
		"handler.go",
		"approval.go",
		"grant.go",
		"sshproxy.go",
	}

	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(".", name)
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("解析 %s 失败: %v", name, err)
			}

			ast.Inspect(parsed, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "AppDataDir" {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if ok && pkg.Name == "bootstrap" {
					t.Errorf("%s 的运行期路径必须使用 bootstrap.ResolvedDataDir()，不能绕过 --data-dir 调用 AppDataDir()", name)
				}
				return true
			})
		})
	}
}
