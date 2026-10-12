package dbdriver

import (
	"context"
	"errors"
	"io"
	"net"
	"os"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ErrUnsupported 表示驱动不支持被要求的操作（没有声明对应的功能，或它的会话做不了这件事）。
// 驱动返回的错误包着它；调用方据此区分「这个驱动不做」与「做了但失败」。
var ErrUnsupported = errors.New("operation not supported by database driver")

// Connector 是驱动契约里「建立会话」的那一半。
type Connector interface {
	// Open 按配置建立一条会话，并在返回前确认数据库可达：连不上就在这里返回驱动自己的错误。
	//
	// 配置要求连接级只读（Config.ReadOnly）时由驱动落实。没有声明 CapReadOnly 的驱动必须
	// 以 ErrUnsupported 拒绝，而不是悄悄打开一条可写的会话。
	Open(ctx context.Context, req OpenRequest) (Session, error)
}

// OpenRequest 是打开一条会话所需的全部输入。
type OpenRequest struct {
	// Config 是要连接的配置；调用方可能已经用本次操作指定的库名覆盖了 Database。
	Config *asset_entity.DatabaseConfig
	// Password 是已解析的明文口令。
	Password string
	// Transport 是共享代码解析好的「怎么到达数据库」。
	Transport Transport
}

// Transport 是到达数据库的途径。隧道、代理、代理链的配置解析与凭据解密都在共享代码里
// 做完，驱动只拿到结果，不再自己读 Config 里的 Proxy / ProxyChain / SSHAssetID。
type Transport struct {
	// Dial 非 nil 时，驱动到数据库的每一条网络连接都必须经它建立（SSH 隧道、SOCKS5 代理、
	// 代理链，或直连 .local 主机时绕过 mDNS 的拨号器）。为 nil 表示直连，用驱动自己的默认拨号。
	Dial DialFunc
	// RemoteFS 非 nil 表示配置了一台 SSH 主机：数据库是那台主机上的文件的驱动（远端 SQLite）
	// 经它打开主机的文件系统。按网络连接的驱动不用它。调用一次占用一条 SSH 连接，由返回值的
	// Close 归还。
	RemoteFS func(ctx context.Context) (RemoteFS, error)
}

// DialFunc 按 network / addr 建立到数据库的底层连接。
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// DialContext 让 DialFunc 可以直接当作各数据库客户端库要求的 Dialer 使用。
func (f DialFunc) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return f(ctx, network, addr)
}

// RemoteFS 是一台远端主机上的文件系统。
type RemoteFS interface {
	OpenFile(name string, flag int) (RemoteFile, error)
	Remove(name string) error
	Stat(name string) (os.FileInfo, error)
	// Close 归还这套文件系统占用的连接。
	io.Closer
}

// RemoteFile 是 RemoteFS 上打开的一个文件。
type RemoteFile interface {
	io.Closer
	io.ReaderAt
	io.WriterAt
	Truncate(size int64) error
	Sync() error
	Stat() (os.FileInfo, error)
}

// Session 是驱动打开并持有的一条数据库会话。会话底下是什么——连接池、单条连接、还是扩展
// 里的一个句柄——只有驱动知道：共享代码只经这些方法操作，拿到的是结构化结果，不拼方言
// SQL，也碰不到驱动的连接。
//
// 实现必须可以并发调用：查询面板与 AI 的批量执行会在多个 goroutine 上共用同一条会话。
type Session interface {
	// Execute 原样执行一条语句。语句返回结果集时 Result.Rows 非 nil，否则给出受影响行数。
	Execute(ctx context.Context, statement string) (*Result, error)
	// ExecutePaged 执行一条语句并只取其中一页，同时给出不分页时的总行数
	// （RowSet.Total）。怎么分页、怎么计数由驱动按自己的方言决定；不能分页的语句照
	// Execute 执行，此时 RowSet.Total 为 nil。
	ExecutePaged(ctx context.Context, statement string, page Page) (*Result, error)
	// Close 释放会话占用的一切（连接、文件锁、远端文件系统）。
	io.Closer
}

// Page 指定分页执行要取的那一页。
type Page struct {
	// Index 是页号，从 0 起。
	Index int
	// Size 是每页行数。
	Size int
}

// Result 是一条语句的执行结果。
type Result struct {
	// Rows 非 nil 表示语句返回了结果集（哪怕是空集）；为 nil 表示语句不返回行，看 AffectedRows。
	Rows *RowSet
	// AffectedRows 是不返回行的语句影响的行数。
	AffectedRows int64
}

// RowSet 是一个结果集。
type RowSet struct {
	// Columns 是列名，按结果集里的顺序。
	Columns []string
	// Rows 的每一行按 Columns 的顺序给值，长度与 Columns 相同。值必须能直接序列化成
	// JSON：把数据库的原生类型转成普通值是驱动的事。
	Rows [][]any
	// Total 是不分页时的总行数；只有 ExecutePaged 确实对语句分了页才非 nil。
	Total *int64
}
