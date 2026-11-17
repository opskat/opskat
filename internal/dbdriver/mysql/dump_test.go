package mysql

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	gomysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

const (
	columnsSQL = "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? " +
		"AND EXTRA NOT LIKE '%VIRTUAL GENERATED%' AND EXTRA NOT LIKE '%STORED GENERATED%' ORDER BY ORDINAL_POSITION"
	viewUsageSQL = "SELECT TABLE_NAME FROM information_schema.VIEW_TABLE_USAGE WHERE VIEW_SCHEMA = ? AND TABLE_SCHEMA = VIEW_SCHEMA AND VIEW_NAME = ?"
	viewsSQL     = "SELECT TABLE_NAME FROM information_schema.VIEWS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME"

	proceduresSQL = "SELECT ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = 'PROCEDURE' ORDER BY ROUTINE_NAME"
	functionsSQL  = "SELECT ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = 'FUNCTION' ORDER BY ROUTINE_NAME"
	triggersSQL   = "SELECT TRIGGER_NAME FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() ORDER BY TRIGGER_NAME"
)

func dumpHeader() dbdriver.DumpHeader {
	return dbdriver.DumpHeader{Version: "1.16.0", Asset: "prod-mysql", Time: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
}

// expectObjects 预期 ListObjects 的四次查询：视图、过程、函数、触发器（只给视图）。
func expectObjects(mock sqlmock.Sqlmock, views ...string) {
	mock.ExpectQuery(viewsSQL).WillReturnRows(names("TABLE_NAME", views...))
	mock.ExpectQuery(proceduresSQL).WillReturnRows(names("ROUTINE_NAME"))
	mock.ExpectQuery(functionsSQL).WillReturnRows(names("ROUTINE_NAME"))
	mock.ExpectQuery(triggersSQL).WillReturnRows(names("TRIGGER_NAME"))
}

func noViewTableUsage() error {
	return &gomysql.MySQLError{Number: 1146, Message: "Table 'information_schema.VIEW_TABLE_USAGE' doesn't exist"}
}

// 整库转储（仅结构）写出的文件：开头 SET NAMES 与关外键检查，每个对象前 DROP ... IF EXISTS（不带库名），
// 定义是服务器的原文（DEFINER 保留）但不带库名，视图按依赖排序，过程、函数、触发器用 DELIMITER ;; 包起来
// 且在视图之后，结尾恢复外键检查。
func TestDumpWritesRestorableMySQLFile(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(viewsSQL).WillReturnRows(names("TABLE_NAME", "v_a", "v_b"))
	mock.ExpectQuery(proceduresSQL).WillReturnRows(names("ROUTINE_NAME", "p_touch"))
	mock.ExpectQuery(functionsSQL).WillReturnRows(names("ROUTINE_NAME", "f_one"))
	mock.ExpectQuery(triggersSQL).WillReturnRows(names("TRIGGER_NAME", "t_bi"))
	mock.ExpectQuery("SHOW TABLES FROM `app`").WillReturnRows(names("Tables_in_app", "items", "v_a", "v_b"))
	mock.ExpectQuery("SHOW CREATE TABLE `items`").WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).
		AddRow("items", "CREATE TABLE `items` (\n  `id` int NOT NULL AUTO_INCREMENT,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB AUTO_INCREMENT=7"))
	mock.ExpectQuery("SHOW CREATE TABLE `v_a`").WillReturnRows(sqlmock.NewRows([]string{"View", "Create View"}).
		AddRow("v_a", "CREATE ALGORITHM=UNDEFINED DEFINER=`root`@`%` SQL SECURITY DEFINER VIEW `app`.`v_a` AS select `app`.`v_b`.`id` AS `id` from `app`.`v_b`"))
	mock.ExpectQuery(viewUsageSQL).WithArgs("app", "v_a").WillReturnRows(names("TABLE_NAME", "v_b"))
	mock.ExpectQuery("SHOW CREATE TABLE `v_b`").WillReturnRows(sqlmock.NewRows([]string{"View", "Create View"}).
		AddRow("v_b", "CREATE ALGORITHM=UNDEFINED DEFINER=`root`@`%` SQL SECURITY DEFINER VIEW `v_b` AS select `items`.`id` AS `id` from `items`"))
	mock.ExpectQuery(viewUsageSQL).WithArgs("app", "v_b").WillReturnRows(names("TABLE_NAME", "items"))
	mock.ExpectQuery("SHOW CREATE FUNCTION `f_one`").WillReturnRows(sqlmock.NewRows([]string{"Function", "sql_mode", "Create Function"}).
		AddRow("f_one", "", "CREATE DEFINER=`root`@`%` FUNCTION `f_one`() RETURNS int\nRETURN 1"))
	mock.ExpectQuery("SHOW CREATE PROCEDURE `p_touch`").WillReturnRows(sqlmock.NewRows([]string{"Procedure", "sql_mode", "Create Procedure"}).
		AddRow("p_touch", "", "CREATE DEFINER=`root`@`%` PROCEDURE `p_touch`()\nBEGIN\n  UPDATE `app`.`items` SET id = id;\nEND"))
	mock.ExpectQuery("SHOW CREATE TRIGGER `t_bi`").WillReturnRows(sqlmock.NewRows([]string{"Trigger", "sql_mode", "SQL Original Statement"}).
		AddRow("t_bi", "", "CREATE DEFINER=`root`@`%` TRIGGER `t_bi` BEFORE INSERT ON `items` FOR EACH ROW SET NEW.id = NEW.id"))

	var out strings.Builder
	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "app", Whole: true, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, "-- OpsKat SQL Dump\n--\n-- OpsKat version: 1.16.0\n-- Asset: prod-mysql\n-- Database: app\n-- Content: structure only\n"+
		"-- Date: 2026-11-17 08:00:00 +00:00\n--\n-- File encoding: UTF-8\n"+`
SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- ----------------------------
-- Table structure for items
-- ----------------------------
DROP TABLE IF EXISTS `+"`items`"+`;
CREATE TABLE `+"`items`"+` (
  `+"`id`"+` int NOT NULL AUTO_INCREMENT,
  PRIMARY KEY (`+"`id`"+`)
) ENGINE=InnoDB AUTO_INCREMENT=7;

-- ----------------------------
-- View structure for v_b
-- ----------------------------
DROP VIEW IF EXISTS `+"`v_b`"+`;
CREATE ALGORITHM=UNDEFINED DEFINER=`+"`root`@`%`"+` SQL SECURITY DEFINER VIEW `+"`v_b`"+` AS select `+"`items`.`id`"+` AS `+"`id`"+` from `+"`items`"+`;

-- ----------------------------
-- View structure for v_a
-- ----------------------------
DROP VIEW IF EXISTS `+"`v_a`"+`;
CREATE ALGORITHM=UNDEFINED DEFINER=`+"`root`@`%`"+` SQL SECURITY DEFINER VIEW `+"`v_a`"+` AS select `+"`v_b`.`id`"+` AS `+"`id`"+` from `+"`v_b`"+`;

-- ----------------------------
-- Function structure for f_one
-- ----------------------------
DROP FUNCTION IF EXISTS `+"`f_one`"+`;
DELIMITER ;;
CREATE DEFINER=`+"`root`@`%`"+` FUNCTION `+"`f_one`"+`() RETURNS int
RETURN 1
;;
DELIMITER ;

-- ----------------------------
-- Procedure structure for p_touch
-- ----------------------------
DROP PROCEDURE IF EXISTS `+"`p_touch`"+`;
DELIMITER ;;
CREATE DEFINER=`+"`root`@`%`"+` PROCEDURE `+"`p_touch`"+`()
BEGIN
  UPDATE `+"`items`"+` SET id = id;
END
;;
DELIMITER ;

-- ----------------------------
-- Trigger structure for t_bi
-- ----------------------------
DROP TRIGGER IF EXISTS `+"`t_bi`"+`;
DELIMITER ;;
CREATE DEFINER=`+"`root`@`%`"+` TRIGGER `+"`t_bi`"+` BEFORE INSERT ON `+"`items`"+` FOR EACH ROW SET NEW.id = NEW.id
;;
DELIMITER ;

SET FOREIGN_KEY_CHECKS = 1;
`, out.String())
}

// 结构和数据时 INSERT 的列去掉生成列（VIRTUAL / STORED GENERATED），表名不带库名。
func TestDumpDataSkipsGeneratedColumnsAndOmitsTheDatabase(t *testing.T) {
	session, mock := newMock(t)
	expectObjects(mock)
	mock.ExpectQuery("SHOW CREATE TABLE `items`").WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).
		AddRow("items", "CREATE TABLE `items` (`id` int)"))
	mock.ExpectQuery(columnsSQL).WithArgs("app", "items").WillReturnRows(names("COLUMN_NAME", "id", "name"))
	mock.ExpectQuery("SHOW FULL COLUMNS FROM `app`.`items`").WillReturnRows(sqlmock.NewRows(
		[]string{"Field", "Type", "Null", "Key", "Default", "Extra"}).
		AddRow("id", "int", "NO", "", nil, "").AddRow("name", "varchar(20)", "YES", "", nil, "").AddRow("name_len", "int", "YES", "", nil, "VIRTUAL GENERATED"))
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(names("@@SESSION.sql_mode", "STRICT_TRANS_TABLES"))
	mock.ExpectQuery("SELECT * FROM `app`.`items`").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "name_len"}).AddRow(int64(1), "a", int64(1)))

	var out strings.Builder
	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "app", Tables: []string{"items"}, WithData: true, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "INSERT INTO `items` (`id`, `name`) VALUES ('1', 'a');\n")
	assert.NotContains(t, out.String(), "name_len")
}

// 视图依赖：information_schema.VIEW_TABLE_USAGE 不存在（5.7、MariaDB）时改看定义里引用的名字。
func TestDumpViewDependsOnNamesInItsDefinitionWithoutViewTableUsage(t *testing.T) {
	session, mock := newMock(t)
	expectObjects(mock, "v_a", "v_b")
	mock.ExpectQuery("SHOW CREATE TABLE `v_a`").WillReturnRows(sqlmock.NewRows([]string{"View", "Create View"}).
		AddRow("v_a", "CREATE VIEW `v_a` AS select `v_b`.`id` AS `id` from `v_b`"))
	mock.ExpectQuery(viewUsageSQL).WithArgs("app", "v_a").WillReturnError(noViewTableUsage())
	mock.ExpectQuery("SHOW CREATE TABLE `v_b`").WillReturnRows(sqlmock.NewRows([]string{"View", "Create View"}).
		AddRow("v_b", "CREATE VIEW `v_b` AS select 1 AS `id`"))
	mock.ExpectQuery(viewUsageSQL).WithArgs("app", "v_b").WillReturnError(noViewTableUsage())

	var out strings.Builder
	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "app", Tables: []string{"v_a", "v_b"}, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)
	assert.Less(t, strings.Index(out.String(), "VIEW `v_b`"), strings.Index(out.String(), "VIEW `v_a`"))
}

// 其他错误不当作「没有这张表」吞掉。
func TestDumpViewUsageErrorIsReported(t *testing.T) {
	session, mock := newMock(t)
	expectObjects(mock, "v_a")
	mock.ExpectQuery("SHOW CREATE TABLE `v_a`").WillReturnRows(sqlmock.NewRows([]string{"View", "Create View"}).
		AddRow("v_a", "CREATE VIEW `v_a` AS select 1 AS `id`"))
	mock.ExpectQuery(viewUsageSQL).WithArgs("app", "v_a").WillReturnError(assert.AnError)

	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "app", Tables: []string{"v_a"}, Header: dumpHeader(), Writer: &strings.Builder{},
	})
	require.ErrorIs(t, err, assert.AnError)
}

func TestStripDatabaseQualifier(t *testing.T) {
	for _, tt := range []struct{ name, database, in, want string }{
		{"quoted qualifiers", "app", "VIEW `app`.`v` AS select `app`.`t`.`c` from `app`.`t`", "VIEW `v` AS select `t`.`c` from `t`"},
		{"other database stays", "app", "select * from `other`.`t`, `app`.`u`", "select * from `other`.`t`, `u`"},
		{"definer named like the database", "app", "DEFINER=`app`@`%` VIEW `v`", "DEFINER=`app`@`%` VIEW `v`"},
		{"strings and comments stay", "app", "select '`app`.`x`' /* `app`.`y` */ -- `app`.`z`\nfrom `app`.`t`", "select '`app`.`x`' /* `app`.`y` */ -- `app`.`z`\nfrom `t`"},
		{"escaped quote in a string", "app", `select 'it\'s ` + "`app`.`x`" + `' from ` + "`app`.`t`", `select 'it\'s ` + "`app`.`x`" + `' from ` + "`t`"},
		{"column named like the database", "app", "select `t`.`app` from `t`", "select `t`.`app` from `t`"},
		{"doubled backtick in the name", "a`b", "from `a``b`.`t`", "from `t`"},
	} {
		got, _ := stripDatabase(tt.in, tt.database)
		assert.Equal(t, tt.want, got, tt.name)
	}
}

func TestTableRefWithoutADatabaseHasNoQualifier(t *testing.T) {
	assert.Equal(t, "`t`", dialect{}.TableRef("", "t"))
	assert.Equal(t, "`app`.`t`", dialect{}.TableRef("app", "t"))
}
