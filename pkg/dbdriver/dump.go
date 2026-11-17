package dbdriver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// Whole 为真时转储整个库（Schema 为空）或整个 schema 的全部对象：ListTables 给出的表与视图，以及
	// ListObjects 给出的函数、过程与触发器。有 schema 分组的驱动给的名字是「schema.名字」（同 ListTables），
	// 整个 schema 就是以「Schema.」开头的那些。
	Whole bool
	// Header 是注释头里写的来源。
	Header DumpHeader
	// Writer 是写出的目标。
	Writer io.Writer
	// Progress 在每个对象开始、写完以及读行的中途被调用（同一个 goroutine 上，按先后）；nil 表示不需要。
	Progress func(DumpProgress)
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
}

// DumpFileScript 是方言给出的、整份文件一次的部分。每条语句都是写进文件的原文（含结束符），一条
// 写一行。
type DumpFileScript struct {
	// Opening 写在注释头之后，如 SET NAMES、关闭外键检查。
	Opening []string `json:"opening,omitempty"`
	// Types 写在第一张表之前：表的列用到的类型定义（如 PostgreSQL 的枚举）。
	Types []string `json:"types,omitempty"`
	// Closing 写在文件末尾，如恢复外键检查。
	Closing []string `json:"closing,omitempty"`
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
}

// DumpObjectScript 是方言为一个对象给出的语句：每条都是写进文件的原文（含结束符），一条写一行。
type DumpObjectScript struct {
	// Drop 是「存在则删除」，写在定义之前。
	Drop []string `json:"drop"`
	// Definition 是建出它的语句，已按方言包好（如 MySQL 的 DELIMITER、Oracle 的 /），索引等附属定义
	// 也在其中。
	Definition []string `json:"definition"`
	// DataColumns 是 INSERT 写入的列，按次序（生成列 / 计算列不在其中）；nil 表示读出的全部列。
	DataColumns []string `json:"dataColumns"`
	// DataPrologue / DataEpilogue 写在它的行之前 / 之后（如 SQL Server 的 IDENTITY_INSERT 开、关）。
	DataPrologue []string `json:"dataPrologue,omitempty"`
	DataEpilogue []string `json:"dataEpilogue,omitempty"`
	// ForeignKeys 写在全部表与数据之后：不能全局关闭外键检查的库在这里加外键（定义里就不带）。
	ForeignKeys []string `json:"foreignKeys,omitempty"`
	// PostData 写在全部对象之后：序列与自增的收尾（如 PostgreSQL 的 setval）。
	PostData []string `json:"postData,omitempty"`
	// DependsOn 是它的定义用到的对象（名字的形式同 DumpObject.Name）：视图按它排序，被依赖的视图先写。
	// 不是这次转储里的视图的名字（如表）不影响次序；不给时视图按列出的次序写。
	DependsOn []string `json:"dependsOn,omitempty"`
}

// DumpDialect 是驱动在转储里的部分：输入输出都能序列化，按整份文件一次、按对象一次调用（不按行），
// 扩展里的驱动可以经 ABI 提供它。顺序、数据的读取与写文件都在编排（Dump）里。
type DumpDialect interface {
	DumpFile(ctx context.Context, scope DumpScope) (*DumpFileScript, error)
	DumpObject(ctx context.Context, object DumpObject) (*DumpObjectScript, error)
}

// DumpSource 是转储读库用到的会话操作（Session 都有）。
type DumpSource interface {
	ListTables(ctx context.Context, database string) ([]string, error)
	ListObjects(ctx context.Context, database string) (*DatabaseObjects, error)
	TableDDL(ctx context.Context, database, table string) (string, error)
	ObjectSource(ctx context.Context, database, objType, name, table string) (string, error)
	PrepareRowStatements(ctx context.Context, database, table string) (RowStatementWriter, error)
	StreamTableRows(ctx context.Context, database, table string, query TableQuery) (RowStream, error)
}

// dumpChunkRows 是一块数据的行数：按块生成 INSERT、报进度、写批分隔符。
const dumpChunkRows = 500

// Dump 是转储的编排，各驱动的 Session.Dump 用它：依次写出注释头、方言的文件开头与类型、每张表（注释
// 分隔行、存在则删除、定义；结构和数据时接着是它的全部行，每行一条与「复制为 INSERT」一样的 INSERT，
// 只写方言给出的列；表名不带库名，文件导入哪个库数据就写进哪个库）、全部表之后的外键、视图（只有定义，按依赖顺序）、整库或整个 schema 时的函数、过程
// 与触发器（在所有数据之后，导入数据时不会触发它们）、数据之后的收尾与方言的文件结尾。行边读边写，不整表
// 留在内存里。
//
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
	file, err := dialect.DumpFile(ctx, DumpScope{
		Database: req.Database, Schema: req.Schema, Tables: plan.listed, WithData: req.WithData, Whole: req.Whole,
	})
	if err != nil {
		return nil, err
	}
	d := &dump{ctx: ctx, req: req, source: source, dialect: dialect, out: &countingWriter{w: req.Writer}, terminator: file.BatchTerminator}
	d.progress.Objects = len(plan.tables) + len(plan.views) + len(plan.routines)
	err = d.run(file, plan)
	return &DumpResult{Objects: d.progress.Objects, Rows: d.progress.Rows, Bytes: d.out.n}, err
}

// dumpItem 是要写的一个对象。
type dumpItem struct {
	kind string
	name string
	// table 是触发器所在的表（ObjectMeta.Table）。
	table string
}

// dumpPlan 是一次转储要写的对象，按段分开。
type dumpPlan struct {
	// listed 是转储的表与视图（交给方言的范围）。
	listed []string
	tables []dumpItem
	views  []dumpItem
	// routines 是整库或整个 schema 时的函数、过程与触发器，按写出的次序。
	routines []dumpItem
}

// planDump 列出要写的对象：选中的表与视图，或 Whole 时库里（schema 里）的全部对象。
func planDump(ctx context.Context, req DumpRequest, source DumpSource) (*dumpPlan, error) {
	objects, err := source.ListObjects(ctx, req.Database)
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}
	plan := &dumpPlan{listed: req.Tables}
	if req.Whole {
		all, err := source.ListTables(ctx, req.Database)
		if err != nil {
			return nil, fmt.Errorf("list tables: %w", err)
		}
		plan.listed = slices.DeleteFunc(all, func(name string) bool { return !inDumpSchema(req.Schema, name) })
	}
	for _, name := range plan.listed {
		if slices.ContainsFunc(objects.Views, func(view ObjectMeta) bool { return view.Name == name }) {
			plan.views = append(plan.views, dumpItem{kind: ObjectView, name: name})
		} else {
			plan.tables = append(plan.tables, dumpItem{kind: DumpTable, name: name})
		}
	}
	if !req.Whole {
		return plan, nil
	}
	for _, group := range [][]ObjectMeta{objects.Functions, objects.Procedures, objects.Triggers} {
		for _, object := range group {
			if inDumpSchema(req.Schema, object.Name) {
				plan.routines = append(plan.routines, dumpItem{kind: object.Type, name: object.Name, table: object.Table})
			}
		}
	}
	return plan, nil
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
	// foreignKeys / postData 是各表攒到全部表之后、全部对象之后才写的语句。
	foreignKeys []string
	postData    []string
}

func (d *dump) run(file *DumpFileScript, plan *dumpPlan) error {
	d.header()
	d.section("", file.Opening)
	d.section("", file.Types)
	for _, table := range plan.tables {
		if err := d.object(table, d.req.WithData); err != nil {
			return err
		}
	}
	d.section("Foreign keys", d.foreignKeys)
	if err := d.views(plan.views); err != nil {
		return err
	}
	for _, routine := range plan.routines {
		if err := d.object(routine, false); err != nil {
			return err
		}
	}
	d.section("Sequence and auto-increment values", d.postData)
	d.section("", file.Closing)
	return d.out.err
}

func (d *dump) header() {
	content := "structure only"
	if d.req.WithData {
		content = "structure and data"
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

// object 写一个对象：注释分隔行、存在则删除、定义，withData 时接着是它的行。
func (d *dump) object(item dumpItem, withData bool) error {
	d.begin(item)
	script, err := d.prepare(item, withData)
	if err != nil {
		return &DumpObjectError{Object: item.name, Err: err}
	}
	return d.write(item, script, withData)
}

// views 写视图：先读出全部视图的定义、得到方言的语句，再按依赖顺序写出。
func (d *dump) views(views []dumpItem) error {
	scripts := make(map[string]*DumpObjectScript, len(views))
	for _, view := range views {
		script, err := d.prepare(view, false)
		if err != nil {
			return &DumpObjectError{Object: view.name, Err: err}
		}
		scripts[view.name] = script
	}
	for _, view := range dependencyOrder(views, scripts) {
		d.begin(view)
		if err := d.write(view, scripts[view.name], false); err != nil {
			return err
		}
	}
	return nil
}

// dependencyOrder 把视图排成被依赖的在前：每个视图之前先放它 DependsOn 里属于这批视图的那些，其余保持
// 列出的次序。每个视图只放一次，所以依赖成环时也会停下。
func dependencyOrder(views []dumpItem, scripts map[string]*DumpObjectScript) []dumpItem {
	byName := make(map[string]dumpItem, len(views))
	for _, view := range views {
		byName[view.name] = view
	}
	placed := make(map[string]bool, len(views))
	ordered := make([]dumpItem, 0, len(views))
	var place func(view dumpItem)
	place = func(view dumpItem) {
		if placed[view.name] {
			return
		}
		placed[view.name] = true
		for _, name := range scripts[view.name].DependsOn {
			if dependency, ok := byName[name]; ok {
				place(dependency)
			}
		}
		ordered = append(ordered, view)
	}
	for _, view := range views {
		place(view)
	}
	return ordered
}

// prepare 读出一个对象的定义，交给方言得到它的语句。
func (d *dump) prepare(item dumpItem, withData bool) (*DumpObjectScript, error) {
	if err := d.ctx.Err(); err != nil {
		return nil, err
	}
	definition, err := d.definition(item)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(definition) == "" {
		return nil, errors.New("the database gave no definition for it")
	}
	return d.dialect.DumpObject(d.ctx, DumpObject{
		Database: d.req.Database, Kind: item.kind, Name: item.name, Table: item.table, Definition: definition, WithData: withData,
	})
}

// definition 是一个对象的定义原文：表与视图的来自 TableDDL，函数、过程与触发器的来自 ObjectSource。
func (d *dump) definition(item dumpItem) (string, error) {
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

// begin 报告开始写一个对象。
func (d *dump) begin(item dumpItem) {
	d.progress.Object++
	d.progress.Name = item.name
	d.report(nil)
}

// write 写出一个对象的语句，withData 时接着是它的行，写完时报告它写了什么。
func (d *dump) write(item dumpItem, script *DumpObjectScript, withData bool) error {
	d.banner(structureTitles[item.kind] + item.name)
	d.statements(script.Drop)
	d.statements(script.Definition)
	d.foreignKeys = append(d.foreignKeys, script.ForeignKeys...)
	d.postData = append(d.postData, script.PostData...)
	var written int64
	if withData {
		d.banner("Records of " + item.name)
		d.statements(script.DataPrologue)
		var err error
		if written, err = d.rows(item.name, script.DataColumns); err != nil {
			return &DumpObjectError{Object: item.name, Err: err}
		}
		d.statements(script.DataEpilogue)
	}
	if d.out.err != nil {
		return &DumpObjectError{Object: item.name, Err: d.out.err}
	}
	d.report(&DumpedObject{Name: item.name, Kind: item.kind, Data: withData, Rows: written})
	return nil
}

// rows 读出一张表的全部行，按块生成 INSERT 写出。writer 在游标打开之前备好：游标占着连接时（远端
// SQLite 只有一条）不能再去查列类型。
func (d *dump) rows(table string, columns []string) (written int64, err error) {
	writer, err := d.source.PrepareRowStatements(d.ctx, d.req.Database, table)
	if err != nil {
		return 0, err
	}
	stream, err := d.source.StreamTableRows(d.ctx, d.req.Database, table, TableQuery{})
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	streamColumns := stream.Columns()
	if columns == nil {
		columns = streamColumns
	}
	chunk := make([]map[string]any, 0, dumpChunkRows)
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		statements, err := insertStatements(writer, columns, chunk)
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
		values := stream.Row()
		row := make(map[string]any, len(streamColumns))
		for i, column := range streamColumns {
			row[column] = values[i]
		}
		chunk = append(chunk, row)
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

// insertStatements 生成一块行的 INSERT。行先转成前端拿到再带回来的样子（经 JSON：nil、字符串、数字、
// 布尔），语句才与「复制为 INSERT」对这些行给出的一字不差；二进制的值除外（见 binaryValue）。
func insertStatements(writer RowStatementWriter, columns []string, rows []map[string]any) ([]string, error) {
	data, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("encode rows: %w", err)
	}
	var sent []map[string]any
	if err := json.Unmarshal(data, &sent); err != nil {
		return nil, fmt.Errorf("decode rows: %w", err)
	}
	for i, row := range rows {
		for column, value := range row {
			if raw, ok := binaryValue(value); ok {
				sent[i][column] = raw
			}
		}
	}
	return writer.RowStatements(RowStatementsRequest{Kind: RowInsert, Columns: columns, Rows: sent, OmitDatabase: true})
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
