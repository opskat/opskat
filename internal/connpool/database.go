package connpool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/pkg/sftp"
	"go.uber.org/zap"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/netdial"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/sshpool"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// OpenDatabase 经配置里的驱动打开一条数据库会话。
//
// 这里只做所有驱动共用的两件事：按 cfg.Driver 在注册表里找到驱动（没注册就返回点名该驱动
// 的 dbregistry.ErrDriverUnavailable，不把它当成别的驱动去连），以及把直连 / SSH 隧道 /
// SOCKS5 代理 / 代理链解析成驱动可以直接用的传输层。连接怎么建、连接级只读怎么落实、
// 连不上报什么错，都是驱动的事；返回的会话归调用方，用完 Close。
//
// password 为已解析的明文密码，cfg.Proxy.Password 为明文，均由调用方负责解密。
func OpenDatabase(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.DatabaseConfig, password string, sshPool *sshpool.Pool) (dbdriver.Session, error) {
	sshAssetID := asset.SSHTunnelID
	if sshAssetID == 0 {
		sshAssetID = cfg.SSHAssetID // backward compat
	}
	logFields := []zap.Field{
		zap.Int64("assetID", asset.ID),
		zap.String("driver", string(cfg.Driver)),
		zap.Int64("sshAssetID", sshAssetID),
		zap.Bool("readOnly", cfg.ReadOnly),
	}
	logger.Ctx(ctx).Info("open database session start", logFields...)

	session, err := openDatabase(ctx, asset.ID, cfg, password, sshAssetID, sshPool)
	if err != nil {
		logger.Ctx(ctx).Error("open database session failed", append(logFields, zap.Error(err))...)
		return nil, err
	}
	logger.Ctx(ctx).Info("open database session done", logFields...)
	return session, nil
}

func openDatabase(ctx context.Context, assetID int64, cfg *asset_entity.DatabaseConfig, password string, sshAssetID int64, sshPool *sshpool.Pool) (dbdriver.Session, error) {
	driver, err := dbregistry.Lookup(cfg.Driver)
	if err != nil {
		return nil, err
	}
	transport, err := databaseTransport(ctx, cfg, sshAssetID, sshPool)
	if err != nil {
		return nil, err
	}
	options, err := driverOptions(driver.Descriptor(), cfg.DriverOptions)
	if err != nil {
		return nil, err
	}
	tlsMaterial, err := driverTLS(driver.Descriptor(), cfg)
	if err != nil {
		return nil, err
	}
	return driver.Open(ctx, dbdriver.OpenRequest{
		Config: cfg.DriverConfig(), Password: password, DriverOptions: options, Transport: transport, AssetID: assetID,
		TLS: tlsMaterial,
	})
}

// driverTLS 是交给驱动的证书 TLS 设置：只给声明了证书字段组（FieldTLSCert）的驱动，且只在资产
// 启用了 TLS 时给，否则为 nil。填的是路径的由本进程（桌面端或 opsctl）读成内容，私钥解密成明文，
// 与口令一样只放进打开会话的请求；读不到或解不开就打不开，错误点名字段、不含内容。内容合不合法
// 由驱动的 TLS 判定。
func driverTLS(desc dbdriver.Descriptor, cfg *asset_entity.DatabaseConfig) (*dbdriver.TLSMaterial, error) {
	if !cfg.TLS || !desc.UsesField(dbdriver.FieldTLSCert) {
		return nil, nil
	}
	fields, err := decryptStoredKey(desc.Name, TLSFields{
		CAFile: cfg.TLSCAFile, CertFile: cfg.TLSCertFile, KeyFile: cfg.TLSKeyFile,
		CAPEM: cfg.TLSCAPEM, CertPEM: cfg.TLSCertPEM, KeyPEM: cfg.TLSKeyPEM,
	})
	if err != nil {
		return nil, err
	}
	contents, err := readTLSContents(desc.Name, fields)
	if err != nil {
		return nil, err
	}
	return &dbdriver.TLSMaterial{
		Insecure: cfg.TLSInsecure, ServerName: cfg.TLSServerName,
		CA: contents.ca, Cert: contents.cert, Key: contents.key,
	}, nil
}

// driverOptions 是交给驱动的专属字段取值，按驱动当前的声明整理：只交声明了的键（驱动升级删掉的
// 键不交出去），没存的取声明的默认值，必填缺失则打不开；存着的口令类取值（credential_svc 加密）
// 解密成明文，与口令一样只放进打开会话的请求。返回副本，不改资产配置。
func driverOptions(desc dbdriver.Descriptor, stored map[string]any) (map[string]any, error) {
	if len(desc.DriverFields) == 0 {
		return nil, nil
	}
	declared := make(map[string]any, len(desc.DriverFields))
	for _, f := range desc.DriverFields {
		if v, ok := stored[f.Key]; ok {
			declared[f.Key] = v
		}
	}
	options, err := desc.CompleteDriverOptions(declared)
	if err != nil {
		return nil, err
	}
	for _, f := range desc.DriverFields {
		encrypted, ok := options[f.Key].(string)
		if f.Type != dbdriver.FieldTypePassword || !ok || encrypted == "" {
			continue
		}
		plaintext, err := credential_svc.Default().Decrypt(encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt driver option %q: %w", f.Key, err)
		}
		options[f.Key] = plaintext
	}
	return options, nil
}

// databaseTransport 把配置解析成到达数据库的途径。网络连接的优先级：代理链 > SSH 隧道 >
// SOCKS5 代理 > 直连；直连 .local 主机时经统一拨号器（绕过系统解析器的 mDNS；零值参数，
// 超时仍由驱动经 ctx 控制），其余直连留给驱动默认的拨号。
//
// 按地址拨号（DialAddr）走同一条途径，只是到达驱动请求的地址：经 SSH 隧道时连那个地址本身
// （Dial 固定连资产地址，内置驱动拨的是本机解析出的 ip:port，不能改它的语义），代理与代理链
// 本就按请求的地址拨；直连时用统一拨号器。
//
// 配置了 SSH 资产时还给出那台主机的文件系统：数据库是主机上一个文件的驱动（远端 SQLite）
// 用它，按网络连接的驱动用 Dial——两者都是惰性的，用哪个由驱动决定。
func databaseTransport(ctx context.Context, cfg *asset_entity.DatabaseConfig, sshAssetID int64, sshPool *sshpool.Pool) (dbdriver.Transport, error) {
	var transport dbdriver.Transport
	if sshAssetID > 0 {
		transport.RemoteFS = func(ctx context.Context) (dbdriver.RemoteFS, error) {
			return openSFTPRemoteFS(ctx, sshAssetID, sshPool)
		}
	}

	switch {
	case cfg.ProxyChain != nil:
		dial, err := chainDialFunc(ctx, cfg.ProxyChain)
		if err != nil {
			return dbdriver.Transport{}, err
		}
		if dial != nil { // 代理链解析为空层即直连
			transport.Dial = dbdriver.DialFunc(dial.ignoreNetwork())
			transport.DialAddr = transport.Dial
		}
	case sshAssetID > 0 && sshPool != nil:
		tunnel := NewSSHTunnel(sshAssetID, cfg.Host, cfg.Port, sshPool)
		transport.Dial = dbdriver.DialFunc(tunnelDialFunc(tunnel).ignoreNetwork())
		transport.DialAddr = dbdriver.DialFunc(tunnelAddrDialFunc(tunnel).ignoreNetwork())
	case cfg.Proxy != nil:
		transport.Dial = dbdriver.DialFunc(proxyDialFunc(cfg.Proxy).ignoreNetwork())
		transport.DialAddr = transport.Dial
	}
	if transport.DialAddr == nil {
		transport.Direct = true
		transport.DialAddr = (&netdial.Dialer{}).DialContext
		if netdial.IsLocalAddr(cfg.Host) {
			transport.Dial = transport.DialAddr
		}
	}
	return transport, nil
}

// openSFTPRemoteFS 在 SSH 资产上打开文件系统：从连接池借一条 SSH 连接，在其上开 SFTP。
func openSFTPRemoteFS(ctx context.Context, sshAssetID int64, sshPool *sshpool.Pool) (dbdriver.RemoteFS, error) {
	if sshPool == nil {
		return nil, errors.New("远端 SQLite 需要 SSH 连接池")
	}
	client, err := sshPool.Get(ctx, sshAssetID)
	if err != nil {
		return nil, fmt.Errorf("get ssh connection for remote SQLite: %w", err)
	}
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		sshPool.Release(sshAssetID)
		return nil, fmt.Errorf("create SFTP client for remote SQLite: %w", err)
	}
	return &sftpRemoteFS{client: sftpClient, pool: sshPool, sshAssetID: sshAssetID}, nil
}

// sftpRemoteFS 是 SSH 资产上的文件系统。Close 关闭 SFTP 并把 SSH 连接还给连接池。
type sftpRemoteFS struct {
	client     *sftp.Client
	pool       *sshpool.Pool
	sshAssetID int64
	release    sync.Once
}

func (fs *sftpRemoteFS) OpenFile(name string, flag int) (dbdriver.RemoteFile, error) {
	f, err := fs.client.OpenFile(name, flag)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (fs *sftpRemoteFS) Remove(name string) error {
	return fs.client.Remove(name)
}

func (fs *sftpRemoteFS) Stat(name string) (os.FileInfo, error) {
	return fs.client.Stat(name)
}

func (fs *sftpRemoteFS) Close() error {
	err := fs.client.Close()
	fs.release.Do(func() { fs.pool.Release(fs.sshAssetID) })
	return err
}
