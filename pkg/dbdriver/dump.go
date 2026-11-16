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
)

// DumpTable 是转储里表的种类；视图用 ObjectView（以后整库转储的过程、函数、触发器用对应的 Object*）。
const DumpTable = "table"

// DumpRequest 是 Session.Dump 要写出的一份 SQL 转储。
type DumpRequest struct {
	// Database 是对象所在的库，含义同 Session 的其他操作。
	Database string
	// Schema 是转储的 schema（有 schema 分组的驱动），只写进注释头、交给方言；转储选中的表时为空。
	Schema string
	// Tables 是要转储的表与视图，用 ListTables 给出的形式，按用户选中的次序。
	Tables []string
	// WithData 为真时每张表的结构之后写出全部行（视图只写定义）。
	WithData bool
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
	// Objects 是这次要写的对象数（表与视图），Object 是正在写的第几个（从 1 起），Name 是它的名字。
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
	Database string   `json:"database"`
	Schema   string   `json:"schema,omitempty"`
	Tables   []string `json:"tables"`
	WithData bool     `json:"withData"`
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
	// Kind 是 DumpTable 或 ObjectView。
	Kind string `json:"kind"`
	// Name 用 ListTables 给出的形式。
	Name string `json:"name"`
	// Definition 是会话给出的定义原文（TableDDL）。
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
}

// DumpDialect 是驱动在转储里的部分：输入输出都能序列化，按整份文件一次、按对象一次调用（不按行），
// 扩展里的驱动可以经 ABI 提供它。顺序、数据的读取与写文件都在编排（Dump）里。
type DumpDialect interface {
	DumpFile(ctx context.Context, scope DumpScope) (*DumpFileScript, error)
	DumpObject(ctx context.Context, object DumpObject) (*DumpObjectScript, error)
}

// DumpSource 是转储读库用到的会话操作（Session 都有）。
type DumpSource interface {
	ListObjects(ctx context.Context, database string) (*DatabaseObjects, error)
	TableDDL(ctx context.Context, database, table string) (string, error)
	PrepareRowStatements(ctx context.Context, database, table string) (RowStatementWriter, error)
	StreamTableRows(ctx context.Context, database, table string, query TableQuery) (RowStream, error)
}

// dumpChunkRows 是一块数据的行数：按块生成 INSERT、报进度、写批分隔符。
const dumpChunkRows = 500

// Dump 是转储的编排，各驱动的 Session.Dump 用它：依次写出注释头、方言的文件开头与类型、每张表（注释
// 分隔行、存在则删除、定义；结构和数据时接着是它的全部行，每行一条与「复制为 INSERT」一样的 INSERT，
// 只写方言给出的列）、全部表之后的外键、视图（只有定义）、数据之后的收尾与方言的文件结尾。行边读边写，
// 不整表留在内存里。
//
// 任何一个对象出错（读定义、方言、读行）或 ctx 被取消都立即停下，返回的错误是点名那个对象的
// *DumpObjectError（被取消时它包着 ctx 的错误）；已经写出的内容由调用方丢弃（见 DumpToFile）。
func Dump(ctx context.Context, req DumpRequest, source DumpSource, dialect DumpDialect) (*DumpResult, error) {
	if len(req.Tables) == 0 {
		return nil, errors.New("no tables to dump")
	}
	objects, err := source.ListObjects(ctx, req.Database)
	if err != nil {
		return nil, fmt.Errorf("list views: %w", err)
	}
	var tables, views []string
	for _, name := range req.Tables {
		if slices.ContainsFunc(objects.Views, func(view ObjectMeta) bool { return view.Name == name }) {
			views = append(views, name)
		} else {
			tables = append(tables, name)
		}
	}
	file, err := dialect.DumpFile(ctx, DumpScope{Database: req.Database, Schema: req.Schema, Tables: req.Tables, WithData: req.WithData})
	if err != nil {
		return nil, err
	}
	d := &dump{ctx: ctx, req: req, source: source, dialect: dialect, out: &countingWriter{w: req.Writer}, terminator: file.BatchTerminator}
	d.progress.Objects = len(tables) + len(views)
	err = d.run(file, tables, views)
	return &DumpResult{Objects: d.progress.Objects, Rows: d.progress.Rows, Bytes: d.out.n}, err
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

func (d *dump) run(file *DumpFileScript, tables, views []string) error {
	d.header()
	d.section("", file.Opening)
	d.section("", file.Types)
	for _, table := range tables {
		if err := d.object(DumpTable, table, d.req.WithData); err != nil {
			return err
		}
	}
	d.section("Foreign keys", d.foreignKeys)
	for _, view := range views {
		if err := d.object(ObjectView, view, false); err != nil {
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
func (d *dump) object(kind, name string, withData bool) error {
	d.progress.Object++
	d.progress.Name = name
	d.report(nil)
	written, err := d.writeObject(kind, name, withData)
	if err != nil {
		return &DumpObjectError{Object: name, Err: err}
	}
	d.report(&DumpedObject{Name: name, Kind: kind, Data: withData, Rows: written})
	return nil
}

func (d *dump) writeObject(kind, name string, withData bool) (int64, error) {
	if err := d.ctx.Err(); err != nil {
		return 0, err
	}
	definition, err := d.source.TableDDL(d.ctx, d.req.Database, name)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(definition) == "" {
		return 0, errors.New("the database gave no definition for it")
	}
	script, err := d.dialect.DumpObject(d.ctx, DumpObject{
		Database: d.req.Database, Kind: kind, Name: name, Definition: definition, WithData: withData,
	})
	if err != nil {
		return 0, err
	}
	structure := "Table structure for "
	if kind == ObjectView {
		structure = "View structure for "
	}
	d.banner(structure + name)
	d.statements(script.Drop)
	d.statements(script.Definition)
	d.foreignKeys = append(d.foreignKeys, script.ForeignKeys...)
	d.postData = append(d.postData, script.PostData...)
	if !withData {
		return 0, d.out.err
	}
	d.banner("Records of " + name)
	d.statements(script.DataPrologue)
	written, err := d.rows(name, script.DataColumns)
	if err != nil {
		return written, err
	}
	d.statements(script.DataEpilogue)
	return written, d.out.err
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
// 布尔），语句才与「复制为 INSERT」对这些行给出的一字不差。
func insertStatements(writer RowStatementWriter, columns []string, rows []map[string]any) ([]string, error) {
	data, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("encode rows: %w", err)
	}
	var sent []map[string]any
	if err := json.Unmarshal(data, &sent); err != nil {
		return nil, fmt.Errorf("decode rows: %w", err)
	}
	return writer.RowStatements(RowStatementsRequest{Kind: RowInsert, Columns: columns, Rows: sent})
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
