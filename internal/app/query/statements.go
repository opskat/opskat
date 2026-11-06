package query

import (
	"context"
	"errors"
	"fmt"

	"github.com/opskat/opskat/internal/app/i18n"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// ClassifySQLStatements 用资产的驱动切分并分类 sqlText，不连数据库。查询面板在资产的驱动声明了
// 语句切分分类（dbdriver.CapClassifyStatements）时，按它定位当前语句、判断危险确认；返回的偏移是
// sqlText 里的字节偏移，由前端换成编辑器的位置。驱动不可用（扩展停用或卸载）与驱动切不了分别报错，
// 错误带着驱动给的原因；没有任何语句时返回空列表。
func (q *Query) ClassifySQLStatements(assetID int64, sqlText string) ([]dbdriver.Statement, error) {
	lang := q.lang.Lang()
	ctx := i18n.Ctx(q.ctx, lang)
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsDatabase() {
		return nil, fmt.Errorf("资产不是数据库类型")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return nil, fmt.Errorf("获取数据库配置失败: %w", err)
	}
	statements, err := classifyWith(ctx, cfg.Driver, sqlText)
	switch {
	case errors.Is(err, dbregistry.ErrDriverUnavailable):
		return nil, fmt.Errorf("%s: %w", i18n.Pick(lang, "数据库驱动不可用，无法切分语句", "The database driver is unavailable, so the statements can't be split"), err)
	case errors.Is(err, dbdriver.ErrCannotSplit):
		return nil, fmt.Errorf("%s: %w", i18n.Pick(lang, "驱动无法切分这段 SQL", "The driver couldn't split this SQL"), err)
	case err != nil:
		return nil, fmt.Errorf("%s: %w", i18n.Pick(lang, "切分语句失败", "Failed to split the statements"), err)
	}
	if statements == nil {
		return []dbdriver.Statement{}, nil
	}
	return statements, nil
}

// classifyWith 按驱动 id 取驱动并切分 sqlText。
func classifyWith(ctx context.Context, id dbdriver.DriverID, sqlText string) ([]dbdriver.Statement, error) {
	driver, err := dbregistry.Lookup(id)
	if err != nil {
		return nil, err
	}
	return dbdriver.ClassifyStatements(ctx, driver, sqlText)
}
