package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"testing"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	_ "github.com/opskat/opskat/internal/dbdriver/builtin"
)

const driverBranchMessage = "共享代码不得按驱动名分支：驱动差异放进 internal/dbdriver 里该驱动的实现，经注册表调用" +
	"（新增驱动见 docs/references/adding-a-database-driver.md）"

var driverConstNames = map[string]bool{
	"DriverMySQL": true, "DriverPostgreSQL": true, "DriverMSSQL": true, "DriverSQLite": true,
}

// registeredDriverLiterals 取注册表里每个驱动的 id 与别名，作为字符串字面量的判据。
func registeredDriverLiterals() map[string]bool {
	out := map[string]bool{}
	for _, d := range dbregistry.List() {
		for _, name := range d.Descriptor().Names() {
			out[name] = true
		}
	}
	return out
}

func repoDriverBranchRule() driverBranchRule {
	return driverBranchRule{
		driverDir: "internal/dbdriver/",
		consts:    driverConstNames,
		literals:  registeredDriverLiterals(),
		message:   driverBranchMessage,
	}
}

// TestNoDriverBranchInSharedCode 全仓扫描：驱动包之外的生产 Go 代码不得按驱动名分支。
func TestNoDriverBranchInSharedCode(t *testing.T) {
	root := repoRoot(t)
	rule := repoDriverBranchRule()
	files := collectGoFiles(t, root)
	all := make([]violation, 0, len(files))
	for _, rel := range files {
		all = append(all, checkDriverBranch(parseRepoFile(t, root, rel), rule)...)
	}
	if len(all) > 0 {
		var b strings.Builder
		for _, v := range all {
			fmt.Fprintf(&b, "%s:%d: %s\n", v.file, v.line, v.message)
		}
		t.Fatalf("发现 %d 处按驱动名分支：\n%s", len(all), b.String())
	}
}

// driverBranchRule 禁止共享 Go 代码按数据库驱动名分支：驱动差异属于 internal/dbdriver 里
// 各驱动自己的实现，经注册表调用。
type driverBranchRule struct {
	driverDir string          // 驱动包所在目录，里面的文件不检查
	consts    map[string]bool // 驱动 id 常量的名字（asset_entity.DriverMySQL…）
	literals  map[string]bool // 驱动 id 与别名的字符串取值，取自注册表
	message   string
}

func (r driverBranchRule) applies(path string) bool {
	return !strings.HasSuffix(path, "_test.go") && !strings.HasPrefix(path, r.driverDir)
}

// lastName 返回标识符或选择器的末段名字（c.Driver -> Driver）。
func lastName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// isDriverConst 判断表达式是不是驱动 id 常量（asset_entity.DriverMySQL，或实体包内的 DriverMySQL）。
func (r driverBranchRule) isDriverConst(e ast.Expr) bool { return r.consts[lastName(e)] }

// isDriverLiteral 判断表达式是不是驱动 id / 别名的字符串字面量。
func (r driverBranchRule) isDriverLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	v, err := strconv.Unquote(lit.Value)
	return err == nil && r.literals[v]
}

// isDriverOperand 判断表达式是不是「一个驱动」：名字就是 driver（不分大小写）的变量或字段，
// 允许被类型转换包着（string(c.Driver)）；DriverVersion 这类只是带着这个词的名字不算。
// 字面量只在对面是它时才算按驱动分支，否则 name == "sqlite" 这类与驱动无关的比较会误报。
func isDriverOperand(e ast.Expr) bool {
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 1 {
		return isDriverOperand(call.Args[0])
	}
	return strings.EqualFold(lastName(e), "driver")
}

func (r driverBranchRule) branchesOn(subject ast.Expr, value ast.Expr) bool {
	return r.isDriverConst(value) || (r.isDriverLiteral(value) && isDriverOperand(subject))
}

func checkDriverBranch(pf parsedFile, rule driverBranchRule) []violation {
	if !rule.applies(pf.path) {
		return nil
	}
	var vs []violation
	report := func(n ast.Node) {
		vs = append(vs, violation{file: pf.path, line: pf.fset.Position(n.Pos()).Line, message: rule.message})
	}
	ast.Inspect(pf.ast, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return true
			}
			if rule.branchesOn(x.X, x.Y) || rule.branchesOn(x.Y, x.X) {
				report(x)
			}
		case *ast.SwitchStmt:
			for _, stmt := range x.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, v := range cc.List {
					if x.Tag != nil && rule.branchesOn(x.Tag, v) {
						report(cc)
						break
					}
				}
			}
		}
		return true
	})
	return vs
}

func TestCheckDriverBranch(t *testing.T) {
	rule := driverBranchRule{
		driverDir: "internal/dbdriver/",
		consts:    map[string]bool{"DriverMySQL": true, "DriverPostgreSQL": true, "DriverMSSQL": true, "DriverSQLite": true},
		literals:  map[string]bool{"mysql": true, "postgresql": true, "postgres": true, "mssql": true, "sqlserver": true, "sqlite": true, "sqlite3": true},
		message:   "按驱动名分支",
	}
	count := func(path, src string) int {
		return len(checkDriverBranch(parseSource(t, path, src), rule))
	}

	violating := map[string]string{
		"== 驱动常量": `package x
import "e"
func f(c *e.DatabaseConfig) bool { return c.Driver == e.DriverMySQL }`,
		"!= 驱动常量": `package x
import "e"
func f(c *e.DatabaseConfig) bool { return c.Driver != e.DriverSQLite }`,
		"常量在左侧": `package x
import "e"
func f(c *e.DatabaseConfig) bool { return e.DriverMSSQL == c.Driver }`,
		"switch case 驱动常量": `package x
import "e"
func f(d e.DatabaseDriver) string {
	switch d {
	case e.DriverPostgreSQL:
		return "pg"
	}
	return ""
}`,
		"实体包内的裸标识符": `package asset_entity
func f(d DatabaseDriver) bool { return d == DriverPostgreSQL }`,
		"驱动字段与字符串字面量比较": `package x
func f(driver string) bool { return driver == "postgres" }`,
		"switch 驱动字段 case 字面量": `package x
func f(c *C) int {
	switch string(c.Driver) {
	case "mssql", "sqlserver":
		return 1
	}
	return 0
}`,
	}
	for name, src := range violating {
		t.Run("报出："+name, func(t *testing.T) {
			if n := count("internal/service/x/x.go", src); n != 1 {
				t.Fatalf("want 1 violation, got %d", n)
			}
		})
	}

	t.Run("驱动包内不报", func(t *testing.T) {
		if n := count("internal/dbdriver/mysql/x.go", violating["== 驱动常量"]); n != 0 {
			t.Fatalf("want 0, got %d", n)
		}
	})
	t.Run("测试文件不报", func(t *testing.T) {
		if n := count("internal/service/x/x_test.go", violating["== 驱动常量"]); n != 0 {
			t.Fatalf("want 0, got %d", n)
		}
	})
	t.Run("非分支用法不报", func(t *testing.T) {
		for name, src := range map[string]string{
			"把常量当值赋给字段":     "package x\nimport \"e\"\nvar c = e.DatabaseConfig{Driver: e.DriverMySQL}",
			"两个变量比较":        "package x\nfunc f(a, b string) bool { return a == b }",
			"字符串与驱动无关的变量比较": "package x\nfunc f(name string) bool { return name == \"sqlite\" }",
			"DriverVersion": "package x\nfunc f(r *R) bool { return r.DriverVersion == \"\" }",
		} {
			if n := count("internal/service/x/x.go", src); n != 0 {
				t.Fatalf("%s: want 0, got %d", name, n)
			}
		}
	})
}
