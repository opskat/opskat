package policy

import (
	"context"
	"errors"
	"fmt"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// ClassifyForAsset 切分并分类 sql：资产的驱动声明了语句切分分类（CapClassifyStatements）时
// 由驱动给出每条语句，否则（内置驱动、未声明的驱动、没有资产的纯组策略）走宿主自己的
// TiDB 解析（ClassifyStatements）。驱动没有在注册表里（扩展停用/卸载）报包着
// dbregistry.ErrDriverUnavailable 的错误；驱动自己的错误原样带着它的原因。
//
// AI、opsctl、策略测试与"总是允许"落规则都经这里，不各自再分一次叉。
func ClassifyForAsset(ctx context.Context, asset *asset_entity.Asset, sqlText string) ([]StatementInfo, error) {
	// 没有数据库配置（或配置里没写驱动）的资产没有驱动可问，与纯组策略一样走宿主解析。
	if asset == nil || !asset.IsDatabase() || asset.Config == "" {
		return ClassifyStatements(sqlText)
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Driver == "" {
		return ClassifyStatements(sqlText)
	}
	d, err := dbregistry.Lookup(cfg.Driver)
	if err != nil {
		return nil, err
	}
	if !d.Descriptor().Supports(dbdriver.CapClassifyStatements) {
		return ClassifyStatements(sqlText)
	}
	stmts, err := dbdriver.ClassifyStatements(ctx, d, sqlText)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, fmt.Errorf("driver %q: empty SQL", d.Descriptor().ID)
	}
	infos := make([]StatementInfo, 0, len(stmts))
	for _, s := range stmts {
		infos = append(infos, StatementInfo{Type: s.Type, Raw: s.Text, Dangerous: s.Dangerous, Reason: s.Reason})
	}
	return infos, nil
}

// ClassifyDenyMessage 是 ClassifyForAsset 失败时给调用方的拒绝原因：驱动不可用单独说明，
// 其余（解析失败、驱动报错）沿用 "SQL 解析失败" 的说法并带上原因。
func ClassifyDenyMessage(ctx context.Context, err error) string {
	if errors.Is(err, dbregistry.ErrDriverUnavailable) {
		return PolicyFmt(ctx, "Database driver unavailable, execution denied: %v", "数据库驱动不可用，拒绝执行: %v", err)
	}
	return PolicyFmt(ctx, "SQL parse failed, execution denied: %v", "SQL 解析失败，拒绝执行: %v", err)
}
