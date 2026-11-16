package sqldb

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// triggerSyntax 是带触发器体的语法（像 SQLite）：四种引号，CREATE TRIGGER 到「; END;」才结束。
var triggerSyntax = ScriptSyntax{
	Quotes:        []ScriptQuote{{Open: '\'', Close: '\''}, {Open: '"', Close: '"'}, {Open: '`', Close: '`'}, {Open: '[', Close: ']'}},
	TriggerBodies: true,
}

// splitRunner 用 SplitScript 切分，执行器只记下语句：经 dbdriver.RunScript 的编排切分整个文件，
// 证明切分与读取块怎么切无关。
type splitRunner struct {
	syntax   ScriptSyntax
	executed []string
}

func (r *splitRunner) SplitScript(_ context.Context, chunk dbdriver.ScriptChunk) (*dbdriver.ScriptSplit, error) {
	return SplitScript(r.syntax, chunk)
}

func (r *splitRunner) OpenScriptExecutor(context.Context, string) (dbdriver.ScriptExecutor, error) {
	return recordingExecutor{r}, nil
}

type recordingExecutor struct{ r *splitRunner }

func (e recordingExecutor) ExecuteScriptBatch(_ context.Context, batch dbdriver.ScriptBatch) (*dbdriver.BatchResult, error) {
	result := &dbdriver.BatchResult{}
	for i, statement := range batch.Statements {
		e.r.executed = append(e.r.executed, statement)
		result.Executed = append(result.Executed, dbdriver.BatchStatement{Index: i, Error: "recorded"})
	}
	return result, nil
}

func (recordingExecutor) Close() error { return nil }

// splitAll 经编排切分整个脚本（每次读 readSize 字节），返回切出的语句。执行器把每条都记为失败、
// 运行继续，所以失败的计数就是语句数。
func splitAll(t *testing.T, syntax ScriptSyntax, script string, readSize int) []string {
	t.Helper()
	runner := &splitRunner{syntax: syntax}
	result, err := dbdriver.RunScript(context.Background(), dbdriver.ScriptRequest{
		Script: strings.NewReader(script), Size: int64(len(script)), ContinueOnError: true, ReadSize: readSize,
	}, runner)
	require.NoError(t, err)
	require.EqualValues(t, len(runner.executed), result.Failed)
	return runner.executed
}

const triggerScript = `-- 文件头注释; 不是语句
/* 多行
   注释; 也不是 */
CREATE TABLE "a;b" (id INTEGER PRIMARY KEY, note TEXT DEFAULT 'it''s; fine', [x;y] TEXT, ` + "`q;r`" + ` TEXT);
INSERT INTO "a;b" (note) VALUES ('semi;colon'), ('中文;分号'); -- 行尾注释;
CREATE TRIGGER trg AFTER INSERT ON "a;b"
BEGIN
  UPDATE "a;b" SET note = CASE WHEN NEW.note = 'x' THEN 'y;' ELSE NEW.note END WHERE id = NEW.id;
  INSERT INTO log VALUES (1);
END;
create temp trigger if not exists t2 before delete on log begin select raise(abort, 'no;'); end;
SELECT 1;;
SELECT 'no terminator at the end'
-- trailing comment only`

var triggerStatements = []string{
	`CREATE TABLE "a;b" (id INTEGER PRIMARY KEY, note TEXT DEFAULT 'it''s; fine', [x;y] TEXT, ` + "`q;r`" + ` TEXT)`,
	`INSERT INTO "a;b" (note) VALUES ('semi;colon'), ('中文;分号')`,
	"CREATE TRIGGER trg AFTER INSERT ON \"a;b\"\nBEGIN\n  UPDATE \"a;b\" SET note = CASE WHEN NEW.note = 'x' THEN 'y;' ELSE NEW.note END WHERE id = NEW.id;\n  INSERT INTO log VALUES (1);\nEND",
	"create temp trigger if not exists t2 before delete on log begin select raise(abort, 'no;'); end",
	"SELECT 1",
	"SELECT 'no terminator at the end'\n-- trailing comment only",
}

// 引号、注释里的分号不结束语句；触发器体到「; END;」才结束（体里 CASE ... END 不算）；空语句
// 跳过；文件结尾没有分号的语句照样给出。
func TestSplitScriptHonoursQuotesCommentsAndTriggerBodies(t *testing.T) {
	assert.Equal(t, triggerStatements, splitAll(t, triggerSyntax, triggerScript, len(triggerScript)+1))
}

// 不管文件按多大的块读取（一个语句、一个引号、一个注释、一个多字节字符被切在哪里），切出的语句
// 都一样。
func TestSplitScriptIsIndependentOfReadChunkBoundaries(t *testing.T) {
	for readSize := 1; readSize <= len(triggerScript); readSize++ {
		require.Equal(t, triggerStatements, splitAll(t, triggerSyntax, triggerScript, readSize), "read size %d", readSize)
	}
}

// 不是文件结尾时，没写完的语句（含没闭合的引号、注释）不被用掉，留给下一次。
func TestSplitScriptLeavesAnUnfinishedStatementForTheNextChunk(t *testing.T) {
	for name, tail := range map[string]string{
		"open statement":              "INSERT INTO t VALUES (1",
		"open quote":                  "INSERT INTO t VALUES ('a;",
		"open comment":                "/* still; open",
		"a quote that may be doubled": "INSERT INTO t VALUES ('it'",
		"open trigger body":           "CREATE TRIGGER x AFTER INSERT ON t BEGIN SELECT 1;",
	} {
		text := "SELECT 1;\n" + tail
		split, err := SplitScript(triggerSyntax, dbdriver.ScriptChunk{Text: text})
		require.NoError(t, err, name)
		assert.Equal(t, []dbdriver.ScriptStatement{{Text: "SELECT 1", Offset: 0}}, split.Statements, name)
		assert.Equal(t, len("SELECT 1;"), split.Consumed, name)
	}
}

// 语句从它的第一个记号算起：前面的空白与注释不算，行号才指向语句本身。
func TestSplitScriptStartsAStatementAtItsFirstToken(t *testing.T) {
	text := "SELECT 1; -- c\n  /* d */ SELECT 2;"
	split, err := SplitScript(triggerSyntax, dbdriver.ScriptChunk{Text: text, Final: true})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ScriptStatement{
		{Text: "SELECT 1", Offset: 0},
		{Text: "SELECT 2", Offset: strings.Index(text, "SELECT 2")},
	}, split.Statements)
	assert.Equal(t, len(text), split.Consumed)
}

// mysqlSyntax 像 MySQL：反斜杠转义的字符串、# 与「-- 」注释、/*! */ 版本注释、DELIMITER。
var mysqlSyntax = ScriptSyntax{
	Quotes:             []ScriptQuote{{Open: '\'', Close: '\'', Backslash: true}, {Open: '"', Close: '"', Backslash: true}, {Open: '`', Close: '`'}},
	HashComments:       true,
	SpacedDashComments: true,
	ExecutableComments: true,
	Delimiter:          true,
}

// pgSyntax 像 PostgreSQL：$tag$ 字符串、E'...' 转义、嵌套注释、BEGIN ATOMIC 函数体、psql 才有的写法。
var pgSyntax = ScriptSyntax{
	Quotes:         []ScriptQuote{{Open: '\'', Close: '\''}, {Open: '"', Close: '"'}},
	AtomicBodies:   true,
	NestedComments: true,
	DollarQuotes:   true,
	EscapeStrings:  true,
	PsqlCommands:   true,
}

// batchSyntax 像 SQL Server：GO 分批，分号不切分。
var batchSyntax = ScriptSyntax{
	Quotes:         []ScriptQuote{{Open: '\'', Close: '\''}, {Open: '[', Close: ']'}},
	NestedComments: true,
	BatchSeparator: "GO",
}

// splitEverySize 在每一种读取块大小下切分 script，切出的语句都必须是 want。
func splitEverySize(t *testing.T, syntax ScriptSyntax, script string, want []string) {
	t.Helper()
	for readSize := 1; readSize <= len(script); readSize++ {
		require.Equal(t, want, splitAll(t, syntax, script, readSize), "read size %d", readSize)
	}
}

// DELIMITER 一行换掉之后语句的结束符，这一行本身不是语句；结束符可以紧跟在词后面（END$$）、可以换回
// 分号；不在语句开头的 DELIMITER 只是一个词。换过的结束符跨读取块保持。
func TestSplitScriptFollowsDelimiterCommands(t *testing.T) {
	script := "SELECT 1;\n" +
		"DELIMITER $$\n" +
		"CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 'x$$'; END$$\n" +
		"delimiter //  trailing words are ignored\n" +
		"CREATE TRIGGER t BEFORE INSERT ON a FOR EACH ROW BEGIN SET @x = 1; END //\n" +
		"SELECT 2; SELECT 3 //\n" +
		"DELIMITER ;\n" +
		"SELECT 4; SELECT delimiter FROM t;\n" +
		"SELECT 5"
	splitEverySize(t, mysqlSyntax, script, []string{
		"SELECT 1",
		"CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 'x$$'; END",
		"CREATE TRIGGER t BEFORE INSERT ON a FOR EACH ROW BEGIN SET @x = 1; END",
		"SELECT 2; SELECT 3",
		"SELECT 4",
		"SELECT delimiter FROM t",
		"SELECT 5",
	})
}

// 当前的结束符经 State 带到下一次切分：上一次换成 ;; 的，下一次照样以 ;; 结束语句；换回分号时 State
// 为空。
func TestSplitScriptCarriesTheDelimiterInItsState(t *testing.T) {
	split, err := SplitScript(mysqlSyntax, dbdriver.ScriptChunk{Text: "DELIMITER ;;\nBEGIN; END;;\nSELECT 1;"})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ScriptStatement{{Text: "BEGIN; END", Offset: len("DELIMITER ;;\n")}}, split.Statements)
	assert.Equal(t, ";;", split.State)
	assert.Equal(t, len("DELIMITER ;;\nBEGIN; END;;"), split.Consumed)

	split, err = SplitScript(mysqlSyntax, dbdriver.ScriptChunk{Text: "\nSELECT 1; SELECT 2;;\nDELIMITER ;\nSELECT 3;", State: ";;", Final: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"SELECT 1; SELECT 2", "SELECT 3"}, statementTexts(split))
	assert.Empty(t, split.State)

	_, err = SplitScript(mysqlSyntax, dbdriver.ScriptChunk{Text: "DELIMITER \nSELECT 1;", Final: true})
	require.Error(t, err)
}

func statementTexts(split *dbdriver.ScriptSplit) []string {
	texts := make([]string, 0, len(split.Statements))
	for _, statement := range split.Statements {
		texts = append(texts, statement.Text)
	}
	return texts
}

// MySQL 的词法：反斜杠转义引号与反斜杠本身；# 与「-- 」是注释，--1 不是；/*! */ 与 /*+ */ 属于语句，
// 普通的 /* */ 不属于；语句只由版本注释构成时照样给出。
func TestSplitScriptHonoursMySQLLexing(t *testing.T) {
	script := "INSERT INTO t VALUES ('it\\'s; \\\\', \"a\\\";b\");\n" +
		"# hash; comment\n" +
		"SELECT 1--1;\n" +
		"-- dash; comment\n" +
		"/*!40101 SET NAMES utf8; */;\n" +
		"SELECT /*+ MAX_EXECUTION_TIME(1); */ 2 /* plain; */;\n" +
		"SELECT `a;b`;"
	splitEverySize(t, mysqlSyntax, script, []string{
		"INSERT INTO t VALUES ('it\\'s; \\\\', \"a\\\";b\")",
		"SELECT 1--1",
		"/*!40101 SET NAMES utf8; */",
		"SELECT /*+ MAX_EXECUTION_TIME(1); */ 2 /* plain; */",
		"SELECT `a;b`",
	})
}

// PostgreSQL 的词法：$$ 与 $tag$ 字符串里的分号（包括别的 tag）不结束语句，$1 参数与含 $ 的标识符
// 不是字符串；E'...' 里反斜杠转义，普通字符串里的反斜杠就是字符；注释可以嵌套。
func TestSplitScriptHonoursPostgreSQLLexing(t *testing.T) {
	script := "CREATE FUNCTION f() RETURNS text AS $body$ SELECT $$a;b$$; $body$ LANGUAGE sql;\n" +
		"PREPARE q AS SELECT $1, a$b$c FROM t;\n" +
		"SELECT E'it\\'s; ok', e'\\\\';\n" +
		"SELECT 'plain\\';\n" +
		"/* outer /* inner; */ still; */ SELECT 2;\n" +
		"SELECT $tag_1$ ; $tag_1$"
	splitEverySize(t, pgSyntax, script, []string{
		"CREATE FUNCTION f() RETURNS text AS $body$ SELECT $$a;b$$; $body$ LANGUAGE sql",
		"PREPARE q AS SELECT $1, a$b$c FROM t",
		"SELECT E'it\\'s; ok', e'\\\\'",
		"SELECT 'plain\\'",
		"SELECT 2",
		"SELECT $tag_1$ ; $tag_1$",
	})
}

// CREATE [OR REPLACE] FUNCTION | PROCEDURE 的 BEGIN ... END 体（体里有 CASE ... END）到配对的 END
// 才结束；括号里的 begin 不算；不是建函数的 BEGIN; 照常结束语句。
func TestSplitScriptKeepsAtomicBodiesWhole(t *testing.T) {
	script := "CREATE OR REPLACE PROCEDURE p(begin int) LANGUAGE sql BEGIN ATOMIC\n" +
		"  INSERT INTO t VALUES (CASE WHEN begin > 0 THEN 1 ELSE 0 END);\n" +
		"  INSERT INTO t VALUES (2);\n" +
		"END;\n" +
		"BEGIN;\n" +
		"SELECT CASE WHEN true THEN 1 END;\n" +
		"END;"
	splitEverySize(t, pgSyntax, script, []string{
		"CREATE OR REPLACE PROCEDURE p(begin int) LANGUAGE sql BEGIN ATOMIC\n" +
			"  INSERT INTO t VALUES (CASE WHEN begin > 0 THEN 1 ELSE 0 END);\n" +
			"  INSERT INTO t VALUES (2);\n" +
			"END",
		"BEGIN",
		"SELECT CASE WHEN true THEN 1 END",
		"END",
	})
}

// 认出 psql 才有的写法时停下：给出它之前的语句与它的位置，不再往后切分。
func TestSplitScriptReportsPsqlOnlyConstructs(t *testing.T) {
	for name, tt := range map[string]struct {
		text, construct string
		before          []string
	}{
		"copy from stdin": {text: "SELECT 1;\nCOPY t (a) FROM STDIN;\n1\n\\.\n", construct: "COPY ... FROM stdin", before: []string{"SELECT 1"}},
		"meta-command":    {text: "SELECT 1; SELECT 2;\n\\i other.sql\n", construct: `\i`, before: []string{"SELECT 1", "SELECT 2"}},
	} {
		split, err := SplitScript(pgSyntax, dbdriver.ScriptChunk{Text: tt.text, Final: true})
		require.NoError(t, err, name)
		assert.Equal(t, tt.before, statementTexts(split), name)
		require.NotNil(t, split.Unsupported, name)
		offset := strings.Index(tt.text, "COPY")
		if offset < 0 {
			offset = strings.Index(tt.text, `\`)
		}
		assert.Equal(t, dbdriver.ScriptUnsupported{Construct: tt.construct, Tool: "psql", Offset: offset}, *split.Unsupported, name)
	}
	// 别的方言里这些只是普通的记号。
	split, err := SplitScript(triggerSyntax, dbdriver.ScriptChunk{Text: "COPY t FROM stdin;", Final: true})
	require.NoError(t, err)
	assert.Nil(t, split.Unsupported)
}

// 单独一行的 GO（不分大小写，前面可以有空白，后面可以跟次数与 -- 注释）分开一个个批；批里的分号不切分；
// 行里还有别的内容、在注释与字符串里、跟在注释后面同一行的 GO 都不是分隔符。
func TestSplitScriptSplitsBatchesAtSeparatorLines(t *testing.T) {
	script := "SELECT 1; SELECT 2\nGO\n" +
		"  go  -- empty batch\n" +
		"SELECT 'GO\nGO'\n/* GO\n/* GO */ GO\n*/ GO\nGO 2\n" +
		"-- comment\nGO\n" +
		"PRINT [GO]\nGO x\nGOTO done\nGo\n" +
		"SELECT 3"
	splitEverySize(t, batchSyntax, script, []string{
		"SELECT 1; SELECT 2",
		"SELECT 'GO\nGO'\n/* GO\n/* GO */ GO\n*/ GO",
		"SELECT 'GO\nGO'\n/* GO\n/* GO */ GO\n*/ GO",
		"PRINT [GO]\nGO x\nGOTO done",
		"SELECT 3",
	})
}

// 没有触发器体的语法（别的方言）里，BEGIN ... END 之间的分号照常结束语句。
func TestSplitScriptWithoutTriggerBodiesEndsAtEverySemicolon(t *testing.T) {
	syntax := ScriptSyntax{Quotes: []ScriptQuote{{Open: '\'', Close: '\''}}}
	split, err := SplitScript(syntax, dbdriver.ScriptChunk{Text: "CREATE TRIGGER x BEGIN SELECT 1; END;", Final: true})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ScriptStatement{{Text: "CREATE TRIGGER x BEGIN SELECT 1", Offset: 0}, {Text: "END", Offset: 33}}, split.Statements)
}
