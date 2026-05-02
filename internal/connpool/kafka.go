package connpool

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"
)

const (
	defaultKafkaClientTTL = 10 * time.Minute
	defaultKafkaMaxCached = 16
)

type kafkaClientEntry struct {
	assetID     int64
	fingerprint string
	client      *kgo.Client
	lastUsed    time.Time
}

// KafkaClientManager owns bounded franz-go client reuse for UI/Wails service calls.
type KafkaClientManager struct {
	sshPool *sshpool.Pool
	ttl     time.Duration
	max     int
	mu      sync.Mutex
	clients map[string]*kafkaClientEntry
}

func NewKafkaClientManager(sshPool *sshpool.Pool) *KafkaClientManager {
	return &KafkaClientManager{
		sshPool: sshPool,
		ttl:     defaultKafkaClientTTL,
		max:     defaultKafkaMaxCached,
		clients: make(map[string]*kafkaClientEntry),
	}
}

func (m *KafkaClientManager) Get(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.KafkaConfig, password string) (*kgo.Client, error) {
	if asset == nil || cfg == nil {
		return nil, fmt.Errorf("kafka 资产配置为空")
	}
	fingerprint := KafkaConfigFingerprint(asset, cfg)
	key := fmt.Sprintf("%d:%s", asset.ID, fingerprint)
	now := time.Now()

	m.mu.Lock()
	m.closeStaleLocked(asset.ID, fingerprint, now)
	if entry := m.clients[key]; entry != nil {
		entry.lastUsed = now
		client := entry.client
		m.mu.Unlock()
		return client, nil
	}
	m.mu.Unlock()

	client, err := DialKafka(ctx, asset, cfg, password, m.sshPool)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if entry := m.clients[key]; entry != nil {
		entry.lastUsed = now
		client.Close()
		return entry.client, nil
	}
	m.clients[key] = &kafkaClientEntry{assetID: asset.ID, fingerprint: fingerprint, client: client, lastUsed: now}
	m.evictOverflowLocked()
	return client, nil
}

func (m *KafkaClientManager) CloseAsset(assetID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, entry := range m.clients {
		if entry.assetID == assetID {
			entry.client.Close()
			delete(m.clients, key)
		}
	}
}

func (m *KafkaClientManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, entry := range m.clients {
		entry.client.Close()
		delete(m.clients, key)
	}
}

func (m *KafkaClientManager) closeStaleLocked(assetID int64, fingerprint string, now time.Time) {
	for key, entry := range m.clients {
		if now.Sub(entry.lastUsed) > m.ttl || (entry.assetID == assetID && entry.fingerprint != fingerprint) {
			entry.client.Close()
			delete(m.clients, key)
		}
	}
}

func (m *KafkaClientManager) evictOverflowLocked() {
	for len(m.clients) > m.max {
		var oldestKey string
		var oldest time.Time
		for key, entry := range m.clients {
			if oldestKey == "" || entry.lastUsed.Before(oldest) {
				oldestKey = key
				oldest = entry.lastUsed
			}
		}
		if oldestKey == "" {
			return
		}
		m.clients[oldestKey].client.Close()
		delete(m.clients, oldestKey)
	}
}

// DialKafka creates a franz-go client and verifies it with Ping.
func DialKafka(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.KafkaConfig, password string, sshPool *sshpool.Pool) (*kgo.Client, error) {
	opts, err := BuildKafkaOptions(asset, cfg, password, sshPool)
	if err != nil {
		return nil, err
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("创建 Kafka 客户端失败: %w", err)
	}
	if err := client.Ping(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("kafka 连接失败: %w", err)
	}
	return client, nil
}

func BuildKafkaOptions(asset *asset_entity.Asset, cfg *asset_entity.KafkaConfig, password string, sshPool *sshpool.Pool) ([]kgo.Opt, error) {
	brokers := NormalizeKafkaBrokers(cfg.Brokers)
	if len(brokers) == 0 {
		return nil, fmt.Errorf("kafka broker不能为空")
	}
	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ClientID(resolveKafkaClientID(cfg)),
		kgo.DisableClientMetrics(),
	}
	if cfg.RequestTimeoutSeconds > 0 {
		timeout := time.Duration(cfg.RequestTimeoutSeconds) * time.Second
		opts = append(opts, kgo.DialTimeout(timeout), kgo.RetryTimeout(timeout), kgo.RequestTimeoutOverhead(timeout))
	}
	tunnelID := int64(0)
	if asset != nil {
		tunnelID = asset.SSHTunnelID
	}
	if tunnelID == 0 {
		tunnelID = cfg.SSHAssetID
	}
	var tlsConfig *tls.Config
	if cfg.TLS {
		var err error
		tlsConfig, err = buildKafkaTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
	}
	if cfg.TLS && tunnelID == 0 {
		opts = append(opts, kgo.DialTLSConfig(tlsConfig))
	}
	if tunnelID > 0 && sshPool != nil {
		opts = append(opts, kgo.Dialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := splitKafkaAddr(addr)
			if err != nil {
				return nil, err
			}
			conn, err := NewSSHTunnel(tunnelID, host, port, sshPool).Dial(ctx)
			if err != nil {
				return nil, err
			}
			if tlsConfig == nil {
				return conn, nil
			}
			cfgClone := tlsConfig.Clone()
			if cfgClone.ServerName == "" {
				cfgClone.ServerName = host
			}
			tlsConn := tls.Client(conn, cfgClone)
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return tlsConn, nil
		}))
	}
	if cfg.TLS && tunnelID > 0 && sshPool == nil {
		opts = append(opts, kgo.DialTLSConfig(tlsConfig))
	}
	mech := normalizeKafkaSASLMechanism(cfg.SASLMechanism)
	switch mech {
	case asset_entity.KafkaSASLNone:
	case asset_entity.KafkaSASLPlain:
		opts = append(opts, kgo.SASL(plain.Auth{User: cfg.Username, Pass: password}.AsMechanism()))
	case asset_entity.KafkaSASLSCRAMSHA256:
		opts = append(opts, kgo.SASL(scram.Auth{User: cfg.Username, Pass: password}.AsSha256Mechanism()))
	case asset_entity.KafkaSASLSCRAMSHA512:
		opts = append(opts, kgo.SASL(scram.Auth{User: cfg.Username, Pass: password}.AsSha512Mechanism()))
	default:
		return nil, fmt.Errorf("不支持的 Kafka SASL 机制: %s", cfg.SASLMechanism)
	}
	return opts, nil
}

func NormalizeKafkaBrokers(brokers []string) []string {
	out := make([]string, 0, len(brokers))
	seen := make(map[string]bool, len(brokers))
	for _, broker := range brokers {
		broker = strings.TrimSpace(broker)
		if broker == "" || seen[broker] {
			continue
		}
		seen[broker] = true
		out = append(out, broker)
	}
	return out
}

func KafkaConfigFingerprint(asset *asset_entity.Asset, cfg *asset_entity.KafkaConfig) string {
	brokers := NormalizeKafkaBrokers(cfg.Brokers)
	sort.Strings(brokers)
	tunnelID := int64(0)
	if asset != nil {
		tunnelID = asset.SSHTunnelID
	}
	if tunnelID == 0 {
		tunnelID = cfg.SSHAssetID
	}
	passwordRef := ""
	if cfg.CredentialID > 0 {
		passwordRef = fmt.Sprintf("cred:%d", cfg.CredentialID)
	} else if cfg.Password != "" {
		passwordRef = "inline:" + hashString(cfg.Password)
	}
	parts := []string{
		strings.Join(brokers, ","),
		normalizeKafkaSASLMechanism(cfg.SASLMechanism),
		cfg.Username,
		strconv.FormatBool(cfg.TLS),
		strconv.FormatBool(cfg.TLSInsecure),
		cfg.TLSServerName,
		cfg.TLSCAFile,
		cfg.TLSCertFile,
		cfg.TLSKeyFile,
		strconv.FormatInt(tunnelID, 10),
		passwordRef,
	}
	return hashString(strings.Join(parts, "\x00"))
}

func resolveKafkaClientID(cfg *asset_entity.KafkaConfig) string {
	if strings.TrimSpace(cfg.ClientID) != "" {
		return strings.TrimSpace(cfg.ClientID)
	}
	return "opskat"
}

func normalizeKafkaSASLMechanism(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return asset_entity.KafkaSASLNone
	}
	return v
}

func splitKafkaAddr(addr string) (string, int, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("kafka broker地址无效: %s", addr)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("kafka broker端口无效: %s", addr)
	}
	return host, port, nil
}

func buildKafkaTLSConfig(cfg *asset_entity.KafkaConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         cfg.TLSServerName,
		InsecureSkipVerify: cfg.TLSInsecure,
	}
	if cfg.TLSCAFile != "" {
		ca, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("读取 Kafka TLS CA 证书失败: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("解析 Kafka TLS CA 证书失败")
		}
		tlsConfig.RootCAs = pool
	}
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" {
		if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
			return nil, fmt.Errorf("kafka TLS 客户端证书和私钥必须同时配置")
		}
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("加载 Kafka TLS 客户端证书失败: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return tlsConfig, nil
}

func hashString(value string) string {
	h := fnv.New64a()
	if _, err := h.Write([]byte(value)); err != nil {
		logger.Default().Warn("hash kafka config", zap.Error(err))
	}
	return strconv.FormatUint(h.Sum64(), 16)
}
