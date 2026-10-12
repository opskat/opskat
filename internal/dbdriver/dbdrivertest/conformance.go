package dbdrivertest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ConformanceStatement 是一致性测试在会话上执行的语句：先 Execute，再用 ExecutePaged 取
// 第一页（每页 ConformancePageSize 行）。会话工厂据此准备数据或桩。
const ConformanceStatement = "SELECT 1"

// ConformancePageSize 是一致性测试分页执行时的每页行数。
const ConformancePageSize = 10

// SessionFactory 给一致性测试一条该驱动的会话，且不需要真的数据库：database/sql 之上的
// 驱动接一个桩连接，文件型驱动开一个临时文件。会话由一致性测试关闭。
type SessionFactory func(t *testing.T) dbdriver.Session

// RunConformance 是每个注册的驱动都必须通过的一致性测试。驱动须已注册。
//
//   - 描述：完整、自洽、只用共享代码与前端认识的 Tab / 字段 / 功能，注册表确实能按它声明
//     的 id 与别名找到它。
//   - 会话：SQL 执行与分页执行是每个驱动都要有的，必须可调用并返回成形的结构化结果；
//     没有声明的功能必须以 dbdriver.ErrUnsupported 拒绝（目前会话层按声明取舍的只有连接级
//     只读）。
func RunConformance(t *testing.T, d dbdriver.Driver, newSession SessionFactory) {
	t.Helper()
	for _, problem := range descriptorProblems(d.Descriptor()) {
		t.Errorf("descriptor: %s", problem)
	}
	for _, problem := range registrationProblems(d) {
		t.Errorf("registration: %s", problem)
	}
	for _, problem := range readOnlyProblems(d) {
		t.Errorf("read-only: %s", problem)
	}
	for _, problem := range sessionProblems(newSession(t)) {
		t.Errorf("session: %s", problem)
	}
}

// readOnlyProblems 检查没有声明连接级只读的驱动是否拒绝只读配置。声明了的驱动怎么落实
// 只读各不相同，由各驱动自己的测试固定。
func readOnlyProblems(d dbdriver.Driver) []string {
	desc := d.Descriptor()
	if desc.Supports(dbdriver.CapReadOnly) {
		return nil
	}
	session, err := d.Open(context.Background(), dbdriver.OpenRequest{
		Config: &asset_entity.DatabaseConfig{Driver: desc.ID, ReadOnly: true},
	})
	if err == nil {
		problem := fmt.Sprintf("the driver does not declare %q, yet opened a session for a read-only config", dbdriver.CapReadOnly)
		if closeErr := session.Close(); closeErr != nil {
			problem += fmt.Sprintf(" (and closing it failed: %v)", closeErr)
		}
		return []string{problem}
	}
	if !errors.Is(err, dbdriver.ErrUnsupported) {
		return []string{fmt.Sprintf("a read-only config must be refused with ErrUnsupported, got: %v", err)}
	}
	return nil
}

// sessionProblems 在一条会话上跑每个驱动都必须支持的操作，最后关闭它。
func sessionProblems(s dbdriver.Session) []string {
	ctx := context.Background()
	var problems []string

	res, err := s.Execute(ctx, ConformanceStatement)
	problems = append(problems, resultProblems("Execute", res, err)...)
	if err == nil && res != nil && res.Rows != nil && res.Rows.Total != nil {
		problems = append(problems, "Execute: an unpaged execution must not report a total")
	}

	res, err = s.ExecutePaged(ctx, ConformanceStatement, dbdriver.Page{Index: 0, Size: ConformancePageSize})
	problems = append(problems, resultProblems("ExecutePaged", res, err)...)
	if err == nil && res != nil && res.Rows != nil {
		switch rows := res.Rows; {
		case rows.Total == nil:
			problems = append(problems, "ExecutePaged: a paged query must report the total row count")
		case *rows.Total < int64(len(rows.Rows)):
			problems = append(problems, fmt.Sprintf("ExecutePaged: total %d is smaller than the %d rows of the page", *rows.Total, len(rows.Rows)))
		}
		if len(res.Rows.Rows) > ConformancePageSize {
			problems = append(problems, fmt.Sprintf("ExecutePaged: the page holds %d rows, more than the %d asked for", len(res.Rows.Rows), ConformancePageSize))
		}
	}

	if err := s.Close(); err != nil {
		problems = append(problems, fmt.Sprintf("Close: %v", err))
	}
	return problems
}

// resultProblems 检查 ConformanceStatement 的执行结果：必须成功，并返回成形的结果集。
func resultProblems(op string, res *dbdriver.Result, err error) []string {
	if errors.Is(err, dbdriver.ErrUnsupported) {
		return []string{fmt.Sprintf("%s: every driver must execute SQL, got: %v", op, err)}
	}
	if err != nil {
		return []string{fmt.Sprintf("%s(%q): %v", op, ConformanceStatement, err)}
	}
	if res == nil {
		return []string{fmt.Sprintf("%s: returned no result and no error", op)}
	}
	if res.Rows == nil {
		return []string{fmt.Sprintf("%s(%q): a query must return a row set", op, ConformanceStatement)}
	}
	var problems []string
	if len(res.Rows.Columns) == 0 {
		problems = append(problems, fmt.Sprintf("%s: the row set has no columns", op))
	}
	for i, row := range res.Rows.Rows {
		if len(row) != len(res.Rows.Columns) {
			problems = append(problems, fmt.Sprintf("%s: row %d has %d values for %d columns", op, i, len(row), len(res.Rows.Columns)))
		}
		if _, jsonErr := json.Marshal(row); jsonErr != nil {
			problems = append(problems, fmt.Sprintf("%s: row %d cannot be serialized to JSON: %v", op, i, jsonErr))
		}
	}
	return problems
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
