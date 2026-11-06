package dbdriver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// 语句切分与分类：声明了 CapClassifyStatements 的驱动自己把一段 SQL 切成语句并给每条分类，
// 宿主用它代替通用的 SQL 解析——AI / opsctl 的权限判断逐条套用策略，查询面板据此定位当前语句
// 与危险确认。切分不连数据库：同一段文本任何时候都切出同样的结果。
//
// 共享代码只经 ClassifyStatements（本文件的函数）取结果，不直接调驱动的 StatementClassifier：
// 那里校验驱动的输出，不合约束的一律按「无法切分」报错。

// ErrCannotSplit 表示驱动没能切分一段 SQL：它的输出不合 Statement 的约束，或扩展驱动答复
// 说切不了（宿主把扩展答复的错误包上它）。驱动的其他错误——如扩展驱动不可用
// （ErrDriverUnavailable）——不包它。
var ErrCannotSplit = errors.New("cannot split SQL")

// 危险原因：分类结果里 Dangerous 的语句只能是这四种原因之一，与宿主自己的 SQL 分类同义，
// 数据库策略的禁止标记（deny flags）按它们匹配。
const (
	// ReasonNoWhereUpdate：不带 WHERE 的 UPDATE。
	ReasonNoWhereUpdate = "no_where_update"
	// ReasonNoWhereDelete：不带 WHERE 的 DELETE。
	ReasonNoWhereDelete = "no_where_delete"
	// ReasonPrepare：预编译执行（PREPARE / EXECUTE 一类）。
	ReasonPrepare = "prepare"
	// ReasonCall：调用存储过程。
	ReasonCall = "call"
)

// DangerReasons 是全部危险原因。
func DangerReasons() []string {
	return []string{ReasonNoWhereUpdate, ReasonNoWhereDelete, ReasonPrepare, ReasonCall}
}

// Statement 是切分出的一条语句。
type Statement struct {
	// Start / End 是语句在输入里的字节偏移，半开区间 [Start, End)：落在输入之内、不切开字符、
	// 不为空；各条按顺序排列且互不重叠。语句之间的空白、分隔符与注释可以不属于任何一条。
	Start int `json:"start"`
	End   int `json:"end"`
	// Text 是要执行的这条语句的原文。
	Text string `json:"text"`
	// Type 是语句类型，大写。宿主已有的类型名（SELECT、INSERT、UPDATE、DELETE、DROP TABLE、
	// TRUNCATE、CREATE TABLE、ALTER TABLE、GRANT、CALL、EXPLAIN 等）只用于同种语句；没有对应
	// 名字的语句（Oracle 的 MERGE、PL/SQL 块）用驱动自己的类型名。
	Type string `json:"type"`
	// Dangerous 标记危险语句，Reason 是它的原因（DangerReasons 之一）；不危险的语句没有原因。
	Dangerous bool   `json:"dangerous"`
	Reason    string `json:"reason,omitempty"`
}

// StatementClassifier 是声明了 CapClassifyStatements 的驱动的可选实现：把 sql 切成语句并分类，
// 不连数据库。无法切分（如引号没有闭合）时返回点明原因的错误。
type StatementClassifier interface {
	ClassifyStatements(ctx context.Context, sql string) ([]Statement, error)
}

// ClassifyStatements 用驱动 d 切分并分类 sql，返回校验过的语句。d 没有声明
// CapClassifyStatements 时报 ErrUnsupported；驱动报错时返回点名驱动、包着驱动错误的错误；
// 输出不合 Statement 的约束时报点名驱动与违反的约束的 ErrCannotSplit。
func ClassifyStatements(ctx context.Context, d Driver, sql string) ([]Statement, error) {
	desc := d.Descriptor()
	c, ok := d.(StatementClassifier)
	if !ok || !desc.Supports(CapClassifyStatements) {
		return nil, fmt.Errorf("%w: driver %q does not split and classify statements", ErrUnsupported, desc.ID)
	}
	stmts, err := c.ClassifyStatements(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("driver %q: %w", desc.ID, err)
	}
	if err := checkStatements(sql, stmts); err != nil {
		return nil, fmt.Errorf("driver %q: %w: %w", desc.ID, ErrCannotSplit, err)
	}
	return stmts, nil
}

// checkStatements 检查切分结果是否合 Statement 的约束；错误点明第几条（从 1 数）违反了哪一条。
func checkStatements(sql string, stmts []Statement) error {
	prevEnd := 0
	for i, s := range stmts {
		n := i + 1
		if s.Start < 0 || s.End > len(sql) || s.Start >= s.End {
			return fmt.Errorf("statement %d spans bytes [%d, %d), which is not a non-empty range inside the %d-byte input", n, s.Start, s.End, len(sql))
		}
		for _, at := range []int{s.Start, s.End} {
			if at < len(sql) && !utf8.RuneStart(sql[at]) {
				return fmt.Errorf("statement %d: byte %d is inside a character, not between two", n, at)
			}
		}
		if s.Start < prevEnd {
			return fmt.Errorf("statement %d starts at byte %d, before statement %d ends at byte %d: statements must be in order and must not overlap", n, s.Start, i, prevEnd)
		}
		prevEnd = s.End
		if s.Type == "" {
			return fmt.Errorf("statement %d has no type", n)
		}
		if s.Type != strings.ToUpper(s.Type) {
			return fmt.Errorf("statement %d: type %q is not uppercase", n, s.Type)
		}
		if s.Dangerous && !slices.Contains(DangerReasons(), s.Reason) {
			return fmt.Errorf("statement %d is dangerous with reason %q, which is not one of %s", n, s.Reason, strings.Join(DangerReasons(), ", "))
		}
		if !s.Dangerous && s.Reason != "" {
			return fmt.Errorf("statement %d has danger reason %q but is not marked dangerous", n, s.Reason)
		}
	}
	return nil
}
