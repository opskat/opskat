// Package oss 实现 OSS(对象存储)binder:Bucket/对象浏览、连接测试。
package oss

import (
	"context"
	"fmt"
	"sync"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/jsonfield"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/oss_svc"
)

// LangProvider 提供当前 UI 语言,用于 i18n 上下文。
type LangProvider interface{ Lang() string }

// OSS binder。
type OSS struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	service *oss_svc.Service
	cancels sync.Map // transferID -> context.CancelFunc(在途传输取消注册表,仿 sftp_svc)
	pending sync.Map // transferID -> func(); 前端订阅进度事件后显式启动，避免终态事件丢失
}

// New 构造 OSS binder。
func New(appCtx context.Context, lang LangProvider) *OSS {
	o := &OSS{appCtx: appCtx, lang: lang, service: oss_svc.New()}
	conntest.Register(asset_entity.AssetTypeOSS, o.testConnection)
	return o
}

// Startup 保存 Wails ctx。
func (o *OSS) Startup(ctx context.Context) { o.ctx = ctx }

// Cleanup 占位。
func (o *OSS) Cleanup() {}

func (o *OSS) i18nCtx() context.Context { return i18n.Ctx(o.ctx, o.lang.Lang()) }

// testConnection 是通用表单"测试连接"经 conntest 分派的钩子。
//
//	plainSecret 走 inline 路径时直接使用; 空时通过 ResolvePasswordGeneric 兜底
//	(支持 managed 凭证 / 已存在密文)。
func (o *OSS) testConnection(ctx context.Context, configJSON, plainSecret string) error {
	cfg, err := jsonfield.Unmarshal[asset_entity.OSSConfig](configJSON, "OSS配置")
	if err != nil {
		return err
	}

	secret := plainSecret
	if secret == "" {
		secret, err = credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
	}

	return o.service.TestConfig(ctx, cfg, secret)
}
