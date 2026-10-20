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

// CreateTableStatements 生成建表语句：表引用与列名按方言加引号，类型文本原样写入。不碰数据库。
func (s *Session) CreateTableStatements(_ context.Context, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	tables, err := s.tables()
	if err != nil {
		return nil, err
	}
	defs := make([]string, 0, len(req.Columns))
	for _, column := range req.Columns {
		def, err := ColumnDefinitionSQL(DefinitionLiterals, tables.QuoteIdent, dbdriver.ColumnDefinition{
			Name: column.Name, Type: column.Type, Nullable: column.Nullable, DefaultValue: column.DefaultValue,
		})
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return []string{fmt.Sprintf("CREATE TABLE %s (\n  %s\n)", tables.TableRef(database, req.Name), strings.Join(defs, ",\n  "))}, nil
}

// ColumnDefinitionSQL 是建表与新增列时的列定义：名称加引号、类型文本原样（首尾空白除外）、不可空时
// NOT NULL、有默认值时 DEFAULT（写法见 Literals.Default）。注释不在其中：各方言写法不同。
func ColumnDefinitionSQL(literals Literals, quote func(string) string, column dbdriver.ColumnDefinition) (string, error) {
	columnType := strings.TrimSpace(column.Type)
	def := quote(strings.TrimSpace(column.Name)) + " " + columnType
	if !column.Nullable {
		def += " NOT NULL"
	}
	if strings.TrimSpace(column.DefaultValue) != "" {
		literal, err := literals.Default(column.DefaultValue, columnType)
		if err != nil {
			return "", err
		}
		def += " DEFAULT " + literal
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
