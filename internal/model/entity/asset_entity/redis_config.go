package asset_entity

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Redis 部署模式
const (
	RedisModeStandalone = "standalone"
	RedisModeCluster    = "cluster"
	RedisModeSentinel   = "sentinel"
)

// EffectiveMode 返回生效的部署模式,未设置时为单机。
func (c *RedisConfig) EffectiveMode() string {
	if c.Mode == "" {
		return RedisModeStandalone
	}
	return c.Mode
}

// KeepModeFieldsOnly 清掉当前部署模式用不到的字段(与桌面表单保存一致:只写入当前模式的字段),
// 避免切换模式后残留的其它模式字段被安全视图展示或参与连接。单机模式不写 mode。
func (c *RedisConfig) KeepModeFieldsOnly() {
	switch c.EffectiveMode() {
	case RedisModeStandalone:
		c.Mode = ""
		c.Nodes = nil
		c.NodeAddressMap = nil
		c.clearSentinelFields()
	case RedisModeCluster:
		c.Host, c.Port = "", 0 // database 保留:非 0 时由 ValidateMode 明确报错,不静默改成 db0
		c.clearSentinelFields()
	case RedisModeSentinel:
		c.Host, c.Port = "", 0
	}
}

func (c *RedisConfig) clearSentinelFields() {
	c.MasterName = ""
	c.SentinelUsername = ""
	c.SentinelPassword = ""
}

// ValidateMode 按部署模式校验必填项,错误信息指出具体字段与行号。导出给 opsctl create /
// AI put_asset 的审批前校验复用(asset.validateRedis 在 commit 时也调它),两处共享同一份
// 规则,不重复一份校验逻辑。
func (c *RedisConfig) ValidateMode() error {
	switch c.EffectiveMode() {
	case RedisModeStandalone:
		if c.Host == "" {
			return errors.New("Redis主机地址不能为空")
		}
		if c.Port <= 0 {
			return errors.New("Redis端口无效")
		}
		return nil
	case RedisModeCluster:
		if err := validateRedisNodes(c.Nodes, "集群种子节点"); err != nil {
			return err
		}
		if c.Database != 0 {
			return errors.New("集群模式只有 db0,database 必须为 0")
		}
	case RedisModeSentinel:
		if err := validateRedisNodes(c.Nodes, "哨兵节点"); err != nil {
			return err
		}
		if strings.TrimSpace(c.MasterName) == "" {
			return errors.New("哨兵模式的主节点名称(master_name)不能为空")
		}
	default:
		return fmt.Errorf("不支持的 Redis 部署模式(mode): %s", c.Mode)
	}
	return validateRedisNodeAddressMap(c.NodeAddressMap)
}

func validateRedisNodes(nodes []string, label string) error {
	if len(nodes) == 0 {
		return fmt.Errorf("%s(nodes)至少填写一个 host:port", label)
	}
	for i, node := range nodes {
		if err := validateRedisHostPort(node); err != nil {
			return fmt.Errorf("%s(nodes)第 %d 行 %q 无效: %w", label, i+1, node, err)
		}
	}
	return nil
}

func validateRedisNodeAddressMap(m map[string]string) error {
	announced := make([]string, 0, len(m))
	for k := range m {
		announced = append(announced, k)
	}
	sort.Strings(announced) // 多条错误时报告稳定
	for _, from := range announced {
		if err := validateRedisHostPort(from); err != nil {
			return fmt.Errorf("节点地址映射(node_address_map)宣告地址 %q 无效: %w", from, err)
		}
		if err := validateRedisHostPort(m[from]); err != nil {
			return fmt.Errorf("节点地址映射(node_address_map)%q 的实际地址 %q 无效: %w", from, m[from], err)
		}
	}
	return nil
}

func validateRedisHostPort(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("格式应为 host:port")
	}
	if host == "" {
		return errors.New("主机不能为空")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return errors.New("端口无效")
	}
	return nil
}
