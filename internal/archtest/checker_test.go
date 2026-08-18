// Package archtest 把 AGENTS.md / docs/DEVELOP.md 中可判定的架构与日志约定
// 机械化为 go test 门禁：文档负责描述，这里的扫描测试负责强制。
// 规则表与全仓扫描见 conventions_test.go；本文件是 checker 实现及其守护单测
// （双向断言：违规必须被报，豁免与合规写法不得误报）。
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type violation struct {
	file    string // 仓库相对路径（斜杠分隔）
	line    int
	message string
}

// importBanRule 禁止某目录（前缀）下的文件 import 指定路径及其子包。
type importBanRule struct {
	dir       string          // 仓库相对路径前缀，"" 表示全仓
	files     map[string]bool // 可选的精确文件集合；用于只约束少量生产入口
	banned    []string        // import 路径；默认同时匹配其子包
	exact     bool            // 仅精确匹配 banned 本身（如禁 "log" 但放行 "log/slog" 适配第三方库）
	skipTests bool            // 是否放行 *_test.go（如测试注入 mock repo）
	exempt    map[string]bool // 仓库相对路径；存量债务或文档明示的保留，只减不增
	message   string
}

type parsedFile struct {
	path string // 仓库相对路径（斜杠分隔）
	fset *token.FileSet
	ast  *ast.File
}

func (r importBanRule) applies(path string) bool {
	if r.files != nil && !r.files[path] {
		return false
	}
	if r.dir != "" && !strings.HasPrefix(path, r.dir) {
		return false
	}
	if r.skipTests && strings.HasSuffix(path, "_test.go") {
		return false
	}
	return !r.exempt[path]
}

// bannedImport 判断 import 路径是否命中 banned 本身（exact 时）或其子包；
// 前缀相似的无关包（如 repositoryx）不命中。
func bannedImport(path string, banned []string, exact bool) bool {
	for _, b := range banned {
		if path == b || (!exact && strings.HasPrefix(path, b+"/")) {
			return true
		}
	}
	return false
}

func checkImportBan(pf parsedFile, rule importBanRule) []violation {
	if !rule.applies(pf.path) {
		return nil
	}
	var vs []violation
	for _, imp := range pf.ast.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if bannedImport(p, rule.banned, rule.exact) {
			vs = append(vs, violation{
				file:    pf.path,
				line:    pf.fset.Position(imp.Pos()).Line,
				message: "import " + p + "：" + rule.message,
			})
		}
	}
	return vs
}

// zapLocalName 返回 go.uber.org/zap 在该文件中的本地包名（含别名）。
func zapLocalName(f *ast.File) (string, bool) {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != "go.uber.org/zap" {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				return "", false
			}
			return imp.Name.Name, true
		}
		return "zap", true
	}
	return "", false
}

// checkZapAny 禁用 zap.Any；唯一豁免是 recover() 的 panic 值（类型未知），
// 约定其 key 为 "panic"（docs/DEVELOP.md → Logging for key flows）。
func checkZapAny(pf parsedFile, message string) []violation {
	name, ok := zapLocalName(pf.ast)
	if !ok {
		return nil
	}
	var vs []violation
	ast.Inspect(pf.ast, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Any" {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != name {
			return true
		}
		if len(call.Args) > 0 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"panic"` {
				return true
			}
		}
		vs = append(vs, violation{file: pf.path, line: pf.fset.Position(call.Pos()).Line, message: message})
		return true
	})
	return vs
}

// repoRoot 由本文件位置定位仓库根（internal/archtest/ 上两级）。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// collectGoFiles 收集待扫描的 .go 文件（仓库相对路径）。
// 跳过隐藏目录、前端与 e2e、构建产物、生成的 mock_*。
func collectGoFiles(t *testing.T, root string) []string {
	t.Helper()
	skip := map[string]bool{"node_modules": true, "frontend": true, "e2e": true, "build": true, "docs": true}
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (skip[name] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "mock_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	return files
}

func parseRepoFile(t *testing.T, root, rel string) parsedFile {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return parsedFile{path: rel, fset: fset, ast: f}
}

// --- 以下是 checker 自身的守护单测：规则被删/被缩权时这里先红 ---

func parseSource(t *testing.T, path, src string) parsedFile {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", path, err)
	}
	return parsedFile{path: path, fset: fset, ast: f}
}

func TestCheckImportBan(t *testing.T) {
	rule := importBanRule{
		dir:       "internal/app/",
		banned:    []string{"github.com/opskat/opskat/internal/repository"},
		skipTests: true,
		exempt:    map[string]bool{"internal/app/legacy/old.go": true},
		message:   "走域服务",
	}

	t.Run("命中禁用包及其子包", func(t *testing.T) {
		pf := parseSource(t, "internal/app/x/x.go",
			`package x
import "github.com/opskat/opskat/internal/repository/asset_repo"
var _ = asset_repo.Asset`)
		if vs := checkImportBan(pf, rule); len(vs) != 1 {
			t.Fatalf("want 1 violation, got %v", vs)
		}
	})

	t.Run("import 别名不能绕过（按路径匹配）", func(t *testing.T) {
		pf := parseSource(t, "internal/app/x/x.go",
			`package x
import ar "github.com/opskat/opskat/internal/repository/asset_repo"
var _ = ar.Asset`)
		if vs := checkImportBan(pf, rule); len(vs) != 1 {
			t.Fatalf("want 1 violation, got %v", vs)
		}
	})

	t.Run("前缀相似的无关包不误报", func(t *testing.T) {
		pf := parseSource(t, "internal/app/x/x.go",
			`package x
import "github.com/opskat/opskat/internal/repositoryx"
var _ = repositoryx.X`)
		if vs := checkImportBan(pf, rule); len(vs) != 0 {
			t.Fatalf("want 0 violations, got %v", vs)
		}
	})

	t.Run("目录范围之外不检查", func(t *testing.T) {
		pf := parseSource(t, "internal/service/asset_svc/svc.go",
			`package asset_svc
import "github.com/opskat/opskat/internal/repository/asset_repo"
var _ = asset_repo.Asset`)
		if vs := checkImportBan(pf, rule); len(vs) != 0 {
			t.Fatalf("want 0 violations, got %v", vs)
		}
	})

	t.Run("豁免路径不报", func(t *testing.T) {
		pf := parseSource(t, "internal/app/legacy/old.go",
			`package legacy
import "github.com/opskat/opskat/internal/repository/asset_repo"
var _ = asset_repo.Asset`)
		if vs := checkImportBan(pf, rule); len(vs) != 0 {
			t.Fatalf("want 0 violations, got %v", vs)
		}
	})

	t.Run("exact 规则不殃及子包（第三方库的 slog 适配是正当用法）", func(t *testing.T) {
		logRule := importBanRule{banned: []string{"log"}, exact: true, message: "用 cago logger"}
		pf := parseSource(t, "internal/service/x/x.go",
			`package x
import "log/slog"
var _ = slog.New`)
		if vs := checkImportBan(pf, logRule); len(vs) != 0 {
			t.Fatalf("want 0 violations, got %v", vs)
		}
		pf = parseSource(t, "internal/service/x/x.go",
			`package x
import "log"
var _ = log.Printf`)
		if vs := checkImportBan(pf, logRule); len(vs) != 1 {
			t.Fatalf("want 1 violation, got %v", vs)
		}
	})

	t.Run("skipTests 时测试文件放行", func(t *testing.T) {
		pf := parseSource(t, "internal/app/x/x_test.go",
			`package x
import "github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
var _ = mock_asset_repo.New`)
		if vs := checkImportBan(pf, rule); len(vs) != 0 {
			t.Fatalf("want 0 violations, got %v", vs)
		}
	})

	t.Run("精确文件集合只约束列出的生产入口", func(t *testing.T) {
		fileRule := importBanRule{
			files: map[string]bool{
				"cmd/opsctl/command/policy.go":       true,
				"cmd/opsctl/command/policy_group.go": true,
				"cmd/opsctl/command/tty_approval.go": true,
			},
			banned:  []string{"github.com/opskat/opskat/internal/repository", "github.com/opskat/opskat/internal/pkg/dbutil"},
			message: "经 policy service 访问",
		}
		for _, tc := range []struct {
			path       string
			importPath string
		}{
			{path: "cmd/opsctl/command/policy.go", importPath: "github.com/opskat/opskat/internal/repository/asset_repo"},
			{path: "cmd/opsctl/command/policy_group.go", importPath: "github.com/opskat/opskat/internal/pkg/dbutil"},
			{path: "cmd/opsctl/command/tty_approval.go", importPath: "github.com/opskat/opskat/internal/repository/asset_repo"},
		} {
			pf := parseSource(t, tc.path, "package command\nimport _ \""+tc.importPath+"\"")
			if vs := checkImportBan(pf, fileRule); len(vs) != 1 {
				t.Fatalf("%s importing %s: want 1 violation, got %v", tc.path, tc.importPath, vs)
			}
		}

		pf := parseSource(t, "cmd/opsctl/command/asset.go",
			`package command
import "github.com/opskat/opskat/internal/repository/asset_repo"
var _ = asset_repo.Asset`)
		if vs := checkImportBan(pf, fileRule); len(vs) != 0 {
			t.Fatalf("unlisted sibling: want 0 violations, got %v", vs)
		}
	})
}

func TestCheckZapAny(t *testing.T) {
	t.Run("zap.Any 被拦截", func(t *testing.T) {
		pf := parseSource(t, "internal/service/x/x.go",
			`package x
import "go.uber.org/zap"
var _ = zap.Any("key", 1)`)
		if vs := checkZapAny(pf, "m"); len(vs) != 1 {
			t.Fatalf("want 1 violation, got %v", vs)
		}
	})

	t.Run("zap 别名不能绕过", func(t *testing.T) {
		pf := parseSource(t, "internal/service/x/x.go",
			`package x
import z "go.uber.org/zap"
var _ = z.Any("key", 1)`)
		if vs := checkZapAny(pf, "m"); len(vs) != 1 {
			t.Fatalf("want 1 violation, got %v", vs)
		}
	})

	t.Run(`recover 豁免：key 为 "panic" 时放行`, func(t *testing.T) {
		pf := parseSource(t, "internal/service/x/x.go",
			`package x
import "go.uber.org/zap"
func f(r any) { _ = zap.Any("panic", r) }`)
		if vs := checkZapAny(pf, "m"); len(vs) != 0 {
			t.Fatalf("want 0 violations, got %v", vs)
		}
	})

	t.Run("强类型字段与他包同名方法不误报", func(t *testing.T) {
		pf := parseSource(t, "internal/service/x/x.go",
			`package x
import (
	"go.uber.org/zap"
	"example.com/other"
)
var _ = zap.String("key", "v")
var _ = other.Any("key")`)
		if vs := checkZapAny(pf, "m"); len(vs) != 0 {
			t.Fatalf("want 0 violations, got %v", vs)
		}
	})

	t.Run("未 import zap 的文件跳过", func(t *testing.T) {
		pf := parseSource(t, "internal/service/x/x.go",
			`package x
import "example.com/zaplike"
var _ = zaplike.Any("key", 1)`)
		if vs := checkZapAny(pf, "m"); len(vs) != 0 {
			t.Fatalf("want 0 violations, got %v", vs)
		}
	})
}
