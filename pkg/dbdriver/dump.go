package dbdriver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// DumpTable 是转储里表的种类；视图、函数、过程与触发器用对应的 Object*。
const DumpTable = "table"

// DumpRequest 是 Session.Dump 要写出的一份 SQL 转储。
type DumpRequest struct {
	// Database 是对象所在的库，含义同 Session 的其他操作。
	Database string
	// Schema 是转储的 schema（有 schema 分组的驱动），写进注释头、交给方言；Whole 时非空表示只转储这个
	// schema。转储选中的表时为空。
	Schema string
	// Tables 是要转储的表与视图，用 ListTables 给出的形式，按用户选中的次序；Whole 时不用。
	Tables []string
	// WithData 为真时每张表的结构之后写出全部行（视图、函数、过程与触发器只写定义）。
	WithData bool
	// Whole 为真时转储整个库（Schema 为空）或整个 schema 的全部对象：ListTables 给出的表，以及 ListObjects
	// 给出的视图（ListTables 没有列出的也在内）、函数、过程与触发器。有 schema 分组的驱动给的名字是
	// 「schema.名字」（同 ListTables），整个 schema 就是以「Schema.」开头的那些。
	Whole bool
	// Options 是导出选项（「导出数据」之外的，那一项是 WithData）。
	Options DumpOptions
	// Header 是注释头里写的来源。
	Header DumpHeader
	// Writer 是写出的目标。
	Writer io.Writer
	// Progress 在每个对象开始、写完以及读行的中途被调用（同一个 goroutine 上，按先后）；nil 表示不需要。
	Progress func(DumpProgress)
}

// DumpOptions 是转储的导出选项：每一项勾上时文件里才有对应的部分，不勾就整个没有，其余不变。零值是全部
// 不勾。
type DumpOptions struct {
	// Indexes：表的索引（主键与唯一约束不算，它们总在建表语句里；唯一索引也不算，外键可能引用它，总是写出）。
	// 依库而定在建表语句里或紧随其后，由方言按 DumpObject.Options 写或不写。
	Indexes bool `json:"indexes"`
	// ForeignKeys：外键。留在建表语句里的（能关闭外键检查的库）由方言按 DumpObject.Options 去掉；全部数据
	// 之后加上的（DumpObjectScript.ForeignKeys）由编排写或不写。
	ForeignKeys bool `json:"foreignKeys"`
	// Views、Routines、Triggers：整库或整个 schema 时的视图、函数与过程、触发器。只在 Whole 时有意义：转储
	// 选中的表时没有函数、过程与触发器，选中的视图照样写定义。
	Views    bool `json:"views"`
	Routines bool `json:"routines"`
	Triggers bool `json:"triggers"`
	// Drops：文件开头的删除段（DumpObjectScript.DropForeignKeys 与 Drop、DumpFileScript.DropTypes）。不勾时
	// 文件里没有任何删除语句。
	Drops bool `json:"drops"`
	// DisableForeignKeyChecks：文件开头关闭外键检查、结尾恢复（DumpFileScript.DisableForeignKeyChecks /
	// EnableForeignKeyChecks）。只有声明了 Descriptor.DumpDisablesForeignKeyChecks 的驱动给出这两段语句。
	DisableForeignKeyChecks bool `json:"disableForeignKeyChecks"`
}

// DumpHeader 是转储文件注释头里写的来源：OpsKat 版本、资产名与生成时间（库名、schema 与内容取自
// DumpRequest）。主机、端口与账号不在其中，也不写进文件。
type DumpHeader struct {
	Version string
	Asset   string
	Time    time.Time
}

// DumpProgress 是转储到目前为止的进度。
type DumpProgress struct {
	// Objects 是这次要写的对象数（表、视图、函数、过程与触发器），Object 是正在写的第几个（从 1 起），Name 是它的名字。
	Objects int    `json:"objects"`
	Object  int    `json:"object"`
	Name    string `json:"name"`
	// Rows 是到目前为止写出的行数（全部表）。
	Rows int64 `json:"rows"`
	// Finished 在一个对象写完时给出它写了什么；其余时候为 nil。
	Finished *DumpedObject `json:"finished,omitempty"`
}

// DumpedObject 是写完的一个对象。
type DumpedObject struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Data 表示写了它的行（结构和数据时的表），Rows 是行数。
	Data bool  `json:"data"`
	Rows int64 `json:"rows"`
}

// DumpResult 是写完的转储。
type DumpResult struct {
	Objects int   `json:"objects"`
	Rows    int64 `json:"rows"`
	Bytes   int64 `json:"bytes"`
}

// DumpObjectError 表示转储停在了一个对象上：读它的定义、生成它的语句或读它的行时出错。
type DumpObjectError struct {
	Object string
	Err    error
}

func (e *DumpObjectError) Error() string {
	return fmt.Sprintf("dump stopped at %s: %v", e.Object, e.Err)
}
func (e *DumpObjectError) Unwrap() error { return e.Err }

// DumpScope 是交给方言的整份转储的范围。
type DumpScope struct {
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
	// Tables 是转储的表与视图：选中的，或 Whole 时库里（schema 里）的全部。
	Tables   []string `json:"tables"`
	WithData bool     `json:"withData"`
	// Whole 表示转储整个库或整个 schema（同 DumpRequest.Whole）。
	Whole bool `json:"whole,omitempty"`
	// Options 是导出选项（同 DumpRequest.Options）。
	Options DumpOptions `json:"options"`
}

// DumpFileScript 是方言给出的、整份文件一次的部分。每条语句都是写进文件的原文（含结束符），一条
// 写一行。
type DumpFileScript struct {
	// Opening 写在注释头之后，如 SET NAMES、关闭外键检查。
	Opening []string `json:"opening,omitempty"`
	// Types 写在第一张表之前：表的列用到的类型定义（如 PostgreSQL 的枚举）。
	Types []string `json:"types,omitempty"`
	// Closing 写在文件末尾。
	Closing []string `json:"closing,omitempty"`
	// DisableForeignKeyChecks / EnableForeignKeyChecks 在勾了 DumpOptions.DisableForeignKeyChecks 时写在
	// Opening 之后 / Closing 之前（如 MySQL 的 SET FOREIGN_KEY_CHECKS）。只有能在会话里关闭外键检查的库
	// （声明了 Descriptor.DumpDisablesForeignKeyChecks）给出它们。
	DisableForeignKeyChecks []string `json:"disableForeignKeyChecks,omitempty"`
	EnableForeignKeyChecks  []string `json:"enableForeignKeyChecks,omitempty"`
	// DropTypes 是删除段最后的「存在则删除」Types 里的类型（勾了 DumpOptions.Drops 时）。
	DropTypes []string `json:"dropTypes,omitempty"`
	// BatchTerminator 非空时在每条结构语句之后、每一块数据之后单独写一行（SQL Server 的 GO）。
	BatchTerminator string `json:"batchTerminator,omitempty"`
}

// DumpObject 是交给方言的一个对象。
type DumpObject struct {
	Database string `json:"database"`
	// Kind 是 DumpTable、ObjectView、ObjectFunction、ObjectProcedure 或 ObjectTrigger。
	Kind string `json:"kind"`
	// Name 用 ListTables（表与视图）或 ListObjects（其余）给出的形式。
	Name string `json:"name"`
	// Table 是触发器所在的表（ObjectMeta.Table，只有给出它的驱动才有）。
	Table string `json:"table,omitempty"`
	// Definition 是会话给出的定义原文：表与视图的来自 TableDDL，其余的来自 ObjectSource。
	Definition string `json:"definition"`
	// WithData 表示它的行会接着写出（只有结构和数据时的表）。
	WithData bool `json:"withData"`
	// Options 是导出选项（同 DumpRequest.Options）：方言据此写不写表的索引与留在建表语句里的外键。
	Options DumpOptions `json:"options"`
}

// DumpObjectScript 是方言为一个对象给出的语句：每条都是写进文件的原文（含结束符），一条写一行。
type DumpObjectScript struct {
	// Drop 是「存在则删除」。勾了 DumpOptions.Drops 时写在文件开头的删除段里：触发器、视图与函数和过程
	// （按依赖倒序）、表（倒序）。方言总是给出它，写不写由编排决定。
	Drop []string `json:"drop"`
	// DropForeignKeys 是表上的外键「存在则删除」：不能关闭外键检查的库（外键挡着，被引用的表删不掉）给出，
	// 勾了 DumpOptions.Drops 时写在删除段的最前面。不带 CASCADE。
	DropForeignKeys []string `json:"dropForeignKeys,omitempty"`
	// Definition 是建出它的语句，已按方言包好（如 MySQL 的 DELIMITER、Oracle 的 /），索引等附属定义
	// 也在其中。为空表示方言不转储这个对象（如 Oracle 列在视图里的物化视图）：它不写进文件（没有注释分隔行、
	// 删除与其余语句），不计入对象数，也不在交给 DumpFile 的范围里。
	Definition []string `json:"definition"`
	// DataColumns 是 INSERT 写入的列，按次序（生成列 / 计算列不在其中）；nil 表示读出的全部列。
	DataColumns []string `json:"dataColumns"`
	// InsertModifier 非空时写在它的每条 INSERT 的列与 VALUES 之间（见 RowStatementsRequest.InsertModifier），
	// 如 PostgreSQL 往 GENERATED ALWAYS 身份列写值的 OVERRIDING SYSTEM VALUE。
	InsertModifier string `json:"insertModifier,omitempty"`
	// DataPrologue / DataEpilogue 写在它的行之前 / 之后（如 SQL Server 的 IDENTITY_INSERT 开、关）。
	DataPrologue []string `json:"dataPrologue,omitempty"`
	DataEpilogue []string `json:"dataEpilogue,omitempty"`
	// ForeignKeys 写在全部表与数据之后：不能全局关闭外键检查的库在这里加外键（定义里就不带）。只在勾了
	// DumpOptions.ForeignKeys 时写出；方言总是给出它。
	ForeignKeys []string `json:"foreignKeys,omitempty"`
	// PostData 写在全部对象之后：序列与自增的收尾（如 PostgreSQL 的 setval）。
	PostData []string `json:"postData,omitempty"`
	// Dependencies 是它的定义用到的视图、函数与过程，带上种类（视图与函数可以同名）。视图、
	// 函数与过程写在同一段里，按它排序：被依赖的先写，删除段里倒过来。不在这一段里的对象（如表、这次
	// 没有转储的对象）不影响次序，自己依赖自己（递归的过程）也不算；没有给出依赖的对象，函数与过程在前、
	// 视图在后，各自按列出的次序。表的 Dependencies 是它的父表（种类 DumpTable：分区所属的分区表、继承的
	// 子表的父表），表那一段同样按它排序：父表先写，删除段里子表先删；没有父表的表按列出的次序。依赖成环时
	// （如 SQL Server 里互相调用的过程：延迟名称解析，谁先建都行）环在回到正在排的对象处断开，照样写出。
	Dependencies []DumpDependency `json:"dependencies,omitempty"`
	// Partitions 是表的分区（名字的形式同 DumpObject.Name；只有分区表的父表给出，多级分区由各级给出自己的下一级）：
	// 它们随这张表一起转储，只转储选中的表时也是，已经在这次转储里的不再重复。给出了分区的表自己没有行（行都在
	// 分区里），结构和数据时也不为它读行、不写它的记录段。
	Partitions []string `json:"partitions,omitempty"`
}

// DumpDependency 是一个对象用到的另一个对象（DumpObjectScript.Dependencies 里的一项）。
type DumpDependency struct {
	// Kind 是 ObjectView、ObjectFunction 或 ObjectProcedure（其余的种类，如 DumpTable，不影响次序）。
	Kind string `json:"kind"`
	// Name 的形式同 DumpObject.Name。
	Name string `json:"name"`
}

// DumpDialect 是驱动在转储里的部分：输入输出都能序列化，按整份文件一次、按对象一次调用（不按行），
// 扩展里的驱动可以经 ABI 提供它。顺序、数据的读取与写文件都在编排（Dump）里。
type DumpDialect interface {
	DumpFile(ctx context.Context, scope DumpScope) (*DumpFileScript, error)
	DumpObject(ctx context.Context, object DumpObject) (*DumpObjectScript, error)
}

// DumpSource 是转储读库用到的会话操作（Session 都有）。还实现了 TableStatementSource 的，表的数据由它自己读出、
// 写成语句（见 TableStatementSource）。
type DumpSource interface {
	ListTables(ctx context.Context, database string) ([]string, error)
	ListObjects(ctx context.Context, database string) (*DatabaseObjects, error)
	TableDDL(ctx context.Context, database, table string) (string, error)
	ObjectSource(ctx context.Context, database, objType, name, table string) (string, error)
	TableRowSource
}

// dumpChunkRows 是一块数据的行数：按块生成 INSERT、报进度、写批分隔符。
const dumpChunkRows = 500

// Dump 是转储的编排，各驱动的 Session.Dump 用它：依次写出注释头（含所选的导出选项）、方言的文件开头（勾了
// 「关闭外键检查」时接着关闭）、删除段（勾了「写入删除语句」时，见 dropSection）、类型、每张表（注释分隔行、
// 定义；结构和数据时接着是它的全部行，每行一条与「复制为 INSERT」一样的 INSERT，只写方言给出的列；表名不带
// 库名，文件导入哪个库数据就写进哪个库）、全部表之后的外键（勾了「包含外键」时）、视图与整库或整个 schema
// 时的函数和过程（同一段，只有定义，按依赖顺序，见 DumpObjectScript.Dependencies）、整库或整个 schema 时的
// 触发器（在所有数据之后，导入数据时不会触发它们；视图、函数与过程、触发器各自勾了时才有）、数据之后的收尾、
// 恢复外键检查与方言的文件结尾。行边读边写，不整表留在内存里。
//
// 每张表只读它自己的行（TableQuery.OwnRows：分区表的父表、继承的父表不带子表的行）；表的分区随它转储（见
// DumpObjectScript.Partitions），分区与继承的子表写在父表之后（见 DumpObjectScript.Dependencies）。
//
// 写任何内容之前先读出全部对象的定义、得到方言的语句（之后才取方言的整份文件部分，范围里已有随父表带上的
// 分区）：删除段在文件开头，要用到每个对象的删除语句。
// 任何一个对象出错（读定义、方言、读行）或 ctx 被取消都立即停下，返回的错误是点名那个对象的
// *DumpObjectError（被取消时它包着 ctx 的错误）；已经写出的内容由调用方丢弃（见 DumpToFile）。
func Dump(ctx context.Context, req DumpRequest, source DumpSource, dialect DumpDialect) (*DumpResult, error) {
	if !req.Whole && len(req.Tables) == 0 {
		return nil, errors.New("no tables to dump")
	}
	plan, err := planDump(ctx, req, source)
	if err != nil {
		return nil, err
	}
	d := &dump{ctx: ctx, req: req, source: source, dialect: dialect, out: &countingWriter{w: req.Writer}}
	if err := d.prepareAll(plan); err != nil {
		return &DumpResult{Objects: plan.objects()}, err
	}
	d.progress.Objects = plan.objects()
	file, err := dialect.DumpFile(ctx, DumpScope{
		Database: req.Database, Schema: req.Schema, Tables: plan.listed, WithData: req.WithData, Whole: req.Whole, Options: req.Options,
	})
	if err != nil {
		return &DumpResult{Objects: d.progress.Objects}, err
	}
	d.terminator = file.BatchTerminator
	err = d.run(file, plan)
	return &DumpResult{Objects: d.progress.Objects, Rows: d.progress.Rows, Bytes: d.out.n}, err
}

// dumpItem 是要写的一个对象。
type dumpItem struct {
	kind string
	name string
	// table 是触发器所在的表（ObjectMeta.Table）。
	table string
	// withData 表示接着写它的行（结构和数据时的表）。
	withData bool
	// script 是方言给出的语句（prepareAll 填上）。
	script *DumpObjectScript
}

// dumpPlan 是一次转储要写的对象，按段分开。
type dumpPlan struct {
	// listed 是转储的表与视图（交给方言的范围），含随父表带上的分区。
	listed []string
	// tables 是表：列出时按选中或列出的次序，随父表带上的分区接在后面；prepareAll 之后是父表在前的次序（见
	// dependencyOrder）。
	tables []*dumpItem
	// definitions 是视图，以及整库或整个 schema 时的函数与过程：列出时函数、过程在前，视图在后，各自按列出的
	// 次序；prepareAll 之后是依赖顺序（被依赖的在前，见 dependencyOrder）。
	definitions []*dumpItem
	// triggers 是整库或整个 schema 时的触发器。
	triggers []*dumpItem
}

// objects 是要写的对象数。
func (p *dumpPlan) objects() int { return len(p.tables) + len(p.definitions) + len(p.triggers) }

// planDump 列出要写的对象：选中的表与视图，或 Whole 时库里（schema 里）的全部对象——视图、函数与过程、
// 触发器各自只在勾了它们的选项时。
func planDump(ctx context.Context, req DumpRequest, source DumpSource) (*dumpPlan, error) {
	objects, err := source.ListObjects(ctx, req.Database)
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}
	plan := &dumpPlan{listed: slices.Clone(req.Tables)} // 随父表带上的分区接在后面，不能写进调用方的切片
	if req.Whole {
		all, err := source.ListTables(ctx, req.Database)
		if err != nil {
			return nil, fmt.Errorf("list tables: %w", err)
		}
		plan.listed = slices.DeleteFunc(all, func(name string) bool { return !inDumpSchema(req.Schema, name) })
		// 视图取自 ListObjects：ListTables 也列出的按它的次序，只有 ListObjects 知道的（ListTables 只列表的驱动）
		// 按 ListObjects 的次序接在后面。
		for _, view := range objects.Views {
			if inDumpSchema(req.Schema, view.Name) && !slices.Contains(plan.listed, view.Name) {
				plan.listed = append(plan.listed, view.Name)
			}
		}
	}
	isView := make(map[string]bool, len(objects.Views))
	for _, view := range objects.Views {
		isView[view.Name] = true
	}
	var views []*dumpItem
	for _, name := range plan.listed {
		if !isView[name] {
			plan.tables = append(plan.tables, &dumpItem{kind: DumpTable, name: name, withData: req.WithData})
		} else if !req.Whole || req.Options.Views {
			views = append(views, &dumpItem{kind: ObjectView, name: name})
		}
	}
	if req.Whole && req.Options.Routines {
		plan.definitions = slices.Concat(wholeItems(req.Schema, objects.Functions), wholeItems(req.Schema, objects.Procedures))
	}
	plan.definitions = append(plan.definitions, views...)
	if req.Whole && req.Options.Triggers {
		plan.triggers = wholeItems(req.Schema, objects.Triggers)
	}
	return plan, nil
}

// wholeItems 是整库或整个 schema 时要写的一种对象：schema 里的那些，按列出的次序。
func wholeItems(schema string, objects []ObjectMeta) []*dumpItem {
	var items []*dumpItem
	for _, object := range objects {
		if inDumpSchema(schema, object.Name) {
			items = append(items, &dumpItem{kind: object.Type, name: object.Name, table: object.Table})
		}
	}
	return items
}

// inDumpSchema 报告一个对象是否在要转储的 schema 里；schema 为空（整个库）时都在。
func inDumpSchema(schema, name string) bool {
	return schema == "" || strings.HasPrefix(name, schema+".")
}

// dump 是一次转储的状态。
type dump struct {
	ctx        context.Context
	req        DumpRequest
	source     DumpSource
	dialect    DumpDialect
	out        *countingWriter
	terminator string
	progress   DumpProgress
}

// prepareAll 读出全部对象的定义、得到方言的语句：表的分区（DumpObjectScript.Partitions）还不在这次转储里的随它
// 加进来，一样读出，给出了分区的表不写数据；再把表排成父表在前，视图、函数与过程排成被依赖的在前（见
// dependencyOrder）。
func (d *dump) prepareAll(plan *dumpPlan) error {
	tables := make(map[string]bool, len(plan.tables))
	for _, table := range plan.tables {
		tables[table.name] = true
	}
	for i := 0; i < len(plan.tables); i++ { // 分区加在后面，也在这一轮里读出
		table := plan.tables[i]
		if err := d.prepare(table); err != nil {
			return &DumpObjectError{Object: table.name, Err: err}
		}
		if len(table.script.Partitions) > 0 { // 分区表的行都在分区里
			table.withData = false
		}
		for _, partition := range table.script.Partitions {
			if !tables[partition] {
				tables[partition] = true
				plan.tables = append(plan.tables, &dumpItem{kind: DumpTable, name: partition, withData: d.req.WithData})
				plan.listed = append(plan.listed, partition)
			}
		}
	}
	for _, group := range [][]*dumpItem{plan.definitions, plan.triggers} {
		for _, item := range group {
			if err := d.prepare(item); err != nil {
				return &DumpObjectError{Object: item.name, Err: err}
			}
		}
	}
	plan.leaveOutUnwritten()
	plan.tables = dependencyOrder(plan.tables)
	plan.definitions = dependencyOrder(plan.definitions)
	return nil
}

// leaveOutUnwritten 去掉方言不转储的对象（给出的 Definition 为空，见 DumpObjectScript.Definition）：它们不在
// 文件里，也不在交给方言的范围与对象数里。范围（listed）里只有表与视图：同名的函数或过程（不共用名字空间的库）
// 不写时，表还在其中。
func (p *dumpPlan) leaveOutUnwritten() {
	unwritten := func(item *dumpItem) bool { return len(item.script.Definition) == 0 }
	for _, item := range slices.Concat(p.tables, p.definitions) {
		if unwritten(item) && (item.kind == DumpTable || item.kind == ObjectView) {
			p.listed = slices.DeleteFunc(p.listed, func(name string) bool { return name == item.name })
		}
	}
	p.tables = slices.DeleteFunc(p.tables, unwritten)
	p.definitions = slices.DeleteFunc(p.definitions, unwritten)
	p.triggers = slices.DeleteFunc(p.triggers, unwritten)
}

func (d *dump) run(file *DumpFileScript, plan *dumpPlan) error {
	options := d.req.Options
	d.header()
	opening, closing := file.Opening, file.Closing
	if options.DisableForeignKeyChecks {
		opening = slices.Concat(opening, file.DisableForeignKeyChecks)
		closing = slices.Concat(file.EnableForeignKeyChecks, closing)
	}
	d.section("", opening)
	if options.Drops {
		d.section("Drop existing objects", dropSection(file, plan))
	}
	d.section("", file.Types)
	for _, table := range plan.tables {
		if err := d.write(table); err != nil {
			return err
		}
	}
	if options.ForeignKeys {
		d.section("Foreign keys", collect(plan.tables, func(s *DumpObjectScript) []string { return s.ForeignKeys }))
	}
	for _, item := range slices.Concat(plan.definitions, plan.triggers) {
		if err := d.write(item); err != nil {
			return err
		}
	}
	d.section("Sequence and auto-increment values",
		collect(slices.Concat(plan.tables, plan.definitions, plan.triggers), func(s *DumpObjectScript) []string { return s.PostData }))
	d.section("", closing)
	return d.out.err
}

// dropSection 是删除段：先删表上的外键（不能关闭外键检查的方言才给出），再按依赖倒序删除要转储的对象——
// 触发器、视图与函数和过程（与写出的次序相反：依赖别的的先删）、表（与写出的次序相反：分区与继承的子表先于
// 父表），最后删类型。
func dropSection(file *DumpFileScript, plan *dumpPlan) []string {
	drops := collect(plan.tables, func(s *DumpObjectScript) []string { return s.DropForeignKeys })
	for _, group := range [][]*dumpItem{plan.triggers, plan.definitions, plan.tables} {
		drops = append(drops, collect(reversed(group), func(s *DumpObjectScript) []string { return s.Drop })...)
	}
	return append(drops, file.DropTypes...)
}

// collect 依次取出各对象语句里的一部分。
func collect(items []*dumpItem, part func(*DumpObjectScript) []string) []string {
	statements := make([]string, 0, len(items))
	for _, item := range items {
		statements = append(statements, part(item.script)...)
	}
	return statements
}

func reversed(items []*dumpItem) []*dumpItem {
	out := slices.Clone(items)
	slices.Reverse(out)
	return out
}

// optionNames 是注释头里列出的选项，按对话框里的次序；视图、函数与过程、触发器只在整库或整个 schema 时有。
var optionNames = []struct {
	name      string
	chosen    func(DumpOptions) bool
	wholeOnly bool
}{
	{"indexes", func(o DumpOptions) bool { return o.Indexes }, false},
	{"foreign keys", func(o DumpOptions) bool { return o.ForeignKeys }, false},
	{"views", func(o DumpOptions) bool { return o.Views }, true},
	{"functions and procedures", func(o DumpOptions) bool { return o.Routines }, true},
	{"triggers", func(o DumpOptions) bool { return o.Triggers }, true},
	{"drop statements", func(o DumpOptions) bool { return o.Drops }, false},
	{"disable foreign key checks", func(o DumpOptions) bool { return o.DisableForeignKeyChecks }, false},
}

func (d *dump) header() {
	content := "structure only"
	if d.req.WithData {
		content = "structure and data"
	}
	var chosen []string
	for _, option := range optionNames {
		if option.chosen(d.req.Options) && (d.req.Whole || !option.wholeOnly) {
			chosen = append(chosen, option.name)
		}
	}
	options := "none"
	if len(chosen) > 0 {
		options = strings.Join(chosen, ", ")
	}
	lines := []string{
		"OpsKat SQL Dump",
		"",
		"OpsKat version: " + d.req.Header.Version,
		"Asset: " + d.req.Header.Asset,
		"Database: " + d.req.Database,
	}
	if d.req.Schema != "" {
		lines = append(lines, "Schema: "+d.req.Schema)
	}
	lines = append(lines,
		"Content: "+content,
		"Options: "+options,
		"Date: "+d.req.Header.Time.Format("2006-01-02 15:04:05 -07:00"),
		"",
		"File encoding: UTF-8",
	)
	for _, line := range lines {
		d.comment(line)
	}
}

// comment 写一行注释。文字里的换行换成空格：名字里带换行时，后面的内容不能跑出注释。
func (d *dump) comment(text string) {
	text = strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
	if text == "" {
		d.out.write("--\n")
		return
	}
	d.out.write("-- " + text + "\n")
}

// banner 写一个对象或一段之前的注释分隔行（前面空一行）。
func (d *dump) banner(title string) {
	d.out.write("\n")
	d.comment("----------------------------")
	d.comment(title)
	d.comment("----------------------------")
}

// section 写一段语句；没有语句时什么也不写。title 为空时不写注释分隔行，只空一行。
func (d *dump) section(title string, statements []string) {
	if len(statements) == 0 {
		return
	}
	if title == "" {
		d.out.write("\n")
	} else {
		d.banner(title)
	}
	d.statements(statements)
}

// statements 写结构语句：每条之后写批分隔符（方言有的话）。
func (d *dump) statements(statements []string) {
	for _, statement := range statements {
		d.out.write(statement + "\n")
		d.batchEnd()
	}
}

func (d *dump) batchEnd() {
	if d.terminator != "" {
		d.out.write(d.terminator + "\n")
	}
}

// dependencyOrder 把一段对象排成被依赖的在前：每个对象之前先放它依赖的（Dependencies）里属于这一段的那些，
// 其余保持 items 的次序。表这一段里是分区与继承的子表依赖它们的父表；视图、函数与
// 过程那一段里没有依赖的函数、过程在前，视图在后。自己依赖自己不算；依赖成环时，回到正在放的对象的那条依赖
// 不算（环在那里断开，与 pg_dump 一样照样写出），环上的对象按放到的次序。
func dependencyOrder(items []*dumpItem) []*dumpItem {
	key := func(kind, name string) string { return kind + " " + name }
	byKey := make(map[string]*dumpItem, len(items))
	for _, item := range items {
		byKey[key(item.kind, item.name)] = item
	}
	dependencies := func(item *dumpItem) []*dumpItem {
		var out []*dumpItem
		for _, dependency := range item.script.Dependencies {
			if found, ok := byKey[key(dependency.Kind, dependency.Name)]; ok {
				out = append(out, found)
			}
		}
		return out
	}
	// started 是已经开始放的对象（含正在放、还在等它依赖的那些）：再遇到它时不再进去，自己依赖自己与成环都在这里断开。
	started := make(map[*dumpItem]bool, len(items))
	ordered := make([]*dumpItem, 0, len(items))
	var place func(item *dumpItem)
	place = func(item *dumpItem) {
		if started[item] {
			return
		}
		started[item] = true
		for _, dependency := range dependencies(item) {
			place(dependency)
		}
		ordered = append(ordered, item)
	}
	for _, item := range items {
		place(item)
	}
	return ordered
}

// prepare 读出一个对象的定义，交给方言得到它的语句。
func (d *dump) prepare(item *dumpItem) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	definition, err := d.definition(item)
	if err != nil {
		return err
	}
	if strings.TrimSpace(definition) == "" {
		return errors.New("the database gave no definition for it")
	}
	item.script, err = d.dialect.DumpObject(d.ctx, DumpObject{
		Database: d.req.Database, Kind: item.kind, Name: item.name, Table: item.table, Definition: definition,
		WithData: item.withData, Options: d.req.Options,
	})
	return err
}

// definition 是一个对象的定义原文：表与视图的来自 TableDDL，函数、过程与触发器的来自 ObjectSource。
func (d *dump) definition(item *dumpItem) (string, error) {
	if item.kind == DumpTable || item.kind == ObjectView {
		return d.source.TableDDL(d.ctx, d.req.Database, item.name)
	}
	return d.source.ObjectSource(d.ctx, d.req.Database, item.kind, item.name, item.table)
}

// structureTitles 是各种对象的注释分隔行。
var structureTitles = map[string]string{
	DumpTable:       "Table structure for ",
	ObjectView:      "View structure for ",
	ObjectFunction:  "Function structure for ",
	ObjectProcedure: "Procedure structure for ",
	ObjectTrigger:   "Trigger structure for ",
}

// write 写出一个对象的定义，withData 时接着是它的行；开始与写完时报告进度。
func (d *dump) write(item *dumpItem) error {
	if err := d.ctx.Err(); err != nil {
		return &DumpObjectError{Object: item.name, Err: err}
	}
	d.progress.Object++
	d.progress.Name = item.name
	d.report(nil)
	script := item.script
	d.banner(structureTitles[item.kind] + item.name)
	d.statements(script.Definition)
	var written int64
	if item.withData {
		d.banner("Records of " + item.name)
		d.statements(script.DataPrologue)
		var err error
		if written, err = d.rows(item.name, script); err != nil {
			return &DumpObjectError{Object: item.name, Err: err}
		}
		d.statements(script.DataEpilogue)
	}
	if d.out.err != nil {
		return &DumpObjectError{Object: item.name, Err: d.out.err}
	}
	d.report(&DumpedObject{Name: item.name, Kind: item.kind, Data: item.withData, Rows: written})
	return nil
}

// rows 写出一张表的全部行：会话能自己写语句（TableStatementSource）时用它（见 statementRows），否则读出行、按块
// 生成 INSERT 写出。writer 在游标打开之前备好：游标占着连接时（远端 SQLite 只有一条）不能再去查列类型。
func (d *dump) rows(table string, script *DumpObjectScript) (written int64, err error) {
	if source, ok := d.source.(TableStatementSource); ok {
		return d.statementRows(source, table, script)
	}
	writer, err := d.source.PrepareRowStatements(d.ctx, d.req.Database, table)
	if err != nil {
		return 0, err
	}
	stream, err := d.source.StreamTableRows(d.ctx, d.req.Database, table, TableQuery{OwnRows: true})
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	streamColumns := stream.Columns()
	columns := script.DataColumns
	if columns == nil {
		columns = streamColumns
	}
	chunk := make([]map[string]any, 0, dumpChunkRows)
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		statements, err := InsertStatements(writer, RowStatementsRequest{
			Kind: RowInsert, Columns: columns, OmitDatabase: true, InsertModifier: script.InsertModifier,
		}, chunk)
		if err != nil {
			return err
		}
		for _, statement := range statements {
			d.out.write(statement + "\n")
		}
		d.batchEnd()
		written += int64(len(chunk))
		d.progress.Rows += int64(len(chunk))
		chunk = chunk[:0]
		d.report(nil)
		if d.out.err != nil {
			return d.out.err
		}
		return d.ctx.Err()
	}
	for stream.Next() {
		chunk = append(chunk, rowMap(streamColumns, stream.Row()))
		if len(chunk) == dumpChunkRows {
			if err := flush(); err != nil {
				return written, err
			}
		}
	}
	if err := stream.Err(); err != nil {
		return written, err
	}
	return written, flush()
}

// statementRows 写出会话自己为一张表写的语句，一页一页地：每页的语句写完报一次进度（写完的行数），写完的行数到
// 一块（dumpChunkRows）时、以及最后一页之后（这一块还有行时）写批分隔符，与按块经 writer 写出的位置一样；页与
// 页之间被取消就停下。语句流在任何情况下都关掉。
func (d *dump) statementRows(source TableStatementSource, table string, script *DumpObjectScript) (written int64, err error) {
	pages, err := source.TableStatements(d.ctx, d.req.Database, table, TableStatementsRequest{
		Columns: script.DataColumns, InsertModifier: script.InsertModifier, OmitDatabase: true, OwnRows: true,
		PageBytes: DumpPageBytes, BatchRows: dumpChunkRows,
	})
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := pages.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	inBatch := 0
	for {
		page, err := pages.Next()
		if err != nil {
			return written, err
		}
		if len(page.Statements) == 0 && !page.Done {
			return written, errors.New("the database driver gave a page with no statements before the end of the table")
		}
		for _, statement := range page.Statements {
			d.out.write(statement + "\n")
		}
		inBatch += page.Rows
		if inBatch >= dumpChunkRows || (page.Done && inBatch > 0) {
			d.batchEnd()
			inBatch = 0
		}
		written += int64(page.Rows)
		d.progress.Rows += int64(page.Rows)
		if len(page.Statements) > 0 {
			d.report(nil)
		}
		if d.out.err != nil {
			return written, d.out.err
		}
		if page.Done {
			return written, nil
		}
		if err := d.ctx.Err(); err != nil {
			return written, err
		}
	}
}

// rowMap 是游标的一行按列名取值的样子（行语句请求里的行）。
func rowMap(columns []string, values []any) map[string]any {
	row := make(map[string]any, len(columns))
	for i, column := range columns {
		row[column] = values[i]
	}
	return row
}

// InsertStatements 生成转储里一块行的 INSERT（req 是除了行之外的请求，rows 是游标读出的样子，会被改写）。行先
// 转成前端拿到再带回来的样子（经 JSON：nil、字符串、数字、布尔），语句才与「复制为 INSERT」对这些行给出的一字
// 不差；二进制的值（见 binaryValue）、整数（见 ExactNumbers）与 JSON 写不出的浮点数（见 nonFiniteText）除外。
// 自己读行的 TableStatementSource 用它写出与转储经游标、writer 写出的一样的语句。
func InsertStatements(writer RowStatementWriter, req RowStatementsRequest, rows []map[string]any) ([]string, error) {
	for _, row := range rows {
		for column, value := range row {
			if text, ok := nonFiniteText(value); ok {
				row[column] = text
			}
		}
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("encode rows: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var sent []map[string]any
	if err := decoder.Decode(&sent); err != nil {
		return nil, fmt.Errorf("decode rows: %w", err)
	}
	for i, row := range rows {
		for column, value := range row {
			if raw, ok := binaryValue(value); ok {
				sent[i][column] = raw
				continue
			}
			if sent[i][column], err = ExactNumbers(sent[i][column]); err != nil {
				return nil, fmt.Errorf("decode rows: %w", err)
			}
		}
	}
	req.Rows = sent
	return writer.RowStatements(req)
}

// ExactNumbers 把经 JSON（json.Decoder.UseNumber）解出的数字（json.Number，列表里的也是）换回 writer 认得的
// 值：整数是 int64，不丢 2^53 以上的位（前端拿到的 float64 会丢，那是「复制为 INSERT」的事，转储要原值）；
// 其余是 float64，写法与「复制为 INSERT」一样。扩展 SDK 解宿主经 ABI 发来的行语句请求也用它，两边按同一套规则。
func ExactNumbers(value any) (any, error) {
	switch v := value.(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, nil
		}
		return v.Float64()
	case []any:
		for i, element := range v {
			var err error
			if v[i], err = ExactNumbers(element); err != nil {
				return nil, err
			}
		}
	}
	return value, nil
}

// nonFiniteText 认出 JSON 写不出的浮点数（NaN 与正负无穷，PostgreSQL 的 float 能存），给出浮点列认得的
// 文字 'NaN' / 'Infinity' / '-Infinity'：它们以字符串交给 writer，不让整个转储停在这张表上。
func nonFiniteText(value any) (string, bool) {
	var f float64
	switch v := value.(type) {
	case float64:
		f = v
	case float32:
		f = float64(v)
	default:
		return "", false
	}
	switch {
	case math.IsNaN(f):
		return "NaN", true
	case math.IsInf(f, 1):
		return "Infinity", true
	case math.IsInf(f, -1):
		return "-Infinity", true
	}
	return "", false
}

// binaryValue 认出不能经 JSON 往返的值：[]byte（JSON 把它变成 base64）与不是合法 UTF-8 的字符串（驱动把
// 二进制列读成字符串，JSON 会把非法字节换成 U+FFFD）。它们以字节交给 writer，写成二进制列的字面量；
// 「复制为 INSERT」读到的值在表格里已经是文本，这一点只有转储需要。
func binaryValue(value any) ([]byte, bool) {
	switch v := value.(type) {
	case []byte:
		return v, true
	case string:
		if !utf8.ValidString(v) {
			return []byte(v), true
		}
	}
	return nil, false
}

func (d *dump) report(finished *DumpedObject) {
	if d.req.Progress == nil {
		return
	}
	progress := d.progress
	progress.Finished = finished
	d.req.Progress(progress)
}

// countingWriter 记下写出的字节数，并记住第一个写错误（之后不再写）。
type countingWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countingWriter) write(s string) {
	if c.err != nil {
		return
	}
	n, err := io.WriteString(c.w, s)
	c.n += int64(n)
	if err != nil {
		c.err = fmt.Errorf("write dump: %w", err)
	}
}

// dumpBufferSize 是写文件时的缓冲大小。
const dumpBufferSize = 256 << 10

// DumpToFile 经 session.Dump 把转储写到 path：先写进同一目录下的临时文件，写完、落盘后才换成 path。
// 出错或被取消时删掉临时文件，path 保持开始前的样子（原来没有就仍然没有，原来有就仍是原来的内容）。
// req.Writer 由它给出。
func DumpToFile(ctx context.Context, session Session, req DumpRequest, path string) (*DumpResult, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create dump file: %w", err)
	}
	buffered := bufio.NewWriterSize(tmp, dumpBufferSize)
	req.Writer = buffered
	result, err := session.Dump(ctx, req)
	if err == nil {
		err = buffered.Flush()
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		if removeErr := os.Remove(tmp.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove unfinished dump: %w", removeErr))
		}
		return result, err
	}
	return result, nil
}
