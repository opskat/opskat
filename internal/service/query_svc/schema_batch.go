package query_svc

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// SchemaStatementStatus 是表设计器保存时一条语句的结局。
type SchemaStatementStatus string

const (
	// SchemaStatementExecuted：执行成功。整批回滚了的（SchemaBatchResult.RolledBack）也没有留下。
	SchemaStatementExecuted SchemaStatementStatus = "executed"
	// SchemaStatementFailed：数据库拒绝了它，错误在 SchemaStatementResult.Error。
	SchemaStatementFailed SchemaStatementStatus = "failed"
	// SchemaStatementNotExecuted：前面有语句失败，没轮到它。
	SchemaStatementNotExecuted SchemaStatementStatus = "notExecuted"
)

// SchemaStatementResult 是保存时一条语句的结果，次序同请求里的语句。
type SchemaStatementResult struct {
	Statement string                `json:"statement"`
	Status    SchemaStatementStatus `json:"status"`
	// Error 是失败时数据库给出的错误。
	Error string `json:"error,omitempty"`
}

// SchemaBatchResult 是表设计器保存（一批表结构语句）的结果。
type SchemaBatchResult struct {
	// Atomic 为真表示整批在一个事务里执行（驱动声明了 Descriptor.TransactionalDDL）。
	Atomic bool `json:"atomic"`
	// RolledBack 为真表示有语句失败、整批已经回滚：表结构不变。
	RolledBack bool `json:"rolledBack"`
	// Statements 逐条给出结果，次序同请求里的语句。
	Statements []SchemaStatementResult `json:"statements"`
	// Error 是与失败的语句一起发生的批本身的错误（如回滚失败），此时整批没有回滚；没有时为空。
	Error string `json:"error,omitempty"`
}

// RunSchemaBatch 在会话的一条连接上执行表设计器保存的一批表结构语句。desc 是会话所属驱动的描述：
// 声明了 TransactionalDDL 的，整批在一个事务里执行，任一条失败即整批回滚；没声明的逐条执行，停在
// 第一条失败的语句，之前执行的留下。database 是语句所在的库。
//
// 空白语句会被整批执行跳过，报成「未执行」会误导用户，所以在交给驱动之前就以 error 拒绝。批本身
// 出错（拿不到连接、开始 / 提交事务失败、ctx 被取消）且没有语句失败时，它就是保存的错误；已经有语句
// 失败的，它记在结果的 Error 里，与各条的结果一起报告。
func RunSchemaBatch(ctx context.Context, session dbdriver.Session, desc dbdriver.Descriptor, database string, statements []string) (*SchemaBatchResult, error) {
	for i, statement := range statements {
		if strings.TrimSpace(statement) == "" {
			return nil, fmt.Errorf("schema statement %d is blank", i)
		}
	}
	atomic := desc.TransactionalDDL
	batch, err := session.ExecuteBatch(ctx, dbdriver.BatchRequest{
		Statements: statements,
		Atomic:     atomic,
		Database:   database,
	})
	if err != nil && (batch == nil || !batch.Failed()) {
		return nil, err
	}

	result := &SchemaBatchResult{Atomic: atomic, RolledBack: batch.RolledBack, Statements: make([]SchemaStatementResult, len(statements))}
	for i, statement := range statements {
		result.Statements[i] = SchemaStatementResult{Statement: statement, Status: SchemaStatementNotExecuted}
	}
	for _, executed := range batch.Executed {
		if executed.Error != "" {
			result.Statements[executed.Index].Status = SchemaStatementFailed
			result.Statements[executed.Index].Error = executed.Error
			continue
		}
		result.Statements[executed.Index].Status = SchemaStatementExecuted
	}
	if err != nil {
		result.Error = err.Error()
	}
	return result, nil
}
