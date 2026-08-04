package helper

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// Direction 是端点在一次传输里被使用的方向。展开（DirList）自己也是一个方向：枚举读的是
// 元数据而不是内容，但 `cp -r web-01:/ ./x` 能把整棵文件树的结构拖出来，所以它同样要授权。
type Direction int

const (
	DirRead Direction = iota
	DirWrite
	DirList
	DirReadScope
	DirWriteScope
)

// Entry 是一次展开产出的单个可传输条目。只有文件/对象，没有目录——目录由 RelPath 隐含，
// 目的端在写入时按需创建。
type Entry struct {
	// Path 是该端点上的完整路径 / 对象 key，直接交给 OpenRead / Write。
	Path string
	// RelPath 相对展开基点，决定它在目的端的落点：目的路径 = <dst 基点> + RelPath。
	// 基点按形态取：单个文件是它所在目录，glob 是通配前的最后一层目录，递归是源目录自身。
	// 始终以 "/" 分隔，跨端点拼接才不会带上本地平台的分隔符。
	RelPath string
	Size    int64
}

// ListResult 是一次展开的全部产出。跳过的符号链接与 Entries 分开返回，因为它们不是可传输
// 条目，却必须原样报给用户——静默跳过就成了一次看起来完整、实际残缺的传输。
type ListResult struct {
	Entries         []Entry
	SkippedSymlinks []string
}

// MaxTransferEntries 是执行资源边界，不是审批弹窗上限。目录范围只审批一次，但单次任务不能
// 无界物化整棵文件树；达到该值时在开始传输前明确失败。
const MaxTransferEntries = 100_000

func (r *ListResult) appendEntry(entry Entry) error {
	if len(r.Entries) >= MaxTransferEntries {
		return fmt.Errorf("transfer expands beyond the execution limit of %d entries", MaxTransferEntries)
	}
	r.Entries = append(r.Entries, entry)
	return nil
}

// TransferAdapter 是一类传输端点的全部能力：怎么展开、怎么读写、以及在每个方向上要授权
// 什么。四件事放在同一个接口里，是为了不出现第二张"类型 → 审批语义"的表。
type TransferAdapter interface {
	// List 展开 pattern。pattern 含 glob 元字符（* ? [）时按 glob 展开；recursive 为真时
	// 下钻目录树；单个具体文件/对象返回单元素切片。展开途中遇到的符号链接一律跳过并计入
	// SkippedSymlinks——跟随会让 `cp -r ./dir` 因为一条指向 / 的链接变成整机 dump。
	// 指名到一个目录却没有 recursive 时报错，不猜。
	List(ctx context.Context, asset *asset_entity.Asset, pattern string, recursive bool) (*ListResult, error)
	// OpenRead 打开该端点上的 path 用于读取；size 未知时返回 -1。返回的 ReadCloser 活过
	// 本次调用，调用方负责 Close。
	OpenRead(ctx context.Context, asset *asset_entity.Asset, path string) (io.ReadCloser, int64, error)
	// Write 把 r 的内容写入该端点的 path，必要时创建中间目录；size 未知时传 -1。
	Write(ctx context.Context, asset *asset_entity.Asset, path string, r io.Reader, size int64) error
	// ValidateDestination 判断 path 能不能当这一端点的写入目标，只看形态、不碰网络。
	//
	// 它存在是因为目的端与源端不对称：源端的形态错误由 List 在展开时报出来，而目的端
	// **不经过 List**（要写的东西还不存在），ApprovalSubject 又不能报错。没有这一关，
	// 一个形态错误的目的地会带着一个错误的主体走完审批、到 Write 才失败——用户批准了
	// 一件必然失败的事。与 handleExec 的排序不变式同源：无副作用的判断必须全部走完，
	// 才能碰有副作用的那一步（审批弹窗）。
	//
	// 入参是**具体的**目的路径，即多源展开时 dst 基点拼上 RelPath 之后的结果——正是
	// 会被审批、也会被写入的那个字符串，因此这里不需要知道是单源还是多源。
	ValidateDestination(path string) error
	// ApprovalSubject 返回这一端点在该方向上必须被授权的审批类型与匹配串。
	//
	// DirList 方向收到的是用户**指名的那个串**——可能是一个 pattern，也可能是一个前缀，
	// 与 List 收到的完全相同。收窄成"实际会被枚举的那个基点"是适配器自己的事，调用方
	// 不得先替它截一刀：哪些字符是通配语法只有适配器知道（对象存储的前缀形态 key 里
	// `*?[` 是字面量，规则侧走的是 strings.HasPrefix），入口层按 glob 截断会让指名一个
	// 前缀的递归传输换来整桶列举的授权。反过来，主体也绝不能是 pattern 原文：cp 的 grant
	// 不分方向，一条 "/var/log/*.log" 的 grant 会连它命中的每个文件的读写一起授权。
	//
	// **一个端点有审批主体，当且仅当它有资产。** 本地端点没有资产，因此正确的调用方按
	// asset 是否为 nil 来决定要不要走权限检查，压根不会问本地适配器要主体；它返回的空串是
	// "不适用"，不是一个让调用方去判空的哨兵——按哨兵写，漏判时就会静默放行。
	ApprovalSubject(path string, dir Direction) (approvalType, subject string)
}

// TransferPathNormalizer 是文件系统类适配器的可选能力。对象存储 key 是不透明名字，不能
// 用文件系统规则清理；本地与 SSH 则必须在审批、审计和 I/O 分叉之前归一成同一个路径。
type TransferPathNormalizer interface {
	NormalizeTransferPath(path string) string
}

// ApprovalTarget 是一个端点操作必须同时满足的单条授权。通常只有一条；SSH 的 `-r path`
// 在审批前不能探测 path 是文件还是目录，因此同时要求精确路径与子树范围。
type ApprovalTarget struct {
	ApprovalType string
	Subject      string
}

// TransferApprovalPlanner 是适配器的可选多主体授权能力。
type TransferApprovalPlanner interface {
	ApprovalSubjects(path string, dir Direction) []ApprovalTarget
}

// TransferCapabilities 让入口询问适配器能力，不按资产类型或协议分支。
type TransferCapabilities interface {
	SupportsPooledProxyCopy() bool
	SameAssetCopyHint(assetName string) string
}

func SupportsPooledProxyCopy(adapter TransferAdapter) bool {
	c, ok := adapter.(TransferCapabilities)
	return ok && c.SupportsPooledProxyCopy()
}

func SameAssetCopyHint(adapter TransferAdapter, assetName string) string {
	c, ok := adapter.(TransferCapabilities)
	if !ok {
		return ""
	}
	return c.SameAssetCopyHint(assetName)
}

// ApprovalSubjectsFor 返回该端点操作全部必须授权的主体。
func ApprovalSubjectsFor(adapter TransferAdapter, p string, dir Direction) []ApprovalTarget {
	if planner, ok := adapter.(TransferApprovalPlanner); ok {
		return planner.ApprovalSubjects(p, dir)
	}
	typ, subject := adapter.ApprovalSubject(p, dir)
	return []ApprovalTarget{{ApprovalType: typ, Subject: subject}}
}

// NormalizeTransferPath 通过适配器能力规范化端点路径；没有该能力的适配器保持原值。
func NormalizeTransferPath(adapter TransferAdapter, p string) string {
	if normalizer, ok := adapter.(TransferPathNormalizer); ok {
		return normalizer.NormalizeTransferPath(p)
	}
	return p
}

type transferWriteScopeKey struct{}

// WithTransferWriteScope fixes the filesystem root that an adapter write may not escape.
func WithTransferWriteScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, transferWriteScopeKey{}, scope)
}

func transferWriteScope(ctx context.Context) string {
	scope, _ := ctx.Value(transferWriteScopeKey{}).(string)
	return scope
}

var transferAdapters = make(map[string]TransferAdapter)

// RegisterTransferAdapter 注册某资产类型的传输适配器，由持有该协议的实现在 init() 中调用。
// 重复注册 panic——与 permission.RegisterExecutor 一致，注册冲突是启动期的编程错误，
// 不该静默覆盖。
func RegisterTransferAdapter(assetType string, a TransferAdapter) {
	if assetType == "" || a == nil {
		panic("helper: invalid transfer adapter registration")
	}
	if _, exists := transferAdapters[assetType]; exists {
		panic(fmt.Sprintf("helper: duplicate transfer adapter registration %q", assetType))
	}
	transferAdapters[assetType] = a
}

// TransferAdapterFor 返回该端点的适配器，与 ParseTransferEndpoint 的返回值配套：
// asset 为 nil 就是本地端点——它没有资产，因此不进注册表，由包级实现直接交出。
// 没有接进传输面的资产类型返回错误，而不是回落成本地。
func TransferAdapterFor(asset *asset_entity.Asset) (TransferAdapter, error) {
	if asset == nil {
		return localTransfer, nil
	}
	if a, ok := transferAdapters[asset.Type]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("asset type %q does not support file transfer", asset.Type)
}

// hasGlobMeta 判断路径里是否有 glob 元字符，即这一端是不是要按通配展开。
// 元字符取 path.Match 的三个：* ? [。
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// HasGlobPattern 是 hasGlobMeta 的导出面，供入口层按 spec §6.5 判"多源形态"。
// 导出而不是让调用方自己写一遍 strings.ContainsAny：判形态与展开必须用同一份元字符
// 定义，否则会出现"按单源审批、按多源展开"这种两边对不上的传输。
func HasGlobPattern(s string) bool {
	return hasGlobMeta(s)
}

// globBase 返回一个 glob 的展开基点：通配之前的最后一层目录（spec §6.5）。
// 因此 "/var/log/*.log" 的基点是 "/var/log"，"/var/*/x.log" 的基点是 "/var"。
// 入参以 "/" 分隔——本地端由调用方先 filepath.ToSlash。
func globBase(pattern string) string {
	segments := strings.Split(pattern, "/")
	for i, seg := range segments {
		if hasGlobMeta(seg) {
			return strings.Join(segments[:i], "/")
		}
	}
	return path.Dir(pattern)
}

// relTo 返回 full 相对 base 的路径。base 永远是 full 的整段前缀（它要么由 globBase 按
// 整段截出，要么就是被递归的那个目录本身），因此这里只需把前缀和分隔符去掉。
func relTo(base, full string) string {
	if base == "" || base == "/" {
		return strings.TrimPrefix(full, "/")
	}
	return strings.TrimPrefix(strings.TrimPrefix(full, base), "/")
}
