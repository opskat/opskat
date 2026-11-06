package bootstrap

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/opskat/opskat/internal/pkg/portable"
	"github.com/opskat/opskat/internal/repository/ai_provider_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/repository/command_review_repo"
	"github.com/opskat/opskat/internal/repository/conversation_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/repository/extension_data_repo"
	"github.com/opskat/opskat/internal/repository/extension_describe_repo"
	"github.com/opskat/opskat/internal/repository/extension_settings_repo"
	"github.com/opskat/opskat/internal/repository/extension_state_repo"
	"github.com/opskat/opskat/internal/repository/forward_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/host_key_repo"
	"github.com/opskat/opskat/internal/repository/policy_group_repo"
	"github.com/opskat/opskat/internal/repository/snippet_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/snippet_svc"
	"github.com/opskat/opskat/migrations"
	"github.com/opskat/opskat/pkg/extension"

	"github.com/cago-frame/cago"
	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/configs/memory"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	_ "github.com/opskat/opskat/internal/pkg/code"

	_ "github.com/cago-frame/cago/database/db/sqlite"
)

// Options 初始化选项
type Options struct {
	DataDir   string // 空则用默认平台目录
	MasterKey string // 空则从 Keychain/文件自动获取或生成
}

// resolvedDataDir 记录 Init 实际使用的数据目录（可被 Options.DataDir / OPSKAT_DATA_DIR 覆盖），
// 供 GetLogsDir 等复用，避免日志写到默认目录而数据库在覆盖目录。
var resolvedDataDir string

// ResolvedDataDir 返回 Init 实际使用的数据目录；Init 之前回退到默认平台目录。
func ResolvedDataDir() string {
	if resolvedDataDir != "" {
		return resolvedDataDir
	}
	return AppDataDir()
}

// portableDir 解析便携数据目录，便携模式外返回 ""。变量而非直接调用，
// 是为了让 AppDataDir 的两个分支可测（os.Executable() 在测试中指向
// 临时测试二进制，无法构造 data 目录）。
var portableDir = portable.Dir

// AppDataDir 返回应用数据目录。
// 便携模式（可执行文件同级存在 data 目录）优先，否则用平台默认目录。
func AppDataDir() string {
	if dir := portableDir(); dir != "" {
		return dir
	}
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "opskat")
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			home, _ := os.UserHomeDir()
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(localAppData, "opskat")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "opskat")
	}
}

// masterKeyOpts 由实际使用的数据目录（dataDir，可能被 --data-dir /
// OPSKAT_DATA_DIR 覆盖）决定 master key 的解析方式。
func masterKeyOpts(opts Options, dataDir string) credential_svc.MasterKeyOptions {
	return credential_svc.MasterKeyOptions{
		Explicit: opts.MasterKey,
		DataDir:  dataDir,
		// 只有真正在用便携目录时才跳过凭据管理器。若按"可执行文件是否便携"
		// 来判断，便携的 opsctl 指向已安装数据目录时会跳过凭据管理器里的
		// 真 key，转而生成新 key 写进已安装目录，库内凭据从此解不开。
		NoKeychain: portable.SameDir(dataDir, portableDir()),
	}
}

// SQLiteDSN 返回主库 opskat.db 的连接串。桌面端与 opsctl 是两个进程，会同时写这个文件：
// 事务一开始就取写锁（_txlock=immediate），"检查再写入"因此在跨进程之间串行；
// 拿不到锁时最多等 busy_timeout，而不是立刻报 database is locked。
func SQLiteDSN(path string) string {
	return path + "?_txlock=immediate&_pragma=busy_timeout(10000)"
}

// Init 初始化数据库、凭证服务、注册 Repository、运行迁移
func Init(ctx context.Context, opts Options) error {
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = AppDataDir()
	}
	resolvedDataDir = dataDir

	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0755); err != nil {
		return err
	}

	cfg, err := configs.NewConfig("opskat", configs.WithSource(memory.NewSource(map[string]interface{}{
		"db": map[string]interface{}{
			"driver": "sqlite",
			"dsn":    SQLiteDSN(filepath.Join(dataDir, "opskat.db")),
		},
	})))
	if err != nil {
		return err
	}

	cago.New(ctx, cfg).
		Registry(db.Database())

	// 获取或生成 KDF salt
	salt, err := resolveKDFSalt(dataDir)
	if err != nil {
		return fmt.Errorf("获取 KDF salt 失败: %w", err)
	}

	// master key（CLI 参数 > Keychain > 文件 > 自动生成）与 Argon2id 派生推迟到首次加解密，
	// 不解密的进程（如 opsctl list）不访问 keychain、不跑 KDF
	mkOpts := masterKeyOpts(opts, dataDir)
	credential_svc.SetDefault(credential_svc.NewLazy(func() (string, error) {
		return credential_svc.ResolveMasterKey(mkOpts)
	}, salt))

	registerRepositories()

	if err := migrations.RunMigrations(db.Default(), dataDir); err != nil {
		return err
	}
	if err := refreshPlannerStats(db.Default()); err != nil {
		logger.Ctx(ctx).Warn("refresh sqlite planner statistics", zap.Error(err))
	}

	snippet_svc.Register(snippet_svc.NewSnippetSvc(snippet_svc.NewCategoryRegistry()))
	registerCommandReview()

	return nil
}

// auditStatsGrowth 是 audit_logs 行数涨到上次统计时的多少倍就重新统计。
const auditStatsGrowth = 10

// auditAnalysisLimit 限制重新统计时每个索引扫描的行数，让大表上的 ANALYZE 也只花毫秒级时间。
const auditAnalysisLimit = 1000

// refreshPlannerStats 让审计表的规划器统计跟上数据增长。迁移里的 ANALYZE 只覆盖升级那一刻
// 已有的数据：新装或升级时表还小的库长大后没有可用统计，审计页的时间窗查询会退回整索引扫描。
//
// 每次启动（含每次 opsctl 调用）都会走到这里，所以判断是否需要统计必须只读：PRAGMA optimize
// 每次都要取写锁（空表 ANALYZE 后仍无统计，永远被当成"缺统计"），另一进程写库时启动就会卡在
// busy_timeout 上。审计日志只增不删，id 跨度即行数，主键两端各查一次即可得出。
// 失败不影响查询结果，由调用方记日志、沿用旧统计。
func refreshPlannerStats(gdb *gorm.DB) error {
	var st struct {
		Analyzed sql.NullInt64
		Rows     int64
	}
	if err := gdb.Raw(`SELECT
	(SELECT MAX(CAST(stat AS INTEGER)) FROM sqlite_stat1 WHERE tbl = 'audit_logs') AS analyzed,
	COALESCE((SELECT MAX(id) FROM audit_logs) - (SELECT MIN(id) FROM audit_logs) + 1, 0) AS rows`).Scan(&st).Error; err != nil {
		return err
	}
	if st.Rows == 0 || (st.Analyzed.Valid && st.Rows < auditStatsGrowth*st.Analyzed.Int64) {
		return nil
	}
	// analysis_limit 是连接级设置，固定在同一条连接上设置并执行。
	return gdb.Connection(func(conn *gorm.DB) error {
		if err := conn.Exec(fmt.Sprintf("PRAGMA analysis_limit = %d", auditAnalysisLimit)).Error; err != nil {
			return err
		}
		return conn.Exec("ANALYZE audit_logs").Error
	})
}

// registerRepositories 注册所有 Repository 单例
func registerRepositories() {
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	audit_repo.RegisterAudit(audit_repo.NewAudit())
	conversation_repo.RegisterConversation(conversation_repo.NewConversation())
	group_repo.RegisterGroup(group_repo.NewGroup())
	grant_repo.RegisterGrant(grant_repo.NewGrant())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	host_key_repo.RegisterHostKey(host_key_repo.NewHostKey())
	forward_repo.RegisterForward(forward_repo.NewForward())
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	policy_group_repo.RegisterPolicyGroup(policy_group_repo.NewPolicyGroup())
	ai_provider_repo.RegisterAIProvider(ai_provider_repo.NewAIProvider())
	extension_data_repo.RegisterExtensionData(extension_data_repo.NewExtensionData())
	extension_state_repo.RegisterExtensionState(extension_state_repo.NewExtensionState())
	extension_settings_repo.RegisterExtensionSettings(extension_settings_repo.NewExtensionSettings())
	extension_describe_repo.RegisterExtensionDescribe(extension_describe_repo.NewExtensionDescribe())
	// Extensions are described by running them; the cache is what keeps listing them
	// (and resolving their asset types from opsctl) off that path.
	extension.SetDescribeCache(extensionDescribeCache{})
	snippet_repo.RegisterSnippet(snippet_repo.NewSnippet())
	custom_type_repo.RegisterCustomType(custom_type_repo.New())
	command_review_repo.RegisterCommandReview(command_review_repo.NewCommandReview())
}

// resolveKDFSalt 从 config.json 获取 salt，不存在则生成并持久化
func resolveKDFSalt(dataDir string) ([]byte, error) {
	appCfg, err := LoadConfig(dataDir)
	if err != nil {
		return nil, err
	}

	if appCfg.KDFSalt != "" {
		salt, err := base64.StdEncoding.DecodeString(appCfg.KDFSalt)
		if err != nil {
			return nil, fmt.Errorf("解码 KDF salt 失败: %w", err)
		}
		return salt, nil
	}

	// 首次启动，生成 salt
	salt, err := credential_svc.GenerateSalt()
	if err != nil {
		return nil, err
	}

	appCfg.KDFSalt = base64.StdEncoding.EncodeToString(salt)
	if err := SaveConfig(appCfg); err != nil {
		return nil, fmt.Errorf("保存 KDF salt 失败: %w", err)
	}

	return salt, nil
}
