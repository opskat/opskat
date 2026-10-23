package connpool

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/kerberos"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// kerberosCache 是本进程（桌面端或 opsctl）的票据缓存：按资产缓存到过期，同一资产的多个实例并发
// 登录只取一次，资产的 Kerberos 设置改动即作废。
var kerberosCache = kerberos.NewCache()

// driverKerberos 是交给驱动的 Kerberos 票据来源：只给声明了 Kerberos 字段组（FieldKerberos）的驱动，
// 且只在资产的认证方式为 Kerberos 时给，否则为 nil。驱动只得知资产用 Kerberos 并按需要票据，
// 拿不到口令或 keytab；票据由宿主经资产的途径（transport.DialAddr，直连时是本机统一拨号）用 TCP
// 向 KDC 取。口令在这里解密成明文，只交给 kerberos 包，不进日志与错误。
func driverKerberos(desc dbdriver.Descriptor, assetID int64, cfg *asset_entity.DatabaseConfig, transport dbdriver.Transport) (*dbdriver.Kerberos, error) {
	if cfg.AuthMethod != asset_entity.AuthMethodKerberos || !desc.UsesField(dbdriver.FieldKerberos) {
		return nil, nil
	}
	if err := cfg.ValidateKerberos(); err != nil {
		return nil, err
	}
	pinned, err := cfg.KerberosPinnedSPN()
	if err != nil {
		return nil, err
	}
	password := ""
	if cfg.KrbPassword != "" {
		if password, err = credential_svc.Default().Decrypt(cfg.KrbPassword); err != nil {
			return nil, fmt.Errorf("解密 %s 的 krb_password 失败: %w", desc.Name, err)
		}
	}
	settings := kerberos.Settings{
		Principal: cfg.KrbPrincipal, Password: password, KeytabFile: cfg.KrbKeytabFile,
		KDCs: cfg.KerberosKDCs(),
	}
	// 没有资产 ID 的登录（测试连接）不进共用缓存：它的票据只属于这一次打开，缓存只活在这次打开里。
	cache := kerberosCache
	if assetID == 0 {
		cache = kerberos.NewCache()
	}
	return &dbdriver.Kerberos{
		Principal: cfg.KrbPrincipal,
		SPN:       pinned,
		Ticket: func(ctx context.Context, spn string) ([]byte, error) {
			return cache.Ticket(ctx, assetID, settings, spn, kerberos.DialFunc(transport.DialAddr))
		},
	}, nil
}
