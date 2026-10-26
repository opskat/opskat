package mysql

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 改表：列照 SHOW FULL COLUMNS 读（同时拿到对话框不显示的 Collation 与 Extra），表注释查
// information_schema.tables；改名用 RENAME TABLE，其余改动合成一条 ALTER TABLE，已有列用
// CHANGE COLUMN（改了名）/ MODIFY COLUMN 整列重写，所以用户没动的自增、ON UPDATE、排序规则（字符集
// 随它）与表达式默认值要从现在的定义里原样带上。表注释与列注释都能改。
var _ sqldb.TableAlterer = dialect{}

var alterAttributes = []dbdriver.AlterAttribute{
	dbdriver.AlterTableComment, dbdriver.AlterColumnType, dbdriver.AlterColumnNullable,
	dbdriver.AlterColumnDefault, dbdriver.AlterColumnComment,
}

func (dialect) AlterAttributes() []dbdriver.AlterAttribute { return alterAttributes }

func (dialect) AlterableTable(ctx context.Context, conn *sql.Conn, database, table string) (*sqldb.AlterableTable, error) {
	records, err := sqldb.QueryRecords(ctx, conn, "SHOW FULL COLUMNS FROM "+quoteTableRef(database, table))
	if err != nil {
		return nil, err
	}
	out := &sqldb.AlterableTable{Name: table, Columns: make([]sqldb.AlterableColumn, 0, len(records))}
	for _, row := range records {
		out.Columns = append(out.Columns, sqldb.AlterableColumn{
			ColumnDefinition: dbdriver.ColumnDefinition{
				Name:         sqldb.PickString(row, "Field"),
				Type:         sqldb.PickString(row, "Type"),
				Nullable:     strings.EqualFold(sqldb.PickString(row, "Null"), "YES"),
				DefaultValue: sqldb.PickString(row, "Default"),
				Comment:      sqldb.PickString(row, "Comment"),
			},
			Row: row,
		})
	}
	err = conn.QueryRowContext(ctx, "SELECT COALESCE(table_comment, '') AS table_comment FROM information_schema.tables "+
		"WHERE table_schema = ? AND table_name = ? LIMIT 1", database, table).Scan(&out.Comment)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return out, nil
}

func (dialect) AlterTableSQL(_ context.Context, _ *sql.Conn, change sqldb.TableChange) ([]string, error) {
	literals := sqldb.DefinitionLiterals
	var statements []string
	if change.Rename != "" {
		statements = append(statements, "RENAME TABLE "+change.TableRef+" TO "+change.TargetRef)
	}

	var clauses []string
	for _, column := range change.Columns {
		if !column.IsNew() {
			continue
		}
		definition, err := columnDefinition(literals, column, false)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, "ADD COLUMN "+definition)
	}
	for _, column := range change.Columns {
		if column.IsNew() {
			continue
		}
		definition, err := columnDefinition(literals, column, column.CommentChanged)
		if err != nil {
			return nil, err
		}
		if column.Renamed {
			clauses = append(clauses, "CHANGE COLUMN "+quoteIdent(column.Current.Name)+" "+definition)
		} else {
			clauses = append(clauses, "MODIFY COLUMN "+definition)
		}
	}
	for _, name := range change.Drops {
		clauses = append(clauses, "DROP COLUMN "+quoteIdent(name))
	}
	if change.Comment != nil {
		comment, err := literals.Literal(*change.Comment, "")
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, "COMMENT = "+comment)
	}
	if len(clauses) > 0 {
		statements = append(statements, "ALTER TABLE "+change.TargetRef+" "+strings.Join(clauses, ", "))
	}
	return statements, nil
}

var (
	// characterTypePattern 匹配带字符集与排序规则的类型；改成别的类型时不再带上原来的排序规则。
	characterTypePattern = regexp.MustCompile(`(?i)^\s*(national\s+)?(n?char|n?varchar|character|tinytext|text|mediumtext|longtext|enum|set)\b`)
	// explicitCharsetPattern 匹配用户在类型文本里自己写了字符集或排序规则。
	explicitCharsetPattern = regexp.MustCompile(`(?i)\b(collate|character\s+set|charset)\b`)
	onUpdatePattern        = regexp.MustCompile(`(?i)\bon update (\S+)`)
	// generatedTimestampPattern 是不加括号就能作默认值的时间函数（其余表达式默认值要加括号）。
	generatedTimestampPattern = regexp.MustCompile(`(?i)^(current_timestamp|now|localtime|localtimestamp)(\(\d*\))?$`)
)

// columnDefinition 是 ADD / CHANGE / MODIFY COLUMN 后面的列定义：名称、类型、可空、默认值、注释（有
// 注释或 forceComment 时写，清空注释要写 COMMENT ”）。改动已有的列时，从它现在的定义里带上用户没动
// 的排序规则、表达式默认值、ON UPDATE 与自增。
func columnDefinition(literals sqldb.Literals, column sqldb.ColumnChange, forceComment bool) (string, error) {
	c := column.Column
	columnType := strings.TrimSpace(c.Type)
	var extra string
	if !column.IsNew() {
		extra = strings.ToLower(sqldb.PickString(column.Current.Row, "Extra"))
	}

	definition := quoteIdent(c.Name) + " " + columnType
	if !column.IsNew() {
		if collation := sqldb.PickString(column.Current.Row, "Collation"); collation != "" &&
			characterTypePattern.MatchString(columnType) && !explicitCharsetPattern.MatchString(columnType) {
			definition += " COLLATE " + collation
		}
	}
	if !c.Nullable {
		definition += " NOT NULL"
	}
	if defaultValue := strings.TrimSpace(c.DefaultValue); defaultValue != "" {
		untouched := !column.IsNew() && !column.DefaultChanged
		switch {
		case untouched && generatedTimestampPattern.MatchString(defaultValue):
			// 时间函数原样写，不加括号、不加引号（MySQL 5.7 的 Extra 里没有 DEFAULT_GENERATED）。
		case untouched && strings.Contains(extra, "default_generated"):
			defaultValue = "(" + defaultValue + ")"
		default:
			literal, err := literals.Default(c.DefaultValue, columnType)
			if err != nil {
				return "", err
			}
			defaultValue = literal
		}
		definition += " DEFAULT " + defaultValue
	}
	if !column.IsNew() {
		if match := onUpdatePattern.FindStringSubmatch(sqldb.PickString(column.Current.Row, "Extra")); match != nil {
			definition += " ON UPDATE " + match[1]
		}
		if strings.Contains(extra, "auto_increment") {
			definition += " AUTO_INCREMENT"
		}
	}
	if forceComment || strings.TrimSpace(c.Comment) != "" {
		comment, err := literals.Literal(c.Comment, "")
		if err != nil {
			return "", err
		}
		definition += " COMMENT " + comment
	}
	return definition, nil
}
