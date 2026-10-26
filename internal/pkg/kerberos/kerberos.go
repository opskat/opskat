// Package kerberos 是宿主的 Kerberos 客户端：用资产里的主体与口令（或 keytab）经调用方给的拨号途径
// 向 KDC 取 TGT 与服务票据，交出登录用的 KRB_AP_REQ。口令、keytab 内容、票据与会话密钥只留在本包，
// 不进错误与日志。
package kerberos

import (
	"context"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/types"
	"go.uber.org/zap"
)

// Settings 是一个资产的 Kerberos 设置。
type Settings struct {
	Principal  string   // user@REALM，realm 取自主体
	Password   string   // 口令；填了 KeytabFile 时不用
	KeytabFile string   // keytab 文件路径；填了就用 keytab
	KDCs       []string // 按顺序尝试的 host[:port]，默认端口 88
}

func (s Settings) equal(o Settings) bool {
	return s.Principal == o.Principal && s.Password == o.Password && s.KeytabFile == o.KeytabFile &&
		slices.Equal(s.KDCs, o.KDCs)
}

// DialFunc 是连 KDC 的拨号途径（直连、SSH 隧道、代理…），KDC 主机名由它解析。
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// expiryMargin 是票据到期前提前重取的余量，免得交出的票据在数据库验证时刚好过期。
const expiryMargin = time.Minute

// Cache 按资产缓存 TGT 与服务票据直到过期：同一资产同时的多个请求只取一次，资产的 Kerberos 设置
// 一变就作废。桌面端整个进程共用一个；opsctl 每次运行新建一个，等于每次重新取。
type Cache struct {
	now func() time.Time

	mu     sync.Mutex
	assets map[int64]*assetTickets
}

// assetTickets 是一个资产在某一份设置下取到的票据。
type assetTickets struct {
	settings Settings
	lock     chan struct{} // 同一资产同时只有一个请求在取票，其余等它取完用缓存
	tgt      *credential
	services map[string]*credential // 按驱动要的服务主体
}

// credential 是一张票据与它的会话密钥。
type credential struct {
	ticket  messages.Ticket
	key     types.EncryptionKey
	expires time.Time
}

func (c *credential) usable(now time.Time) bool {
	return c != nil && now.Add(expiryMargin).Before(c.expires)
}

// NewCache 返回空缓存。
func NewCache() *Cache {
	return &Cache{now: time.Now, assets: map[int64]*assetTickets{}}
}

// Ticket 用资产 assetKey 的设置 s 取服务主体 spn（service/host，可带 @REALM）的票据，返回登录用的
// KRB_AP_REQ。缓存里有未过期的 TGT 与服务票据就直接用，每次都生成新的认证符。
func (c *Cache) Ticket(ctx context.Context, assetKey int64, s Settings, spn string, dial DialFunc) ([]byte, error) {
	cl, err := newClient(s)
	if err != nil {
		return nil, err
	}
	sname, err := cl.serviceName(spn)
	if err != nil {
		return nil, err
	}
	entry := c.entry(assetKey, s)
	select {
	case entry.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-entry.lock }()

	svc, err := c.serviceTicket(ctx, assetKey, entry, cl, spn, sname, dial)
	if err != nil {
		return nil, err
	}
	return cl.apReq(svc)
}

// entry 取资产的缓存项；设置变了就换一个新的空项，旧票据随之作废。
func (c *Cache) entry(assetKey int64, s Settings) *assetTickets {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.assets[assetKey]
	if e == nil || !e.settings.equal(s) {
		s.KDCs = slices.Clone(s.KDCs)
		e = &assetTickets{settings: s, lock: make(chan struct{}, 1), services: map[string]*credential{}}
		c.assets[assetKey] = e
	}
	return e
}

// serviceTicket 在持有 entry.lock 时调用：缺服务票据就取（缺 TGT 先取 TGT），记日志。
func (c *Cache) serviceTicket(ctx context.Context, assetKey int64, e *assetTickets, cl *client, spn string, sname types.PrincipalName, dial DialFunc) (*credential, error) {
	now := c.now()
	if svc := e.services[spn]; svc.usable(now) {
		return svc, nil
	}
	log := logger.Ctx(ctx).With(zap.Int64("assetID", assetKey), zap.String("principal", cl.principal))
	if !e.tgt.usable(now) {
		log.Debug("kerberos: requesting TGT")
		tgt, kdc, err := cl.asExchange(ctx, dial)
		if err != nil {
			log.Error("kerberos: TGT request failed", zap.Error(err))
			return nil, err
		}
		log.Info("kerberos: TGT obtained", zap.String("kdc", kdc), zap.Time("expires", tgt.expires))
		e.tgt = tgt
	}
	log = log.With(zap.String("spn", spn))
	log.Debug("kerberos: requesting service ticket")
	svc, kdc, err := cl.tgsExchange(ctx, dial, e.tgt, sname)
	if err != nil {
		log.Error("kerberos: service ticket request failed", zap.Error(err))
		return nil, err
	}
	log.Info("kerberos: service ticket obtained", zap.String("kdc", kdc), zap.Time("expires", svc.expires))
	e.services[spn] = svc
	return svc, nil
}
