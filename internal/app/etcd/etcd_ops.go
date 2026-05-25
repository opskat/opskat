package etcd

import (
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/service/etcd_svc"
)

// EtcdTestConnection 即时拨号验证 etcd 资产可达。
func (e *Etcd) EtcdTestConnection(assetID int64) error {
	return e.service.TestConnection(i18n.Ctx(e.ctx, e.lang.Lang()), assetID)
}

// EtcdExec 执行 etcd 操作(get/put/del/lease/member/endpoint),来源标记为查询面板。
func (e *Etcd) EtcdExec(req etcd_svc.ExecRequest) (*etcd_svc.ExecResult, error) {
	if req.Source == "" {
		req.Source = "query"
	}
	return e.service.Exec(i18n.Ctx(e.ctx, e.lang.Lang()), &req)
}

// EtcdListPrefix 按前缀分层列出 keys(用于 KV 树懒加载)。
func (e *Etcd) EtcdListPrefix(req etcd_svc.ListPrefixRequest) (*etcd_svc.ListPrefixResult, error) {
	return e.service.ListPrefix(i18n.Ctx(e.ctx, e.lang.Lang()), &req)
}
