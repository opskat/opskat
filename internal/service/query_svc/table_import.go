package query_svc

import (
	"context"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// TableImportBatchRequest describes a prepared table import batch.
type TableImportBatchRequest struct {
	Statements              []string `json:"statements"`
	Mode                    string   `json:"mode"`
	ContinueOnError         bool     `json:"continueOnError"`
	DisableForeignKeyChecks bool     `json:"disableForeignKeyChecks"`
	// Table 是导入的目标表（ListTables 给出的形式）：按表停用外键约束的驱动作用于它。
	Table string `json:"table"`
}

// TableImportBatchError captures a failed statement; Index is -1 for a failure of the batch itself
// (such as restoring foreign key checks) reported alongside failed statements.
type TableImportBatchError struct {
	Index     int    `json:"index"`
	Statement string `json:"statement"`
	Message   string `json:"message"`
}

// TableImportBatchResult returns aggregate counters for the import summary.
type TableImportBatchResult struct {
	Processed  int                     `json:"processed"`
	Added      int64                   `json:"added"`
	Updated    int64                   `json:"updated"`
	Deleted    int64                   `json:"deleted"`
	Error      int                     `json:"error"`
	RolledBack bool                    `json:"rolledBack"`
	Errors     []TableImportBatchError `json:"errors"`
}

// RunTableImportBatch 让驱动在会话的一条连接上执行导入的整批语句，再把结果计成导入摘要。
// 复制模式与出错不继续的导入放进一个事务。database 是目标表所在的库。
//
// 批本身出错时：已经有语句失败的，它作为一条不对应语句的错误与那些失败一起报告；没有语句失败
// 的，它就是导入的错误。
//
// 新增 / 更新 / 删除只计留下来的行：事务里的整批有语句失败就不会提交（回滚了，或者连回滚都出错），
// 其中执行成功的语句也不计。
func RunTableImportBatch(ctx context.Context, session dbdriver.Session, database string, request TableImportBatchRequest) (*TableImportBatchResult, error) {
	atomic := request.Mode == "copy" || !request.ContinueOnError
	batch, err := session.ExecuteBatch(ctx, dbdriver.BatchRequest{
		Statements:              request.Statements,
		Atomic:                  atomic,
		ContinueOnError:         request.ContinueOnError,
		DisableForeignKeyChecks: request.DisableForeignKeyChecks,
		Database:                database,
		Table:                   request.Table,
	})
	if err != nil && (batch == nil || !batch.Failed()) {
		return nil, err
	}

	result := &TableImportBatchResult{Processed: len(batch.Executed), RolledBack: batch.RolledBack}
	committed := !atomic || !batch.Failed()
	for _, executed := range batch.Executed {
		statement := strings.TrimSpace(request.Statements[executed.Index])
		if executed.Error != "" {
			result.Errors = append(result.Errors, TableImportBatchError{Index: executed.Index, Statement: statement, Message: executed.Error})
			continue
		}
		if committed {
			addAffectedRows(result, statement, request.Mode, executed.AffectedRows)
		}
	}
	if err != nil {
		result.Errors = append(result.Errors, TableImportBatchError{Index: -1, Message: err.Error()})
	}
	result.Error = len(result.Errors)
	return result, nil
}

func addAffectedRows(result *TableImportBatchResult, statement string, mode string, affected int64) {
	switch leadingSQLKeyword(statement) {
	case "DELETE":
		result.Deleted += affected
	case "UPDATE":
		result.Updated += affected
	case "INSERT", "REPLACE":
		result.Added += affected
	default:
		switch mode {
		case "update":
			result.Updated += affected
		case "delete":
			result.Deleted += affected
		default:
			result.Added += affected
		}
	}
}

func leadingSQLKeyword(statement string) string {
	fields := strings.Fields(statement)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}
