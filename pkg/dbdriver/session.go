package dbdriver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
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
	// SessionPerDatabase 声明一条会话对应什么：true 表示它只连着 Config.Database 指定的那
	// 一个库，换库要另开一条（库名写进连接串的驱动）；false 表示一条会话覆盖整个资产，
	// 调用方传来的库名只是会话里的一个名字空间（SQLite：库就是那个文件）。
	//
	// 共享代码据此决定复用会话的粒度（按资产，还是按资产 + 库），自己不按驱动名判断。
	SessionPerDatabase() bool
}

// OpenRequest 是打开一条会话所需的全部输入。
type OpenRequest struct {
	// Config 是要连接的配置；调用方可能已经用本次操作指定的库名覆盖了 Database。
	Config *Config
	// Password 是已解析的明文口令。
	Password string
	// DriverOptions 是资产的驱动专属字段（Descriptor.DriverFields）的取值，按键取；口令类的值
	// 与 Password 一样已解密成明文、只在打开会话时给出。不声明专属字段的驱动（内置驱动）忽略它。
	DriverOptions map[string]any
	// Transport 是共享代码解析好的「怎么到达数据库」。
	Transport Transport
	// AssetID 是这条会话所属的数据库资产，只用来在宿主的日志里点名资产；驱动不靠它连接。
	AssetID int64
	// TLS 是资产的证书 TLS 设置：只在驱动声明了证书字段组（FieldTLSCert）且资产启用了 TLS
	// 时给出，否则为 nil（内置驱动不声明，始终为 nil，照旧按 Config.TLS / SSLMode 处理）。
	// 内容与 Password 一样只在打开会话时给出，不进日志。
	TLS *TLSMaterial
	// Kerberos 是资产的 Kerberos 票据来源：只在驱动声明了 Kerberos 字段组（FieldKerberos）且资产
	// 的认证方式为 Kerberos 时给出，否则为 nil（内置驱动不声明，始终为 nil）。给出时 Password 不用：
	// 驱动在登录握手中按数据库给的服务名与主机名经 Kerberos.Ticket 要票据。
	Kerberos *Kerberos
}

// Kerberos 是一个资产登录数据库用的 Kerberos 票据来源。口令、keytab 与会话密钥都在 Ticket 背后，
// 不在这里：打开会话的进程持有它们，驱动只拿到票据。
type Kerberos struct {
	// Principal 是资产的主体（user@REALM），只用来在宿主的日志里点名；扩展里的驱动拿到的为空。
	Principal string
	// SPN 是资产固定的服务主体（service/host，不带 realm）；为空表示按驱动要的取。扩展里的驱动
	// 拿到的为空：要别的服务主体由宿主拒绝。
	SPN string
	// Ticket 返回登录服务主体 spn（KerberosSPN 的形式）用的票据（Kerberos AP-REQ 的字节），
	// 原样写进数据库的登录握手；失败时返回可读的原因，不含口令、keytab 内容、票据或密钥。
	Ticket func(ctx context.Context, spn string) ([]byte, error)
}

// KerberosSPN 是数据库在登录握手中给出的服务名 service 与主机名 host 对应的服务主体：service/host
// （go-ora 的 Authenticate(server, service) 即 KerberosSPN(service, server)）。
func KerberosSPN(service, host string) string {
	return service + "/" + host
}

// CheckKerberosSPN 检查 spn 是否为宿主放行的服务主体形式 service/host（不带 realm）：两段都不为空，
// 都不含 "/" 或 "@"。宿主拒绝别的形式的票据请求，资产固定的服务主体保存时按同一规则校验。
func CheckKerberosSPN(spn string) error {
	service, host, ok := strings.Cut(spn, "/")
	if !ok || service == "" || host == "" || strings.Contains(service, "@") || strings.ContainsAny(host, "/@") {
		return fmt.Errorf("%q is not a Kerberos service principal of the form service/host (neither name may contain \"/\" or \"@\")", spn)
	}
	return nil
}

// TLSMaterial 是驱动自己完成 TLS 所需的设置与证书内容。填的是路径的，打开会话的进程已读成
// 内容；私钥已解密。内容原样交出（通常是 PEM），合不合法由驱动的 TLS 判定。没填的项为空。
type TLSMaterial struct {
	// Insecure 要求跳过服务器证书校验。
	Insecure bool `json:"insecure,omitempty"`
	// ServerName 是校验证书与 SNI 用的服务器名；为空时由驱动按它连接的主机名取。
	ServerName string `json:"serverName,omitempty"`
	// CA 是校验服务器证书的 CA 证书；为空时用系统的信任根。
	CA []byte `json:"ca,omitempty"`
	// Cert / Key 是双向 TLS 的客户端证书与（明文）私钥。
	Cert []byte `json:"cert,omitempty"`
	Key  []byte `json:"key,omitempty"`
}

// Transport 是到达数据库的途径。隧道、代理、代理链的配置解析与凭据解密都在共享代码里
// 做完，驱动只拿到结果，不再自己读 Config 里的 Proxy / ProxyChain / SSHAssetID。
type Transport struct {
	// Dial 非 nil 时，驱动到数据库的每一条网络连接都必须经它建立（SSH 隧道、SOCKS5 代理、
	// 代理链，或直连 .local 主机时绕过 mDNS 的拨号器）。为 nil 表示直连，用驱动自己的默认拨号。
	Dial DialFunc
	// DialAddr 经同一途径按请求的地址拨号：与 Dial 不同，经 SSH 隧道时连到 addr 本身，而不是固定
	// 连资产的地址；经隧道、代理或代理链时主机名在远端解析。给跟随数据库重定向（集群节点、只读
	// 副本、路由的监听器）的扩展驱动用；内置驱动只用 Dial。为 nil 表示直连，用本机的统一拨号。
	DialAddr DialFunc
	// Direct 表示途径是直连：主机名在本机解析，没有隧道、代理或代理链。
	Direct bool
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

	// 以下是树的浏览：驱动在自己的连接上、按自己的方言完成，返回结构化结果。库名
	// （database）的含义同 SessionPerDatabase：库名写进连接串的驱动里它就是会话所连的库，
	// 会话覆盖整个资产的驱动里它是其中的名字空间；传空表示驱动的默认。驱动没提供的操作返回
	// 包着 ErrUnsupported 的错误，且不执行任何语句。

	// ListDatabases 列出资产上可浏览的库（驱动自己决定隐藏哪些系统库，没有任何库时给出的
	// 默认名字空间）。
	ListDatabases(ctx context.Context) ([]string, error)
	// ListTables 列出库里的表与视图，按驱动的排序。有 schema 分组的驱动给 "schema.table"
	// 形式的限定名。
	ListTables(ctx context.Context, database string) ([]string, error)
	// TableMetadata 返回一张表的列、索引与外键。table 用 ListTables 给出的形式。
	TableMetadata(ctx context.Context, database, table string) (*TableMetadata, error)
	// ListObjects 返回库里的视图 / 存储过程 / 函数 / 触发器；驱动没有声明的那几类为空。
	ListObjects(ctx context.Context, database string) (*DatabaseObjects, error)
	// ObjectSource 返回一个对象的只读定义。objType 是 Object* 常量之一，没有声明对应功能
	// 的返回 ErrUnsupported。table 只有 ObjectMeta.Table 非空时才需要。
	ObjectSource(ctx context.Context, database, objType, name, table string) (string, error)

	// 以下是表浏览：同样由驱动在自己的连接上完成。table 用 ListTables 给出的形式；筛选与
	// 排序是结构化的（见 TableQuery），驱动据此生成自己方言的查询。驱动没提供的返回包着
	// ErrUnsupported 的错误，且不执行任何语句。

	// OpenTable 一次给出打开一张表所需的首屏：列与类型、列约束、主键、总行数，以及不带
	// 筛选与排序的第一页（pageSize 行）。
	OpenTable(ctx context.Context, database, table string, pageSize int) (*OpenedTable, error)
	// TableRows 按筛选与排序取表里的一页。结果不带总行数（RowSet.Total 为 nil），总行数
	// 另由 CountTableRows 给出。
	TableRows(ctx context.Context, database, table string, query TableQuery, page Page) (*RowSet, error)
	// CountTableRows 返回表里符合筛选条件的行数；没有条件时是表的总行数。
	CountTableRows(ctx context.Context, database, table string, filters []FilterItem) (int64, error)
	// StreamTableRows 用一次查询取出表里符合筛选条件的全部行（按排序项排序，不分页），
	// 以游标交给调用方逐行读取：导出整张表用它，行不会整个攒在内存里，也不会因为分成
	// 多条语句而在没有稳定顺序时重复或漏行。调用方读完或放弃都必须 Close 返回的游标。
	StreamTableRows(ctx context.Context, database, table string, query TableQuery) (RowStream, error)

	// 以下是表的两项文本产出：同样由驱动按自己的方言生成，共享代码和前端都不拼方言 SQL。

	// TableDDL 返回一张表（或视图）的建表语句；表不存在或驱动给不出时返回空串。表名用
	// ListTables 给出的形式，带 schema 的驱动按表真正所在的 schema 解析。没有声明 CapViewDDL
	// 的驱动返回包着 ErrUnsupported 的错误，且不执行任何语句。
	TableDDL(ctx context.Context, database, table string) (string, error)
	// StarterQuery 返回 SQL 编辑器的起始查询语句：取这张表前 limit 行，表引用与取前几行的
	// 写法都是驱动自己的方言。驱动没提供时返回包着 ErrUnsupported 的错误。
	StarterQuery(ctx context.Context, database, table string, limit int) (string, error)

	// 以下是写路径的语句生成：驱动在自己的会话上、按自己的方言生成语句并返回，只生成、不执行；
	// 调用方展示后再经 Execute 执行的正是这些语句。生成时驱动可以先查表的元数据（列类型等）
	// 来决定写法。table 用 ListTables 给出的形式。

	// PrepareRowStatements 在会话上读好为这张表生成行语句要的一切（列类型、随会话设置变化的
	// 字面量规则），交回一个之后不再碰数据库的 RowStatementWriter：返回前用过的连接已经还回，
	// 所以调用方可以在另一个查询占着会话的连接时（整表导出为 SQL：先备好，再打开读取的游标，
	// 边读边写语句）用它生成语句。驱动没有声明用到它的功能（Descriptor.GeneratesRowStatements）
	// 时返回包着 ErrUnsupported 的错误，且不执行任何语句。
	PrepareRowStatements(ctx context.Context, database, table string) (RowStatementWriter, error)

	// GridEditStatements 生成表格编辑要执行的语句：每个新增行一条 INSERT、每个改动的行一条只改
	// 那一行的 UPDATE、每个要删除的行一条只删那一行的 DELETE，各自按请求里的次序（见
	// GridEditRequest）。定位已有行、没有主键时怎么收敛到一行、整行取默认值的写法、往自增列写值
	// 时要的开关都由驱动决定。请求里的列不在表中（或表已不存在）时返回 error。驱动没有声明
	// CapGridEdit 时返回包着 ErrUnsupported 的错误，且不执行任何语句。
	GridEditStatements(ctx context.Context, database, table string, req GridEditRequest) (*GridEditStatements, error)

	// CreateTableStatements 生成建表语句（只生成、不执行）：表名、列名按方言加引号，列的类型文本
	// 原样写入，默认值的写法见 NewColumn.DefaultValue。req.Name 用 ListTables 给出的形式。驱动没有
	// 声明 CapCreateTable 时返回包着 ErrUnsupported 的错误，且不执行任何语句。
	CreateTableStatements(ctx context.Context, database string, req CreateTableRequest) ([]string, error)

	// DropTableStatements 生成删除一张表的语句（只生成、不执行）。驱动没有声明 CapDropTable 时返回
	// 包着 ErrUnsupported 的错误，且不执行任何语句。
	DropTableStatements(ctx context.Context, database, table string) ([]string, error)

	// TruncateTableStatements 生成清空一张表的语句（只生成、不执行）：没有 TRUNCATE 的方言用
	// DELETE FROM。驱动没有声明 CapTruncateTable 时返回包着 ErrUnsupported 的错误，且不执行任何语句。
	TruncateTableStatements(ctx context.Context, database, table string) ([]string, error)

	// CreateDatabaseStatements 生成新建数据库的语句（只生成、不执行）。req.Options 的键只能是驱动
	// 在 Descriptor.CreateDatabaseOptions 里声明过的；值为空表示不写该选项（用服务器默认）。驱动
	// 没有声明 CapCreateDatabase 时返回包着 ErrUnsupported 的错误，且不执行任何语句。
	CreateDatabaseStatements(ctx context.Context, req CreateDatabaseRequest) ([]string, error)

	// AlterTableModel 读出改表对话框打开时的这张表：不带 schema 的表名、表注释，以及各列的名称、
	// 类型、可空、默认值与注释，按列在表里的次序（见 AlterTableModel）。表不存在时返回 error。驱动
	// 没有声明 CapAlterTable 时返回包着 ErrUnsupported 的错误，且不执行任何语句。
	AlterTableModel(ctx context.Context, database, table string) (*AlterTableModel, error)

	// AlterTableStatements 生成把表改成 req 的语句（只生成、不执行），按执行的次序；没有任何改动时
	// 语句为空列表。驱动在自己的会话上读这张表现在的定义，与 req 比较得出改动（比较的方式同
	// AlterTableRequest 的说明），改列时保留对话框里不显示、用户也没有改的列定义。req 要改驱动没有
	// 声明可改的属性（Descriptor.AlterTableAttributes），或说到的列不在表中（表已不存在）时返回
	// error。驱动没有声明 CapAlterTable 时返回包着 ErrUnsupported 的错误，且不执行任何语句。
	AlterTableStatements(ctx context.Context, database, table string, req AlterTableRequest) (*AlterTableStatements, error)

	// ImportStatements 生成导入要执行的语句（只生成、不执行），按执行的次序：各导入模式的写法（见
	// ImportMode）、字面量、往自增列写值时要的开关都由驱动决定。请求里的列不在表中（或表已不存在）、
	// 一行的值与列数不符时返回 error。没有列时返回空列表。驱动没有声明 CapImport 时返回包着
	// ErrUnsupported 的错误，且不执行任何语句。
	ImportStatements(ctx context.Context, database, table string, req ImportRequest) ([]string, error)

	// ExecuteBatch 在会话的一条连接上按次序执行一批语句（导入的整批执行用它），见 BatchRequest。
	// 空白语句跳过、不执行。语句本身的失败记在结果里，不作为 error 返回。
	//
	// 请求关闭外键检查时，驱动在执行前（事务之外）关闭、结束后恢复成关闭之前的样子（本来就没在
	// 检查的连接不会被打开检查）——语句失败、回滚、ctx 被取消也要恢复；驱动没有声明 Descriptor.CanDisableForeignKeyChecks 时以 ErrUnsupported 拒绝，
	// 且不执行任何语句。
	//
	// 批本身出错（拿不到连接，关闭 / 恢复外键检查、开始 / 提交 / 回滚事务失败，ctx 被取消）时
	// 返回 error；此前已经执行过语句的，同时返回记着它们的结果。
	ExecuteBatch(ctx context.Context, req BatchRequest) (*BatchResult, error)

	// Close 释放会话占用的一切（连接、文件锁、远端文件系统）。
	io.Closer
}

// RowStatementKind 是行语句的种类。
type RowStatementKind string

const (
	// RowInsert：每行一条 INSERT，写入 Columns 列。
	RowInsert RowStatementKind = "insert"
	// RowUpdate：每行一条只改那一行的 UPDATE，把 Columns 列设为行里的值。
	RowUpdate RowStatementKind = "update"
)

// RowStatementWriter 为一张表生成行语句（Session.PrepareRowStatements 备好的）。
type RowStatementWriter interface {
	// RowStatements 生成把若干行写进表的语句，每行一条，按 req.Rows 的次序：RowInsert 是 INSERT，
	// RowUpdate 是只改那一行的 UPDATE（见 RowStatementsRequest）。没有行时返回空列表。只生成，
	// 不执行，也不碰数据库。
	RowStatements(req RowStatementsRequest) ([]string, error)
}

// RowStatementsRequest 是 RowStatementWriter.RowStatements 要生成的语句。
type RowStatementsRequest struct {
	Kind RowStatementKind `json:"kind"`
	// Columns 是 INSERT 写入、UPDATE 设置的列，按语句里的次序。
	Columns []string `json:"columns"`
	// Rows 是各行的值，按列名取：表格里读到的值经前端原样带回（nil、字符串、数字、布尔），
	// 行里没有的列按 NULL 写。
	Rows []map[string]any `json:"rows"`
	// PrimaryKeys 是 UPDATE 定位那一行的列（值取自同一行）；为空时按全部 Columns 定位。
	// INSERT 不用它。
	PrimaryKeys []string `json:"primaryKeys"`
}

// GridEditRequest 是 Session.GridEditStatements 要生成的语句：表格里新增、改动与删除的行。
type GridEditRequest struct {
	// Columns 是表格里的全部列，按表格的次序：表没有主键时按它们的原值定位已有的行。
	Columns []string `json:"columns"`
	// PrimaryKeys 是定位已有行的主键列，按主键里的次序；为空表示表没有主键。
	PrimaryKeys []string `json:"primaryKeys"`
	// Inserts 是新增的行。
	Inserts []GridInsert `json:"inserts"`
	// Updates 是改动的已有行。
	Updates []GridUpdate `json:"updates"`
	// Deletes 是要删除的已有行：表格里读到的整行（按列名取值），用来定位。
	Deletes []map[string]any `json:"deletes"`
}

// GridInsert 是一个新增行。
type GridInsert struct {
	// Values 是填写过的列与值，按语句里的次序；为空表示整行取默认值。
	Values []ColumnValue `json:"values"`
}

// GridUpdate 是一个改动的已有行。
type GridUpdate struct {
	// Row 是表格里读到的这一行（按列名取值，改动前的原值），用来定位。
	Row map[string]any `json:"row"`
	// Set 是改动的列与新值，按语句里的次序。
	Set []ColumnValue `json:"set"`
}

// ColumnValue 是一列与它的值。值是表格里的值，经前端原样带回（nil、字符串、数字、布尔）。
type ColumnValue struct {
	Column string `json:"column"`
	Value  any    `json:"value"`
}

// GridEditStatements 是表格编辑的语句，与 GridEditRequest 的三组逐一对应（同样长、同样的次序）。
type GridEditStatements struct {
	Inserts []string `json:"inserts"`
	Updates []string `json:"updates"`
	Deletes []string `json:"deletes"`
}

// NewColumn 是建表对话框里的一列。
type NewColumn struct {
	Name string `json:"name"`
	// Type 是类型文本，原样写入（首尾空白除外）。
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	// DefaultValue 是用户填的默认值文本，空表示没有默认值。数字、TRUE / FALSE / NULL、
	// CURRENT_TIMESTAMP / NOW() 原样写入，其余当作字符串加引号。
	DefaultValue string `json:"defaultValue"`
}

// CreateTableRequest 是 Session.CreateTableStatements 要生成的建表语句。
type CreateTableRequest struct {
	Name    string      `json:"name"`
	Columns []NewColumn `json:"columns"`
}

// CreateDatabaseRequest 是 Session.CreateDatabaseStatements 要生成的建库语句。
type CreateDatabaseRequest struct {
	Name string `json:"name"`
	// Options 是驱动声明的选项值，按选项的键取。
	Options map[string]string `json:"options"`
}

// ColumnDefinition 是改表对话框里一列可编辑的部分。
type ColumnDefinition struct {
	Name string `json:"name"`
	// Type 是类型文本，写进语句时原样（首尾空白除外）。
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	// DefaultValue 是默认值文本，空表示没有默认值；写进语句的规则同 NewColumn.DefaultValue。
	DefaultValue string `json:"defaultValue"`
	// Comment 是列注释，空表示没有注释。
	Comment string `json:"comment"`
}

// AlterTableModel 是改表对话框打开时看到的表。
type AlterTableModel struct {
	// Name 是改名输入框里的表名：不带 schema。
	Name string `json:"name"`
	// Comment 是表注释，没有时为空串。
	Comment string `json:"comment"`
	// Columns 按列在表里的次序。
	Columns []ColumnDefinition `json:"columns"`
}

// AlterTableRequest 是改表对话框提交的改动：改完之后这张表的样子。
//
// 已有的列按 OriginalName 对到表里现在的那一列，与它比较：名称不同是改名，类型去掉首尾空白后
// 不分大小写比较，默认值与注释去掉首尾空白后比较。都相同的列不生成语句。
type AlterTableRequest struct {
	// Name 是新表名，不带 schema（改名后表仍在原 schema）；为空或与现在的相同表示不改名。
	Name string `json:"name"`
	// Comment 是表注释；去掉首尾空白后与现在的相同表示不改。
	Comment string `json:"comment"`
	// Columns 是保留与新增的列，按对话框里的次序。
	Columns []AlterColumn `json:"columns"`
	// Drops 是要删除的已有列。
	Drops []string `json:"drops"`
}

// AlterColumn 是改表请求里的一列。
type AlterColumn struct {
	// OriginalName 是这一列在表里现在的名字；为空表示新增的列。
	OriginalName string `json:"originalName"`
	// 以下同 ColumnDefinition：改表之后这一列的样子。
	Name         string `json:"name"`
	Type         string `json:"type"`
	Nullable     bool   `json:"nullable"`
	DefaultValue string `json:"defaultValue"`
	Comment      string `json:"comment"`
}

// Definition 是这一列改表之后的样子。
func (c AlterColumn) Definition() ColumnDefinition {
	return ColumnDefinition{Name: c.Name, Type: c.Type, Nullable: c.Nullable, DefaultValue: c.DefaultValue, Comment: c.Comment}
}

// AlterTableStatements 是改表的语句。
type AlterTableStatements struct {
	// Statements 按执行的次序；任何一条失败都应停下。
	Statements []string `json:"statements"`
	// Table 是语句执行后这张表的名字，用 ListTables 给出的形式（改了名就是新名字，schema 不变）。
	Table string `json:"table"`
}

// ImportMode 是导入模式：已有的行（按 ImportRequest.Keys 匹配）怎么处理。
type ImportMode string

const (
	// ImportAppend：每行插入。
	ImportAppend ImportMode = "append"
	// ImportUpdate：按键更新已有的行，不插入。
	ImportUpdate ImportMode = "update"
	// ImportAppendUpdate：键已存在的行更新，其余插入（upsert）。
	ImportAppendUpdate ImportMode = "append-update"
	// ImportAppendSkip：键已存在的行跳过，其余插入。
	ImportAppendSkip ImportMode = "append-skip"
	// ImportDelete：按键删除已有的行。
	ImportDelete ImportMode = "delete"
	// ImportCopy：先清空表，再每行插入。
	ImportCopy ImportMode = "copy"
)

// ImportRequest 是 Session.ImportStatements 要生成的导入语句：映射到表上的行，以及怎么写进去。
//
// 要按键匹配已有行的模式（更新、追加/更新、追加/跳过、删除）在 Columns 里没有键列时不生成任何
// 语句；更新模式没有可更新的列时同样不生成。更新类模式不写数据库不允许更新的自增列（SQL Server
// IDENTITY、PostgreSQL GENERATED ALWAYS）：已有行的其余列照常更新，自增列保持原值。
type ImportRequest struct {
	Mode ImportMode `json:"mode"`
	// Columns 是写入的目标列，按语句里的次序。
	Columns []string `json:"columns"`
	// Keys 是匹配已有行的键列；只有同时在 Columns 里的才用来匹配。
	Keys []string `json:"keys"`
	// Rows 是各行的值，与 Columns 一一对应。
	Rows [][]ImportValue `json:"rows"`
	// ExtendedInsert 为真时把多行合并成一条多行 INSERT，每条不超过 MaxStatementSizeKB（可以带小数，
	// 小于 1 按 1 算）；方言写不成多行的模式（SQL Server 的 MERGE）仍每行一条。
	ExtendedInsert     bool    `json:"extendedInsert"`
	MaxStatementSizeKB float64 `json:"maxStatementSizeKb"`
}

// ImportValueKind 是导入值的种类。
type ImportValueKind string

const (
	ImportText   ImportValueKind = "text"
	ImportNull   ImportValueKind = "null"
	ImportBinary ImportValueKind = "binary"
)

// ImportValue 是导入的一个值，已按源文件的格式转换好（日期、小数点、空值、二进制编码都在前端
// 处理）：驱动只按目标列写成字面量。
type ImportValue struct {
	Kind ImportValueKind `json:"kind"`
	// Text 是 ImportText 的文本。
	Text string `json:"text,omitempty"`
	// Hex 是 ImportBinary 的字节，按十六进制写（不带前缀）。
	Hex string `json:"hex,omitempty"`
}

// BatchRequest 是 Session.ExecuteBatch 要在一条连接上执行的一批语句。
type BatchRequest struct {
	// Statements 按次序执行。
	Statements []string `json:"statements"`
	// Atomic 为真时整批在一个事务里执行：任一条失败即停止并回滚整批。
	Atomic bool `json:"atomic"`
	// ContinueOnError 只在不是 Atomic 时有意义：一条失败后继续执行后面的；为假时停在失败的那条。
	ContinueOnError bool `json:"continueOnError"`
	// DisableForeignKeyChecks 要求执行期间关闭外键检查（见 Session.ExecuteBatch）。
	DisableForeignKeyChecks bool `json:"disableForeignKeyChecks"`
	// Database / Table 是这批语句写入的表，含义同 Session 的其他表操作（table 用 ListTables 给出
	// 的形式）。按表停用外键约束的驱动（SQL Server）作用于这张表，所以关闭外键检查时 Table 必填。
	Database string `json:"database"`
	Table    string `json:"table"`
}

// BatchResult 是一批语句的执行结果。
type BatchResult struct {
	// Executed 是执行过的语句，按执行的次序；跳过的空白语句、没轮到的语句不在其中。
	Executed []BatchStatement `json:"executed"`
	// RolledBack 为真表示 Atomic 的批里有语句失败、整批已经回滚：Executed 里成功的语句也没有留下。
	RolledBack bool `json:"rolledBack"`
}

// BatchStatement 是批里一条执行过的语句。
type BatchStatement struct {
	// Index 是它在 BatchRequest.Statements 里的下标。
	Index int `json:"index"`
	// AffectedRows 是成功时影响的行数。
	AffectedRows int64 `json:"affectedRows"`
	// Error 是失败时数据库给出的错误；成功时为空串。
	Error string `json:"error,omitempty"`
}

// Failed 报告批里是否有语句失败。
func (r *BatchResult) Failed() bool {
	for _, statement := range r.Executed {
		if statement.Error != "" {
			return true
		}
	}
	return false
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

// RowStream 是一次查询的结果游标。读取方式同 database/sql 的 Rows：Next 前进，Row 给出当前行，
// Next 返回 false 后用 Err 区分读完与出错。
type RowStream interface {
	// Columns 是列名，按结果集里的顺序。
	Columns() []string
	// Next 前进到下一行；没有更多行（或出错）时返回 false。
	Next() bool
	// Row 是当前行，按 Columns 的顺序给值，值的要求同 RowSet.Rows。
	Row() []any
	// Err 是读取过程中遇到的错误；正常读完为 nil。
	Err() error
	// Close 释放游标占用的连接。
	io.Closer
}
