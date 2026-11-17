package sqldb

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// RunScript 运行 SQL 文件：经 dbdriver.RunScript 的编排，用 ScriptRunner 的钩子在会话的一条独立连接上
// 逐条执行；方言不能运行 SQL 文件时以 dbdriver.ErrUnsupported 拒绝，不拿连接、不执行任何语句。
func (s *Session) RunScript(ctx context.Context, req dbdriver.ScriptRequest) (*dbdriver.ScriptResult, error) {
	runner, err := s.ScriptRunner()
	if err != nil {
		return nil, err
	}
	return dbdriver.RunScript(ctx, req, runner)
}

// ScriptRunner 是会话运行 SQL 文件的两个钩子（RunScript 交给编排的就是它；扩展 SDK 经它在 ABI 上提供
// 运行 SQL 文件，编排在宿主里）：切分按方言（scriptSplitter），执行在从连接池里拿出的一条连接上。方言
// 既不实现 dbdriver.ScriptSplitter 也不实现 ScriptDialect 时以 dbdriver.ErrUnsupported 拒绝。
func (s *Session) ScriptRunner() (dbdriver.ScriptRunner, error) {
	splitter, ok := scriptSplitter(s.dialect)
	if !ok {
		return nil, fmt.Errorf("%w: this driver does not run SQL files", dbdriver.ErrUnsupported)
	}
	return scriptRunner{ScriptSplitter: splitter, session: s}, nil
}

// scriptRunner 是 database/sql 之上的驱动运行 SQL 文件的两个钩子。
type scriptRunner struct {
	dbdriver.ScriptSplitter
	session *Session
}

// OpenScriptExecutor 从连接池里拿出一条连接，整个文件都在它上面执行。库名写进连接串的驱动，会话的
// 连接本来就连着要执行的库；一条会话覆盖整个资产的驱动（SQLite）只有一个库，所以不需要切换。
func (r scriptRunner) OpenScriptExecutor(ctx context.Context, _ string) (dbdriver.ScriptExecutor, error) {
	conn, err := r.session.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("打开数据库会话失败: %w", err)
	}
	return scriptExecutor{conn: conn}, nil
}

// scriptExecutor 在一条固定的连接上执行 SQL 文件的语句，每条单独提交（不开事务）。
type scriptExecutor struct{ conn *sql.Conn }

// ExecuteScriptBatch 逐条执行一批语句；batch.Stop 被关闭后在当前语句之后停下，没轮到的语句不执行。
func (e scriptExecutor) ExecuteScriptBatch(ctx context.Context, batch dbdriver.ScriptBatch) (*dbdriver.BatchResult, error) {
	result := &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{}}
	for index, statement := range batch.Statements {
		select {
		case <-batch.Stop:
			return result, nil
		default:
		}
		one, err := runStatements(ctx, e.conn, dbdriver.BatchRequest{Statements: []string{statement}, ContinueOnError: batch.ContinueOnError})
		for _, executed := range one.Executed {
			executed.Index = index
			result.Executed = append(result.Executed, executed)
		}
		if err != nil {
			return result, err
		}
		if one.Failed() && !batch.ContinueOnError {
			break
		}
	}
	return result, nil
}

func (e scriptExecutor) Close() error { return e.conn.Close() }
