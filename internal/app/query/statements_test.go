package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// classifyingFake 是声明了语句切分分类的假驱动：切分结果与错误由测试给出，并记下收到的 SQL。
type classifyingFake struct {
	*dbdrivertest.Fake
	statements []dbdriver.Statement
	err        error
	got        []string
}

func newClassifyingFake() *classifyingFake {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapClassifyStatements)
	return &classifyingFake{Fake: fake}
}

func (d *classifyingFake) ClassifyStatements(_ context.Context, sql string) ([]dbdriver.Statement, error) {
	d.got = append(d.got, sql)
	return d.statements, d.err
}

// 面板取资产驱动的切分：语句的字节偏移、原文、类型与危险标记原样交给前端，不打开会话。
func TestClassifySQLStatementsReturnsTheDriversStatements(t *testing.T) {
	driver := newClassifyingFake()
	sql := "SELECT '中' FROM dual;\nBEGIN DELETE FROM t; END;"
	driver.statements = []dbdriver.Statement{
		{Start: 0, End: 20, Text: "SELECT '中' FROM dual", Type: "SELECT"},
		{Start: 22, End: len(sql), Text: "BEGIN DELETE FROM t; END;", Type: "PLSQL BLOCK", Dangerous: true, Reason: dbdriver.ReasonNoWhereDelete},
	}
	dbdrivertest.Register(t, driver)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	got, err := q.ClassifySQLStatements(1, sql)
	require.NoError(t, err)
	assert.Equal(t, driver.statements, got)
	assert.Equal(t, []string{sql}, driver.got)
	assert.Empty(t, driver.Sessions(), "splitting does not connect to the database")
}

// 驱动没切出任何语句（只有注释或空白）：交给前端的是空列表，不是 null。
func TestClassifySQLStatementsReturnsAnEmptyListNotNull(t *testing.T) {
	driver := newClassifyingFake()
	dbdrivertest.Register(t, driver)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	got, err := q.ClassifySQLStatements(1, "-- nothing")
	require.NoError(t, err)
	payload, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(payload))
}

// 驱动切不了：错误带着驱动给的原因，前端据此提示、不执行。
func TestClassifySQLStatementsReportsWhyTheDriverCannotSplit(t *testing.T) {
	driver := newClassifyingFake()
	driver.err = fmt.Errorf("%w: unterminated quoted string at line 2", dbdriver.ErrCannotSplit)
	dbdrivertest.Register(t, driver)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.ClassifySQLStatements(1, "SELECT 'x")
	require.ErrorIs(t, err, dbdriver.ErrCannotSplit)
	assert.Contains(t, err.Error(), "unterminated quoted string at line 2")
	assert.Contains(t, err.Error(), "fakedb")
}

// 驱动输出不合约束（位置越界）同样按无法切分报，点名驱动。
func TestClassifySQLStatementsRejectsOutputOutsideTheContract(t *testing.T) {
	driver := newClassifyingFake()
	driver.statements = []dbdriver.Statement{{Start: 0, End: 99, Text: "SELECT 1", Type: "SELECT"}}
	dbdrivertest.Register(t, driver)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.ClassifySQLStatements(1, "SELECT 1")
	require.ErrorIs(t, err, dbdriver.ErrCannotSplit)
	assert.Contains(t, err.Error(), "fakedb")
}

// 驱动不可用（扩展停用、卸载或切分途中被移除）：报驱动不可用，而不是「切不了」。
func TestClassifySQLStatementsReportsAnUnavailableDriver(t *testing.T) {
	t.Run("the driver is not registered", func(t *testing.T) {
		q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "oracle", Host: "h", Port: 1521, Username: "u"})

		_, err := q.ClassifySQLStatements(1, "SELECT 1 FROM dual")
		require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
		assert.Contains(t, err.Error(), "oracle")
	})

	t.Run("the driver goes away while splitting", func(t *testing.T) {
		driver := newClassifyingFake()
		driver.err = fmt.Errorf("%w: %q (its extension %q is no longer loaded)", dbregistry.ErrDriverUnavailable, "fakedb", "ext")
		dbdrivertest.Register(t, driver)
		q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

		_, err := q.ClassifySQLStatements(1, "SELECT 1")
		require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
		require.False(t, errors.Is(err, dbdriver.ErrCannotSplit))
		assert.Contains(t, err.Error(), "no longer loaded")
	})
}
