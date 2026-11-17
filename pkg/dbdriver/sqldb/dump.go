package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// DumpDialect 是方言里「转储写成什么样」里整份文件不变的部分。实现 Dialect 的类型同时实现它（以及
// Tables）才提供转储（Session.Dump）；没实现的方言以 dbdriver.ErrUnsupported 拒绝。
//
// 什么钩子都不实现时得到的是默认写法：没有文件开头结尾与批分隔符，每个对象之前按种类写
// DROP TABLE / VIEW / FUNCTION / PROCEDURE / TRIGGER IF EXISTS（引用是 Tables.TableRef("", 名字)：空库名给不带库名的引用，文件导入哪个库就写哪个库），定义是会话给出的
// 原文补上分号，INSERT 写读出的全部列，外键留在定义里，视图不给依赖（按列出的次序写）。要改的方言再实现
// DumpFileWriter / DumpObjectWriter。
type DumpDialect interface {
	DumpSyntax() DumpSyntax
}

// DumpSyntax 是整份转储里不随库变化的写法。
type DumpSyntax struct {
	// Opening / Closing 是文件开头 / 结尾的语句（含结束符），如关闭 / 恢复外键检查。
	Opening []string
	Closing []string
	// BatchTerminator 非空时在每条结构语句之后、每一块数据之后单独写一行（SQL Server 的 GO）。
	BatchTerminator string
}

// DumpFileWriter 是可选的钩子：在一条连接上补全整份文件一次的部分（script 已按 DumpSyntax 填好），
// 如 scope 里的表用到的类型定义（PostgreSQL 的枚举，写进 Types）。
type DumpFileWriter interface {
	DumpFile(ctx context.Context, conn *sql.Conn, scope dbdriver.DumpScope, script *dbdriver.DumpFileScript) error
}

// DumpObjectWriter 是可选的钩子：在一条连接上改一个对象的默认写法（script 已填好默认的 Drop 与
// Definition）。能改的见 dbdriver.DumpObjectScript：存在则删除的写法（没有 IF EXISTS 的库），定义的
// 包装（DELIMITER、/）与附属定义（索引），INSERT 的列（去掉生成列），数据前后的开关（IDENTITY_INSERT），
// 挪到全部数据之后的外键（定义里就要去掉它们），数据之后的收尾（setval），以及视图依赖的对象（DependsOn）。
type DumpObjectWriter interface {
	DumpObject(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error
}

// Dump 写出转储：经 dbdriver.Dump 的编排，用 DumpDialect 给出的写法；方言不能转储时以 ErrUnsupported
// 拒绝，不拿连接、不写任何内容。
func (s *Session) Dump(ctx context.Context, req dbdriver.DumpRequest) (*dbdriver.DumpResult, error) {
	dialect, err := s.DumpDialect()
	if err != nil {
		return nil, err
	}
	return dbdriver.Dump(ctx, req, s, dialect)
}

// DumpDialect 是会话在转储里的部分（Dump 交给编排的就是它；扩展 SDK 经它在 ABI 上提供转储，编排在
// 宿主里）：默认写法加上方言的钩子。方言没有实现 DumpDialect（或 Tables）时以 dbdriver.ErrUnsupported
// 拒绝。
func (s *Session) DumpDialect() (dbdriver.DumpDialect, error) {
	dialect, ok := s.dialect.(DumpDialect)
	if !ok {
		return nil, fmt.Errorf("%w: this driver does not dump SQL files", dbdriver.ErrUnsupported)
	}
	tables, err := s.tables()
	if err != nil {
		return nil, err
	}
	return newDumpDialect(s, tables, dialect), nil
}

// dumpDialect 是 database/sql 之上的驱动在转储里的部分：默认写法加上方言的钩子。
type dumpDialect struct {
	session *Session
	tables  Tables
	syntax  DumpSyntax
}

func newDumpDialect(s *Session, tables Tables, dialect DumpDialect) dumpDialect {
	return dumpDialect{session: s, tables: tables, syntax: dialect.DumpSyntax()}
}

func (d dumpDialect) DumpFile(ctx context.Context, scope dbdriver.DumpScope) (*dbdriver.DumpFileScript, error) {
	script := &dbdriver.DumpFileScript{
		Opening: d.syntax.Opening, Closing: d.syntax.Closing, BatchTerminator: d.syntax.BatchTerminator,
	}
	hook, ok := d.session.dialect.(DumpFileWriter)
	if !ok {
		return script, nil
	}
	return onConn(ctx, d.session, func(conn *sql.Conn) (*dbdriver.DumpFileScript, error) {
		return script, hook.DumpFile(ctx, conn, scope, script)
	})
}

// dropKeywords 是默认的存在则删除按对象种类写的关键字。
var dropKeywords = map[string]string{
	dbdriver.DumpTable: "TABLE", dbdriver.ObjectView: "VIEW",
	dbdriver.ObjectFunction: "FUNCTION", dbdriver.ObjectProcedure: "PROCEDURE", dbdriver.ObjectTrigger: "TRIGGER",
}

func (d dumpDialect) DumpObject(ctx context.Context, object dbdriver.DumpObject) (*dbdriver.DumpObjectScript, error) {
	keyword, ok := dropKeywords[object.Kind]
	if !ok {
		return nil, fmt.Errorf("no default dump for a %s", object.Kind)
	}
	script := &dbdriver.DumpObjectScript{
		Drop:       []string{fmt.Sprintf("DROP %s IF EXISTS %s;", keyword, d.tables.TableRef("", object.Name))},
		Definition: []string{TerminateStatement(object.Definition)},
	}
	hook, ok := d.session.dialect.(DumpObjectWriter)
	if !ok {
		return script, nil
	}
	return onConn(ctx, d.session, func(conn *sql.Conn) (*dbdriver.DumpObjectScript, error) {
		return script, hook.DumpObject(ctx, conn, object, script)
	})
}

// TerminateStatement 去掉语句末尾的空白，没有分号结束的补上一个。
func TerminateStatement(statement string) string {
	statement = strings.TrimRight(statement, " \t\r\n")
	if strings.HasSuffix(statement, ";") {
		return statement
	}
	return statement + ";"
}
