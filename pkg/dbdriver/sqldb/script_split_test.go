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

// 没有触发器体的语法（别的方言）里，BEGIN ... END 之间的分号照常结束语句。
func TestSplitScriptWithoutTriggerBodiesEndsAtEverySemicolon(t *testing.T) {
	syntax := ScriptSyntax{Quotes: []ScriptQuote{{Open: '\'', Close: '\''}}}
	split, err := SplitScript(syntax, dbdriver.ScriptChunk{Text: "CREATE TRIGGER x BEGIN SELECT 1; END;", Final: true})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ScriptStatement{{Text: "CREATE TRIGGER x BEGIN SELECT 1", Offset: 0}, {Text: "END", Offset: 33}}, split.Statements)
}
