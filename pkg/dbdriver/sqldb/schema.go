package sqldb

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// TableTruncator 是方言里「清空一张表怎么写」：多数方言是 TRUNCATE TABLE，没有它的方言（SQLite）
// 用 DELETE FROM。建表与删表各方言相同（CREATE TABLE / DROP TABLE，标识符与表引用沿用 Tables），
// 不需要方言另外实现。
//
// 没实现的方言，Session.TruncateTableStatements 返回 dbdriver.ErrUnsupported。
type TableTruncator interface {
	// TruncateTableSQL 是清空一张表的语句（不含分号）。tableRef 已按方言加好引号。
	TruncateTableSQL(tableRef string) string
}

// DatabaseCreator 是方言里「新建数据库怎么写」。没实现的方言（SQLite），
// Session.CreateDatabaseStatements 返回 dbdriver.ErrUnsupported。
type DatabaseCreator interface {
	// CreateDatabaseSQL 是新建数据库的语句（不含分号）。databaseRef 已按方言加好引号；options 已经
	// 过 CleanDatabaseOptions：键是方言接受的、值是合法字符，没填的选项不在其中。
	CreateDatabaseSQL(databaseRef string, options map[string]string) string
	// DatabaseOptionKeys 是方言接受的选项键（对应 Descriptor.CreateDatabaseOptions）。
	DatabaseOptionKeys() []string
}

// DefinitionLiterals 是建表与改表里默认值、注释的字面量规则：与改动前一样，文本一律写成单引号成对
// 转义的字符串——不按列类型加 N 前缀、不转义反斜杠、不写成十六进制。按方言写字面量的只是写进表里
// 的值（RowWriter.Literals）。
var DefinitionLiterals = Literals{}

// CreateTableStatements 生成建表语句：表引用与列名按方言加引号，类型文本原样写入，默认值与自增见
// NewColumnSQL。索引与注释（请求的表注释、列的 Comment）只交给开启了它们的方言：方言能整条写建表语句
// （TableCreator）时一并交给它，否则用共用的 CREATE TABLE，随后按请求的次序建索引（TableIndexer），再写
// 注释（SchemaCommenter，表注释在前、列注释后）——两者都写不了时，请求带它们就以 ErrUnsupported 拒绝，
// 而不是悄悄丢掉。请求用到方言没开启的索引或列能力时同样以 ErrUnsupported 拒绝。不碰数据库。
func (s *Session) CreateTableStatements(_ context.Context, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	tables, err := s.tables()
	if err != nil {
		return nil, err
	}
	columns := req.Definitions()
	if err := columnFeatures(s.dialect).Check(columns...); err != nil {
		return nil, err
	}
	indexer, writesIndexes := s.dialect.(TableIndexer)
	if err := checkCreateIndexes(indexer, writesIndexes, req.Indexes); err != nil {
		return nil, err
	}
	if writesIndexes {
		if err := checkExclusiveOptions(indexer.IndexEditing(), req.Indexes, req.Name); err != nil {
			return nil, err
		}
	}
	commenter, writesComments := s.dialect.(SchemaCommenter)
	creator, writesTheTable := s.dialect.(TableCreator)
	if writesTheTable {
		return creator.CreateTableSQL(tables, database, req)
	}
	if err := checkSchemaComments(req, writesComments); err != nil {
		return nil, err
	}

	defs := make([]string, 0, len(columns))
	for _, column := range columns {
		def, err := NewColumnSQL(s.dialect, DefinitionLiterals, tables.QuoteIdent, column)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	tableRef := tables.TableRef(database, req.Name)
	statements := []string{fmt.Sprintf("CREATE TABLE %s (\n  %s\n)", tableRef, strings.Join(defs, ",\n  "))}
	if writesIndexes {
		for _, index := range req.Indexes {
			statement, err := indexer.IndexSQL(tableRef, index)
			if err != nil {
				return nil, err
			}
			statements = append(statements, statement)
		}
	}
	if writesComments {
		if comment := strings.TrimSpace(req.Comment); comment != "" {
			statement, err := commenter.TableCommentSQL(tableRef, comment)
			if err != nil {
				return nil, err
			}
			statements = append(statements, statement)
		}
		for _, column := range columns {
			if strings.TrimSpace(column.Comment) == "" {
				continue
			}
			statement, err := commenter.ColumnCommentSQL(tableRef, column)
			if err != nil {
				return nil, err
			}
			statements = append(statements, statement)
		}
	}
	return statements, nil
}

// checkCreateIndexes 检查建表请求里的索引按方言的声明只用了声明了的能力（类型、降序、前缀、注释、选项）。
// 没开启索引编辑的方言没有声明，任何索引都被拒绝。
func checkCreateIndexes(indexer TableIndexer, writesIndexes bool, indexes []dbdriver.IndexDefinition) error {
	if !writesIndexes {
		return (*dbdriver.IndexEditing)(nil).Check(indexes...)
	}
	return indexer.IndexEditing().Check(indexes...)
}

// checkSchemaComments 拒绝共用的建表写法写不出来的注释：它们只能由方言自己写（SchemaCommenter 或
// TableCreator），共用的 CREATE TABLE 没有它们的位置。
func checkSchemaComments(req dbdriver.CreateTableRequest, writesComments bool) error {
	if writesComments {
		return nil
	}
	if strings.TrimSpace(req.Comment) != "" {
		return fmt.Errorf("%w: this driver does not write a table comment when creating a table", dbdriver.ErrUnsupported)
	}
	for _, column := range req.Columns {
		if strings.TrimSpace(column.Comment) != "" {
			return fmt.Errorf("%w: this driver does not write column comments when creating a table (column %q)", dbdriver.ErrUnsupported, column.Name)
		}
	}
	return nil
}

// ColumnDefinitionSQL 是建表与新增列时的列定义：名称加引号、类型文本原样（首尾空白除外）、有默认值时
// DEFAULT（写法见 DefaultSQL）、不可空时 NOT NULL。DEFAULT 在 NOT NULL 之前：Oracle 只接受这个
// 次序（反过来报 ORA-00907），其余方言两种次序都接受。注释不在其中：各方言写法不同。自增的写法在方言
// 里：要自增的列用 NewColumnSQL，这里以 ErrUnsupported 拒绝，而不是悄悄丢掉自增。
func ColumnDefinitionSQL(literals Literals, quote func(string) string, column dbdriver.ColumnDefinition) (string, error) {
	if column.AutoIncrement {
		return "", autoIncrementUnsupported(column)
	}
	return columnDefinitionSQL(literals, quote, column, "")
}

// columnDefinitionSQL 写列定义；autoIncrement 是方言的自增子句，写在类型之后，空表示不自增。
func columnDefinitionSQL(literals Literals, quote func(string) string, column dbdriver.ColumnDefinition, autoIncrement string) (string, error) {
	column.Type = strings.TrimSpace(column.Type)
	def := quote(strings.TrimSpace(column.Name)) + " " + column.Type
	if autoIncrement != "" {
		def += " " + autoIncrement
	}
	literal, ok, err := DefaultSQL(literals, column)
	if err != nil {
		return "", err
	}
	if ok {
		def += " DEFAULT " + literal
	}
	if !column.Nullable {
		def += " NOT NULL"
	}
	return def, nil
}

// DropTableStatements 生成删表语句。不碰数据库。
func (s *Session) DropTableStatements(_ context.Context, database, table string) ([]string, error) {
	tables, err := s.tables()
	if err != nil {
		return nil, err
	}
	return []string{"DROP TABLE " + tables.TableRef(database, table)}, nil
}

// TruncateTableStatements 生成清空表的语句。不碰数据库。
func (s *Session) TruncateTableStatements(_ context.Context, database, table string) ([]string, error) {
	tables, err := s.tables()
	if err != nil {
		return nil, err
	}
	truncator, ok := s.dialect.(TableTruncator)
	if !ok {
		return nil, fmt.Errorf("%w: this driver does not truncate tables", dbdriver.ErrUnsupported)
	}
	return []string{truncator.TruncateTableSQL(tables.TableRef(database, table))}, nil
}

// CreateDatabaseStatements 生成新建数据库的语句。库名按方言加引号，选项值只接受字母、数字、
// 下划线、点和连字符（它们原样写进语句）。不碰数据库。
func (s *Session) CreateDatabaseStatements(_ context.Context, req dbdriver.CreateDatabaseRequest) ([]string, error) {
	tables, err := s.tables()
	if err != nil {
		return nil, err
	}
	creator, ok := s.dialect.(DatabaseCreator)
	if !ok {
		return nil, fmt.Errorf("%w: this driver does not create databases", dbdriver.ErrUnsupported)
	}
	options, err := cleanDatabaseOptions(req.Options, creator.DatabaseOptionKeys())
	if err != nil {
		return nil, err
	}
	return []string{creator.CreateDatabaseSQL(tables.QuoteIdent(req.Name), options)}, nil
}

var safeOptionPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// cleanDatabaseOptions 去掉首尾空白与没填的选项，拒绝方言不接受的键与含其他字符的值。
func cleanDatabaseOptions(options map[string]string, allowed []string) (map[string]string, error) {
	cleaned := make(map[string]string, len(options))
	for key, value := range options {
		if !slices.Contains(allowed, key) {
			return nil, fmt.Errorf("unsupported create database option %q", key)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !safeOptionPattern.MatchString(value) {
			return nil, fmt.Errorf("invalid value for create database option %q", key)
		}
		cleaned[key] = value
	}
	return cleaned, nil
}
