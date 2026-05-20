// Package sshadapt 收纳 binder 之间共用的 SSH 适配器类型，避免循环依赖。
//
// 目前只有 PoolDialer：ssh 和 opsctl 两个 binder 都需要把
// credential_resolver.Default().DialAssetSSH 适配成 sshpool.PoolDialer 接口。
package sshadapt

import (
	"context"
	"io"

	"github.com/opskat/opskat/internal/service/credential_resolver"
	"golang.org/x/crypto/ssh"
)

// PoolDialer 实现 sshpool.PoolDialer，委托给 credential_resolver 统一 dial。
type PoolDialer struct{}

// DialAsset 通过 credential_resolver 拨号一个 asset 的 SSH 主连接。
func (d *PoolDialer) DialAsset(ctx context.Context, assetID int64) (*ssh.Client, []io.Closer, error) {
	return credential_resolver.Default().DialAssetSSH(ctx, assetID)
}
