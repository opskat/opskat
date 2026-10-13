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
//     没有声明的功能必须以 dbdriver.ErrUnsupported 拒绝：连接级只读，以及没有声明的对象类型
//     的浏览（ObjectSource），且拒绝时不能执行任何语句。声明了的浏览操作与表浏览能不能
//     调用，要有数据才能验证，由 RunBrowseConformance / RunTableConformance 在能提供数据的
//     会话上检查。
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
	for _, problem := range sessionProblems(d.Descriptor(), newSession(t)) {
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
func sessionProblems(desc dbdriver.Descriptor, s dbdriver.Session) []string {
	ctx := context.Background()
	var problems []string

	// 拒绝要在任何语句之前：基于桩连接的会话在见到未预期的语句时会失败。
	problems = append(problems, undeclaredObjectProblems(ctx, desc, s)...)

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

// undeclaredObjectProblems 检查没有声明的对象类型都被 ObjectSource 以 ErrUnsupported 拒绝。
func undeclaredObjectProblems(ctx context.Context, desc dbdriver.Descriptor, s dbdriver.Session) []string {
	var problems []string
	for _, objType := range objectTypes {
		capability, _ := dbdriver.ObjectCapability(objType)
		if desc.Supports(capability) {
			continue
		}
		if _, err := s.ObjectSource(ctx, "", objType, "x", ""); !errors.Is(err, dbdriver.ErrUnsupported) {
			problems = append(problems, fmt.Sprintf("ObjectSource(%q): the driver does not declare %q, so it must be refused with ErrUnsupported, got: %v", objType, capability, err))
		}
	}
	return problems
}

var objectTypes = []string{dbdriver.ObjectView, dbdriver.ObjectProcedure, dbdriver.ObjectFunction, dbdriver.ObjectTrigger}

// RunBrowseConformance 检查树的浏览：声明了的操作可以调用并给出成形的结果，没声明的对象
// 类型不出现在对象列表里、查源码被 ErrUnsupported 拒绝。会话必须是能回答这些操作的（有库、
// 有表可列），由测试关闭。
func RunBrowseConformance(t *testing.T, d dbdriver.Driver, newSession SessionFactory) {
	t.Helper()
	for _, problem := range browseProblems(d.Descriptor(), newSession(t)) {
		t.Errorf("browse: %s", problem)
	}
}

func browseProblems(desc dbdriver.Descriptor, s dbdriver.Session) []string {
	ctx := context.Background()
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	databases, err := s.ListDatabases(ctx)
	if err != nil {
		report("ListDatabases: %v", err)
	}
	database := ""
	if len(databases) > 0 {
		database = databases[0]
	}
	tables, err := s.ListTables(ctx, database)
	if err != nil {
		report("ListTables(%q): %v", database, err)
	}
	if len(tables) > 0 {
		meta, err := s.TableMetadata(ctx, database, tables[0])
		switch {
		case err != nil:
			report("TableMetadata(%q): %v", tables[0], err)
		case meta == nil:
			report("TableMetadata(%q): returned no metadata and no error", tables[0])
		case meta.Columns == nil || meta.Indexes == nil || meta.ForeignKeys == nil:
			report("TableMetadata(%q): columns, indexes and foreign keys must be empty lists, not nil (they serialize to null)", tables[0])
		}
	}

	objects, err := s.ListObjects(ctx, database)
	if err != nil {
		report("ListObjects: %v", err)
	} else if objects == nil {
		report("ListObjects: returned no objects and no error")
	} else {
		groups := map[string][]dbdriver.ObjectMeta{
			dbdriver.ObjectView: objects.Views, dbdriver.ObjectProcedure: objects.Procedures,
			dbdriver.ObjectFunction: objects.Functions, dbdriver.ObjectTrigger: objects.Triggers,
		}
		for _, objType := range objectTypes {
			if capability, _ := dbdriver.ObjectCapability(objType); !desc.Supports(capability) && len(groups[objType]) > 0 {
				report("ListObjects lists %d %s objects, but the driver does not declare %q", len(groups[objType]), objType, capability)
			}
		}
	}

	for _, objType := range objectTypes {
		capability, _ := dbdriver.ObjectCapability(objType)
		_, err := s.ObjectSource(ctx, database, objType, "x", "")
		switch declared := desc.Supports(capability); {
		case declared && errors.Is(err, dbdriver.ErrUnsupported):
			report("ObjectSource(%q): declared %q but refused with ErrUnsupported: %v", objType, capability, err)
		case !declared && !errors.Is(err, dbdriver.ErrUnsupported):
			report("ObjectSource(%q): not declared %q, so it must be refused with ErrUnsupported, got: %v", objType, capability, err)
		}
	}

	if err := s.Close(); err != nil {
		report("Close: %v", err)
	}
	return problems
}

// RunTableConformance 检查表浏览：打开表、取一页、计数都可以调用，给出成形且彼此一致的结果。
// 会话必须是能回答这些操作的（database 里有 table 这张表），由测试关闭。筛选条件与排序项
// 的语义随驱动的查询方式而定，由各驱动自己的测试固定。
func RunTableConformance(t *testing.T, newSession SessionFactory, database, table string) {
	t.Helper()
	for _, problem := range tableProblems(newSession(t), database, table) {
		t.Errorf("table: %s", problem)
	}
}

func tableProblems(s dbdriver.Session, database, table string) []string {
	ctx := context.Background()
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	page := dbdriver.Page{Index: 0, Size: ConformancePageSize}

	opened, err := s.OpenTable(ctx, database, table, page.Size)
	switch {
	case err != nil:
		report("OpenTable(%q): %v", table, err)
	case opened == nil:
		report("OpenTable(%q): returned no result and no error", table)
	case opened.FirstPage == nil:
		report("OpenTable(%q): the result has no first page", table)
	default:
		first := opened.FirstPage
		problems = append(problems, rowSetProblems("OpenTable", first)...)
		if len(first.Rows) > page.Size {
			report("OpenTable: the first page holds %d rows, more than the %d asked for", len(first.Rows), page.Size)
		}
		if opened.TotalCount < int64(len(first.Rows)) {
			report("OpenTable: total %d is smaller than the %d rows of the first page", opened.TotalCount, len(first.Rows))
		}
		// 类型、约束、主键说的都必须是表格里有的列：前端按列名把它们对到表格上。
		for name := range opened.ColumnTypes {
			if !slices.Contains(first.Columns, name) {
				report("OpenTable: column types name %q, which is not a column of the first page", name)
			}
		}
		for _, rule := range opened.ColumnRules {
			if !slices.Contains(first.Columns, rule.Name) {
				report("OpenTable: column rules name %q, which is not a column of the first page", rule.Name)
			}
		}
		for _, name := range opened.PrimaryKeys {
			if !slices.Contains(first.Columns, name) {
				report("OpenTable: primary keys name %q, which is not a column of the first page", name)
			}
		}
	}

	rows, err := s.TableRows(ctx, database, table, dbdriver.TableQuery{}, page)
	switch {
	case err != nil:
		report("TableRows(%q): %v", table, err)
	case rows == nil:
		report("TableRows(%q): returned no row set and no error", table)
	default:
		problems = append(problems, rowSetProblems("TableRows", rows)...)
		if len(rows.Rows) > page.Size {
			report("TableRows: the page holds %d rows, more than the %d asked for", len(rows.Rows), page.Size)
		}
		if rows.Total != nil {
			report("TableRows: a table page must not report a total; CountTableRows does")
		}
		if opened != nil && opened.FirstPage != nil {
			if !slices.Equal(rows.Columns, opened.FirstPage.Columns) {
				report("TableRows: columns %v differ from the %v OpenTable gave", rows.Columns, opened.FirstPage.Columns)
			}
			if len(rows.Rows) != len(opened.FirstPage.Rows) {
				report("TableRows: the unfiltered first page holds %d rows, OpenTable's first page %d", len(rows.Rows), len(opened.FirstPage.Rows))
			}
		}
	}

	total, err := s.CountTableRows(ctx, database, table, nil)
	switch {
	case err != nil:
		report("CountTableRows(%q): %v", table, err)
	case opened != nil && total != opened.TotalCount:
		report("CountTableRows: the unfiltered count is %d, but OpenTable reported a total of %d", total, opened.TotalCount)
	}

	if err := s.Close(); err != nil {
		report("Close: %v", err)
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
	return rowSetProblems(op, res.Rows)
}

// rowSetProblems 检查一个结果集成形：有列，每行的值与列一一对应，且能序列化成 JSON。
func rowSetProblems(op string, rows *dbdriver.RowSet) []string {
	var problems []string
	if len(rows.Columns) == 0 {
		problems = append(problems, fmt.Sprintf("%s: the row set has no columns", op))
	}
	for i, row := range rows.Rows {
		if len(row) != len(rows.Columns) {
			problems = append(problems, fmt.Sprintf("%s: row %d has %d values for %d columns", op, i, len(row), len(rows.Columns)))
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
