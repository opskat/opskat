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
// 什么钩子都不实现时得到的是默认写法：没有文件开头结尾与批分隔符，每个对象的删除按种类写
// DROP TABLE / VIEW / FUNCTION / PROCEDURE / TRIGGER IF EXISTS（编排只在勾了「写入删除语句」时写进删除段）（引用是 Tables.TableRef("", 名字)：空库名给不带库名的引用，文件导入哪个库就写哪个库），定义是会话给出的
// 原文补上分号，INSERT 写读出的全部列，外键留在定义里，视图、函数与过程不给依赖（函数与过程在前、视图在后，
// 各自按列出的次序写）。要改的方言再实现
// DumpFileWriter / DumpObjectWriter。
type DumpDialect interface {
	DumpSyntax() DumpSyntax
}

// DumpSyntax 是整份转储里不随库变化的写法。
type DumpSyntax struct {
	// Opening / Closing 是文件开头 / 结尾的语句（含结束符），如 SET NAMES。
	Opening []string
	Closing []string
	// DisableForeignKeyChecks / EnableForeignKeyChecks 是在一个会话里关闭 / 恢复外键检查的语句：勾了「关闭
	// 外键检查」时写在 Opening 之后 / Closing 之前。给出它们即声明驱动描述的 DumpDisablesForeignKeyChecks
	// （DumpDisablesForeignKeyChecks）；不能在会话里关闭的库不给，外键要放在全部数据之后、删除段先删外键
	// （DumpObjectWriter 给出 ForeignKeys 与 DropForeignKeys）。
	DisableForeignKeyChecks []string
	EnableForeignKeyChecks  []string
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
// 包装（DELIMITER、/）与附属定义（索引，只在 object.Options.Indexes 时写），INSERT 的列（去掉生成列），数据
// 前后的开关（IDENTITY_INSERT），挪到全部数据之后的外键与删除段里先删的外键（不能在会话里关闭外键检查的
// 库；定义里就要去掉它们），留在定义里的外键（object.Options.ForeignKeys 不勾时去掉），数据之后的收尾
// （setval），以及视图、函数与过程依赖的对象（Dependencies；见 QueryDumpDependencies）。
// 删除语句与挪走的外键总是给出，写不写由编排按选项决定。
type DumpObjectWriter interface {
	DumpObject(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error
}

// DumpDisablesForeignKeyChecks 报告方言的转储能在会话里关闭外键检查（DumpSyntax 给出了关闭的语句），即
// 驱动描述的 DumpDisablesForeignKeyChecks。不能转储的方言没有。
func DumpDisablesForeignKeyChecks(dialect Dialect) bool {
	dump, ok := dialect.(DumpDialect)
	return ok && len(dump.DumpSyntax().DisableForeignKeyChecks) > 0
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
		DisableForeignKeyChecks: d.syntax.DisableForeignKeyChecks, EnableForeignKeyChecks: d.syntax.EnableForeignKeyChecks,
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

// QueryDumpDependencies 读一个对象依赖的对象（dbdriver.DumpObjectScript.Dependencies）：query 每行两列，种类
// （dbdriver.ObjectView、ObjectFunction、ObjectProcedure）与名字（形式同 DumpObject.Name）。
func QueryDumpDependencies(ctx context.Context, conn *sql.Conn, query string, args ...any) ([]dbdriver.DumpDependency, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var dependencies []dbdriver.DumpDependency
	for rows.Next() {
		var dependency dbdriver.DumpDependency
		if err := rows.Scan(&dependency.Kind, &dependency.Name); err != nil {
			return nil, err
		}
		dependencies = append(dependencies, dependency)
	}
	return dependencies, rows.Err()
}

// TableStatementsWriter 是可选的钩子：转储时方言自己读出一张表的行、写成语句（dbdriver.TableStatementSource），
// 值不必为了写语句从扩展交给宿主、再交回扩展（Oracle 的大 LOB 由它分片读、写成几个追加块）。只经扩展 SDK 在
// ABI 上提供（Session.TableStatements）；进程内的会话转储照旧经游标与行语句生成器，内置驱动都不实现它。
//
// 它逐条给出语句（dbdriver.StatementCursor：一行可以是几条，最后一条标明行的结束），分页由 sqldb 按请求做
// （dbdriver.PageStatements）。写法要与经游标、行语句生成器写出的一样（每行一条「复制为 INSERT」的 INSERT）；
// 只照着写的方言给出 table.RowInsertStatements(ctx) 即可。它取的连接、打开的游标在游标 Close 时还回。
type TableStatementsWriter interface {
	TableStatements(ctx context.Context, table TableStatementsTable) (dbdriver.StatementCursor, error)
}

// TableStatementsTable 是 TableStatementsWriter 要写的那张表。
type TableStatementsTable struct {
	// DB 是会话的连接池。
	DB *sql.DB
	// Database、Table 同 dbdriver.TableStatementSource.TableStatements 的。
	Database, Table string
	// Request 是要写的语句（列、InsertModifier、OmitDatabase、OwnRows）与分页（分页不用方言管）。
	Request dbdriver.TableStatementsRequest

	session *Session
}

// TableStatements 是会话自己写表的数据（扩展 SDK 经它在 ABI 上提供 dbdriver.TableStatementSource）：方言的
// TableStatementsWriter 逐条给出的语句，按请求分页。方言没有实现它时以 dbdriver.ErrUnsupported 拒绝。
func (s *Session) TableStatements() (dbdriver.TableStatementSource, error) {
	hook, ok := s.dialect.(TableStatementsWriter)
	if !ok {
		return nil, fmt.Errorf("%w: this driver does not write a table's statements itself", dbdriver.ErrUnsupported)
	}
	return tableStatementSource{session: s, hook: hook}, nil
}

type tableStatementSource struct {
	session *Session
	hook    TableStatementsWriter
}

func (t tableStatementSource) TableStatements(ctx context.Context, database, table string, req dbdriver.TableStatementsRequest) (dbdriver.TableStatementPages, error) {
	cursor, err := t.hook.TableStatements(ctx, TableStatementsTable{DB: t.session.db, Database: database, Table: table, Request: req, session: t.session})
	if err != nil {
		return nil, err
	}
	return dbdriver.PageStatements(cursor, req), nil
}

// PrepareRowStatements 是这张表的行语句生成器（Session.PrepareRowStatements）：「复制为 INSERT」的写法。
func (t TableStatementsTable) PrepareRowStatements(ctx context.Context) (dbdriver.RowStatementWriter, error) {
	return t.session.PrepareRowStatements(ctx, t.Database, t.Table)
}

// RowInsertStatements 是照着经游标与行语句生成器的写法写这张表的语句（dbdriver.RowInsertStatements）：会话的
// 游标逐行读出、生成器逐行写出，值不离开会话。
func (t TableStatementsTable) RowInsertStatements(ctx context.Context) (dbdriver.StatementCursor, error) {
	return dbdriver.RowInsertStatements(ctx, t.session, t.Database, t.Table, t.Request)
}
