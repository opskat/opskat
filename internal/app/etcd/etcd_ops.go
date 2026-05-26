package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/etcd_svc"
	"github.com/opskat/opskat/internal/service/testreg"
)

// EtcdTestConnection 即时拨号验证 etcd 资产可达。
func (e *Etcd) EtcdTestConnection(assetID int64) error {
	return e.service.TestConnection(i18n.Ctx(e.ctx, e.lang.Lang()), assetID)
}

// EtcdTestConfig 在「资产表单」上测试未保存的 etcd 配置。
//
//	testID 用于配合「取消测试」: 前端可在 CancelTest(testID) 时中断拨号。
//	configJSON 是前端 EtcdConfig 的 JSON 序列化。
//	plainPassword 走 inline 路径时直接使用; 空时通过 ResolvePasswordGeneric 兜底
//	(支持 managed 凭证 / 已存在密文)。
func (e *Etcd) EtcdTestConfig(testID string, configJSON string, plainPassword string) error {
	var cfg asset_entity.EtcdConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}

	parent, parentCancel := context.WithTimeout(i18n.Ctx(e.ctx, e.lang.Lang()), 10*time.Second)
	defer parentCancel()
	ctx, release := testreg.Begin(parent, testID)
	defer release()

	password := plainPassword
	if password == "" {
		var err error
		password, err = credential_resolver.Default().ResolvePasswordGeneric(ctx, &cfg)
		if err != nil {
			logger.Ctx(ctx).Error("etcd test connection resolve password failed",
				zap.String("testID", testID), zap.Error(err))
			return fmt.Errorf("连接失败: %w", err)
		}
	}

	return e.service.TestConfig(ctx, &cfg, password)
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
