package permission

import (
	"context"
	"fmt"
	"sort"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

type permissionCheckFunc func(context.Context, int64, string) aictx.CheckResult

// permissionTypeHandler is the single source of truth for permission dispatch,
// opsctl aliases, approval item types, and grant normalization behavior.
type permissionTypeHandler struct {
	canonical    string
	approvalType string
	shellLike    bool
	check        permissionCheckFunc
}

var permissionTypes = make(map[string]*permissionTypeHandler)

func registerPermissionType(canonical, approvalType string, shellLike bool, check permissionCheckFunc, aliases ...string) {
	if canonical == "" || approvalType == "" || check == nil {
		panic("permission: invalid type registration")
	}
	handler := &permissionTypeHandler{
		canonical:    canonical,
		approvalType: approvalType,
		shellLike:    shellLike,
		check:        check,
	}
	for _, name := range append([]string{canonical}, aliases...) {
		if _, exists := permissionTypes[name]; exists {
			panic(fmt.Sprintf("permission: duplicate type registration %q", name))
		}
		permissionTypes[name] = handler
	}
}

func permissionTypeFor(name string) (*permissionTypeHandler, bool) {
	handler, ok := permissionTypes[name]
	return handler, ok
}

func init() {
	registerPermissionType(asset_entity.AssetTypeSSH, "exec", true, checkCommandPolicyPermission, "exec")
	registerPermissionType(asset_entity.AssetTypeSerial, "serial", false, checkCommandPolicyPermission)
	registerPermissionType(asset_entity.AssetTypeDatabase, "sql", false, checkDatabasePermission, "sql")
	registerPermissionType(asset_entity.AssetTypeRedis, "redis", false, checkRedisPermission)
	registerPermissionType(asset_entity.AssetTypeEtcd, "etcd", false, checkEtcdPermission)
	registerPermissionType(asset_entity.AssetTypeMongoDB, "mongo", false, checkMongoDBPermission, "mongo")
	registerPermissionType(asset_entity.AssetTypeKafka, "kafka", false, checkKafkaPermission)
	registerPermissionType(asset_entity.AssetTypeK8s, "k8s", true, checkK8sPermission)
}

// --- 执行器注册表 ---
//
// 注册方向是自下而上推送：helper 等持有协议代码的包导入本包，
// 因此本包只声明类型与注册入口，实现由它们在 init() 中调 RegisterExecutor 推上来。

// ExecFunc 按资产真实类型执行一条命令。scope 是"不属于命令本身的连接级目标"
// （database 用库名、redis 用 db 序号），其余类型忽略。
type ExecFunc func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error)

// CanonicalizeFunc 把模型给的原始命令规范化为"真正会被执行、也应被策略匹配"的形式。
// 仅当某类型执行前会改写命令时才需要注册（目前只有 k8s 注入 --context/--namespace）。
type CanonicalizeFunc func(asset *asset_entity.Asset, command string) (string, error)

type execEntry struct {
	exec         ExecFunc
	help         string
	canonicalize CanonicalizeFunc
}

var execEntries = make(map[string]*execEntry)

// RegisterExecutor 注册某资产类型的执行器与用法文档。canonicalize 是可选的第四个参数——
// 只有执行前会改写命令的类型（目前只有 k8s）才需要传；不传的类型按原样校验与执行。
// 重复注册 panic——与 registerPermissionType 一致，注册冲突是启动期的编程错误，不该静默覆盖。
func RegisterExecutor(canonical string, exec ExecFunc, help string, canonicalize ...CanonicalizeFunc) {
	if canonical == "" || exec == nil {
		panic("permission: invalid executor registration")
	}
	if _, exists := execEntries[canonical]; exists {
		panic(fmt.Sprintf("permission: duplicate executor registration %q", canonical))
	}
	var canon CanonicalizeFunc
	if len(canonicalize) > 0 {
		canon = canonicalize[0]
	}
	execEntries[canonical] = &execEntry{exec: exec, help: help, canonicalize: canon}
}

// ExecutorFor 返回该资产类型的执行器。
func ExecutorFor(assetType string) (ExecFunc, bool) {
	entry, ok := execEntries[assetType]
	if !ok {
		return nil, false
	}
	return entry.exec, true
}

// HelpFor 返回该资产类型的用法文档。
func HelpFor(assetType string) (string, bool) {
	entry, ok := execEntries[assetType]
	if !ok {
		return "", false
	}
	return entry.help, true
}

// CanonicalizeFor 返回该资产类型注册的命令规范化钩子（如有）。没有注册规范化钩子
// 的类型（多数类型）返回 (nil, false)——调用方应按原样校验与执行。
func CanonicalizeFor(assetType string) (CanonicalizeFunc, bool) {
	entry, ok := execEntries[assetType]
	if !ok || entry.canonicalize == nil {
		return nil, false
	}
	return entry.canonicalize, true
}

// RegisteredExecTypes 返回已注册执行器的资产类型，已排序。
func RegisteredExecTypes() []string {
	types := make([]string, 0, len(execEntries))
	for name := range execEntries {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}
