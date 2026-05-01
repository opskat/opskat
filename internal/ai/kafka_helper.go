package ai

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
)

// --- Kafka connection cache ---

type kafkaCacheKeyType struct{}

type kafkaClientCloser struct {
	client *kgo.Client
}

func (c *kafkaClientCloser) Close() error {
	if c != nil && c.client != nil {
		c.client.Close()
	}
	return nil
}

type KafkaClientCache = ConnCache[*kafkaClientCloser]

func NewKafkaClientCache() *KafkaClientCache {
	return NewConnCache[*kafkaClientCloser]("Kafka")
}

func WithKafkaCache(ctx context.Context, cache *KafkaClientCache) context.Context {
	return context.WithValue(ctx, kafkaCacheKeyType{}, cache)
}

func getKafkaCache(ctx context.Context) *KafkaClientCache {
	if cache, ok := ctx.Value(kafkaCacheKeyType{}).(*KafkaClientCache); ok {
		return cache
	}
	return nil
}

// --- Handlers ---

func handleKafkaCluster(ctx context.Context, args map[string]any) (string, error) {
	assetID := argInt64(args, "asset_id")
	operation := normalizeKafkaOperation(argString(args, "operation"), "overview")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	command, err := kafkaClusterCommand(operation)
	if err != nil {
		return "", err
	}
	if result, ok := checkKafkaToolPermission(ctx, assetID, command); !ok {
		return result.Message, nil
	}

	client, closeFn, err := openKafkaClient(ctx, assetID)
	if err != nil {
		return "", err
	}
	defer closeFn()

	admin := kadm.NewClient(client)
	switch operation {
	case "overview":
		metadata, err := admin.Metadata(ctx)
		if err != nil {
			return "", fmt.Errorf("read Kafka cluster overview: %w", err)
		}
		return marshalResult(kafkaOverviewResult(assetID, metadata))
	case "brokers", "list_brokers":
		brokers, err := admin.ListBrokers(ctx)
		if err != nil {
			return "", fmt.Errorf("list Kafka brokers: %w", err)
		}
		return marshalResult(map[string]any{"brokers": kafkaBrokersResult(brokers), "count": len(brokers)})
	default:
		return "", fmt.Errorf("unsupported kafka_cluster operation: %s", operation)
	}
}

func handleKafkaTopic(ctx context.Context, args map[string]any) (string, error) {
	assetID := argInt64(args, "asset_id")
	operation := normalizeKafkaOperation(argString(args, "operation"), "list")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	command, err := kafkaTopicCommand(operation, argString(args, "topic"))
	if err != nil {
		return "", err
	}
	if result, ok := checkKafkaToolPermission(ctx, assetID, command); !ok {
		return result.Message, nil
	}

	client, closeFn, err := openKafkaClient(ctx, assetID)
	if err != nil {
		return "", err
	}
	defer closeFn()

	admin := kadm.NewClient(client)
	switch operation {
	case "list":
		topics, err := listKafkaTopics(ctx, admin, argBool(args, "include_internal"))
		if err != nil {
			return "", fmt.Errorf("list Kafka topics: %w", err)
		}
		return marshalResult(kafkaTopicListResult(topics, args))
	case "get", "describe":
		topic := strings.TrimSpace(argString(args, "topic"))
		topics, err := admin.ListTopicsWithInternal(ctx, topic)
		if err != nil {
			return "", fmt.Errorf("describe Kafka topic: %w", err)
		}
		detail, ok := topics[topic]
		if !ok {
			return "", fmt.Errorf("Kafka topic not found: %s", topic)
		}
		return marshalResult(map[string]any{"topic": kafkaTopicDetailResult(detail)})
	default:
		return "", fmt.Errorf("unsupported kafka_topic operation: %s", operation)
	}
}

func handleKafkaConsumerGroup(ctx context.Context, args map[string]any) (string, error) {
	assetID := argInt64(args, "asset_id")
	operation := normalizeKafkaOperation(argString(args, "operation"), "list")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	command, err := kafkaConsumerGroupCommand(operation, argString(args, "group"))
	if err != nil {
		return "", err
	}
	if result, ok := checkKafkaToolPermission(ctx, assetID, command); !ok {
		return result.Message, nil
	}

	client, closeFn, err := openKafkaClient(ctx, assetID)
	if err != nil {
		return "", err
	}
	defer closeFn()

	admin := kadm.NewClient(client)
	switch operation {
	case "list":
		groups, err := admin.ListGroups(ctx)
		if err != nil {
			return "", fmt.Errorf("list Kafka consumer groups: %w", err)
		}
		return marshalResult(map[string]any{"groups": kafkaConsumerGroupsResult(groups), "count": len(groups)})
	case "get", "describe":
		group := strings.TrimSpace(argString(args, "group"))
		groups, err := admin.DescribeGroups(ctx, group)
		if err != nil {
			return "", fmt.Errorf("describe Kafka consumer group: %w", err)
		}
		detail, ok := groups[group]
		if !ok {
			return "", fmt.Errorf("Kafka consumer group not found: %s", group)
		}
		result := kafkaConsumerGroupDetailResult(detail)
		if lags, lagErr := admin.Lag(ctx, group); lagErr != nil {
			result["lag_error"] = lagErr.Error()
		} else if lag, ok := lags[group]; ok {
			result["lag"] = kafkaConsumerGroupLagResult(lag)
			result["total_lag"] = lag.Lag.Total()
		}
		return marshalResult(map[string]any{"group": result})
	default:
		return "", fmt.Errorf("unsupported kafka_consumer_group operation: %s", operation)
	}
}

func checkKafkaToolPermission(ctx context.Context, assetID int64, command string) (CheckResult, bool) {
	if checker := GetPolicyChecker(ctx); checker != nil {
		result := checker.CheckForAsset(ctx, assetID, asset_entity.AssetTypeKafka, command)
		setCheckResult(ctx, result)
		if result.Decision != Allow {
			return result, false
		}
		return result, true
	}
	return CheckResult{Decision: Allow}, true
}

func openKafkaClient(ctx context.Context, assetID int64) (*kgo.Client, func(), error) {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil, func() {}, fmt.Errorf("asset not found: %w", err)
	}
	if !asset.IsKafka() {
		return nil, func() {}, fmt.Errorf("asset is not Kafka type")
	}
	cfg, err := asset.GetKafkaConfig()
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to get Kafka config: %w", err)
	}

	wrapper, err := getOrDialKafka(ctx, asset, cfg)
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to connect to Kafka: %w", err)
	}
	if getKafkaCache(ctx) != nil {
		return wrapper.client, func() {}, nil
	}
	return wrapper.client, func() {
		if err := wrapper.Close(); err != nil {
			logger.Default().Warn("close Kafka connection", zap.Error(err))
		}
	}, nil
}

func getOrDialKafka(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.KafkaConfig) (*kafkaClientCloser, error) {
	dialFn := func() (*kafkaClientCloser, io.Closer, error) {
		password, err := credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve credentials: %w", err)
		}
		client, err := connpool.DialKafka(ctx, asset, cfg, password, getSSHPool(ctx))
		if err != nil {
			return nil, nil, err
		}
		return &kafkaClientCloser{client: client}, nil, nil
	}
	if cache := getKafkaCache(ctx); cache != nil {
		wrapper, _, err := cache.GetOrDial(asset.ID, dialFn)
		return wrapper, err
	}
	wrapper, _, err := dialFn()
	return wrapper, err
}

func normalizeKafkaOperation(operation, fallback string) string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "" {
		return fallback
	}
	return operation
}

func kafkaClusterCommand(operation string) (string, error) {
	switch operation {
	case "overview":
		return "cluster.read *", nil
	case "brokers", "list_brokers":
		return "broker.read *", nil
	default:
		return "", fmt.Errorf("unsupported kafka_cluster operation: %s", operation)
	}
}

func kafkaTopicCommand(operation, topic string) (string, error) {
	switch operation {
	case "list":
		return "topic.list *", nil
	case "get", "describe":
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return "", fmt.Errorf("topic is required for kafka_topic %s", operation)
		}
		return "topic.read " + topic, nil
	default:
		return "", fmt.Errorf("unsupported kafka_topic operation: %s", operation)
	}
}

func kafkaConsumerGroupCommand(operation, group string) (string, error) {
	switch operation {
	case "list":
		return "consumer_group.list *", nil
	case "get", "describe":
		group = strings.TrimSpace(group)
		if group == "" {
			return "", fmt.Errorf("group is required for kafka_consumer_group %s", operation)
		}
		return "consumer_group.read " + group, nil
	default:
		return "", fmt.Errorf("unsupported kafka_consumer_group operation: %s", operation)
	}
}

func listKafkaTopics(ctx context.Context, admin *kadm.Client, includeInternal bool) (kadm.TopicDetails, error) {
	if includeInternal {
		return admin.ListTopicsWithInternal(ctx)
	}
	return admin.ListTopics(ctx)
}

func kafkaOverviewResult(assetID int64, metadata kadm.Metadata) map[string]any {
	out := map[string]any{
		"asset_id":        assetID,
		"cluster_id":      metadata.Cluster,
		"controller_id":   metadata.Controller,
		"broker_count":    len(metadata.Brokers),
		"topic_count":     len(metadata.Topics),
		"brokers":         kafkaBrokersResult(metadata.Brokers),
		"partition_count": 0,
	}
	internalTopics := 0
	offlinePartitions := 0
	underReplicatedPartitions := 0
	partitionCount := 0
	for _, topic := range metadata.Topics {
		if topic.IsInternal {
			internalTopics++
		}
		for _, partition := range topic.Partitions {
			partitionCount++
			if partition.Leader < 0 {
				offlinePartitions++
			}
			if len(partition.Replicas) > 0 && len(partition.ISR) < len(partition.Replicas) {
				underReplicatedPartitions++
			}
		}
	}
	out["internal_topic_count"] = internalTopics
	out["partition_count"] = partitionCount
	out["offline_partition_count"] = offlinePartitions
	out["under_replicated_partition_count"] = underReplicatedPartitions
	return out
}

func kafkaBrokersResult(details kadm.BrokerDetails) []map[string]any {
	out := make([]map[string]any, 0, len(details))
	for _, detail := range details {
		broker := map[string]any{
			"node_id": detail.NodeID,
			"host":    detail.Host,
			"port":    detail.Port,
		}
		if detail.Rack != nil {
			broker["rack"] = *detail.Rack
		}
		out = append(out, broker)
	}
	return out
}

func kafkaTopicListResult(topics kadm.TopicDetails, args map[string]any) map[string]any {
	search := strings.ToLower(strings.TrimSpace(argString(args, "search")))
	page, pageSize := normalizeKafkaPage(argInt(args, "page"), argInt(args, "page_size"))
	items := make([]map[string]any, 0, len(topics))
	for _, topic := range topics.Sorted() {
		item := kafkaTopicSummaryResult(topic)
		name, _ := item["name"].(string)
		if search != "" && !strings.Contains(strings.ToLower(name), search) {
			continue
		}
		items = append(items, item)
	}
	total := len(items)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return map[string]any{
		"topics":    items[start:end],
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}
}

func kafkaTopicSummaryResult(topic kadm.TopicDetail) map[string]any {
	replicationFactor := 0
	offlinePartitions := 0
	underReplicatedPartitions := 0
	for _, partition := range topic.Partitions {
		if len(partition.Replicas) > replicationFactor {
			replicationFactor = len(partition.Replicas)
		}
		if partition.Leader < 0 {
			offlinePartitions++
		}
		if len(partition.Replicas) > 0 && len(partition.ISR) < len(partition.Replicas) {
			underReplicatedPartitions++
		}
	}
	out := map[string]any{
		"name":                             topic.Topic,
		"id":                               topic.ID.String(),
		"internal":                         topic.IsInternal,
		"partition_count":                  len(topic.Partitions),
		"replication_factor":               replicationFactor,
		"offline_partition_count":          offlinePartitions,
		"under_replicated_partition_count": underReplicatedPartitions,
	}
	if topic.Err != nil {
		out["error"] = topic.Err.Error()
	}
	return out
}

func kafkaTopicDetailResult(topic kadm.TopicDetail) map[string]any {
	out := kafkaTopicSummaryResult(topic)
	partitions := make([]map[string]any, 0, len(topic.Partitions))
	for _, partition := range topic.Partitions.Sorted() {
		item := map[string]any{
			"partition":        partition.Partition,
			"leader":           partition.Leader,
			"leader_epoch":     partition.LeaderEpoch,
			"replicas":         partition.Replicas,
			"isr":              partition.ISR,
			"offline_replicas": partition.OfflineReplicas,
		}
		if partition.Err != nil {
			item["error"] = partition.Err.Error()
		}
		partitions = append(partitions, item)
	}
	out["partitions"] = partitions
	out["authorized_operations"] = kafkaACLOperations(topic.AuthorizedOperations)
	return out
}

func kafkaConsumerGroupsResult(groups kadm.ListedGroups) []map[string]any {
	out := make([]map[string]any, 0, len(groups))
	for _, group := range groups.Sorted() {
		out = append(out, map[string]any{
			"group":         group.Group,
			"coordinator":   group.Coordinator,
			"protocol_type": group.ProtocolType,
			"state":         group.State,
		})
	}
	return out
}

func kafkaConsumerGroupDetailResult(group kadm.DescribedGroup) map[string]any {
	out := map[string]any{
		"group":         group.Group,
		"coordinator":   kafkaBrokersResult(kadm.BrokerDetails{group.Coordinator})[0],
		"state":         group.State,
		"protocol_type": group.ProtocolType,
		"protocol":      group.Protocol,
		"members":       kafkaConsumerGroupMembersResult(group.Members),
	}
	if group.Err != nil {
		out["error"] = group.Err.Error()
	}
	if group.ErrMessage != "" {
		out["error_message"] = group.ErrMessage
	}
	out["authorized_operations"] = kafkaACLOperations(group.AuthorizedOperations)
	return out
}

func kafkaConsumerGroupMembersResult(members []kadm.DescribedGroupMember) []map[string]any {
	out := make([]map[string]any, 0, len(members))
	for _, member := range members {
		item := map[string]any{
			"member_id":   member.MemberID,
			"client_id":   member.ClientID,
			"client_host": member.ClientHost,
		}
		if member.InstanceID != nil {
			item["instance_id"] = *member.InstanceID
		}
		if assignment, ok := member.Assigned.AsConsumer(); ok {
			assigned := make([]map[string]any, 0, len(assignment.Topics))
			for _, topic := range assignment.Topics {
				partitions := append([]int32(nil), topic.Partitions...)
				sort.Slice(partitions, func(i, j int) bool { return partitions[i] < partitions[j] })
				assigned = append(assigned, map[string]any{
					"topic":      topic.Topic,
					"partitions": partitions,
				})
			}
			item["assigned_partitions"] = assigned
		}
		out = append(out, item)
	}
	return out
}

func kafkaConsumerGroupLagResult(lag kadm.DescribedGroupLag) []map[string]any {
	out := make([]map[string]any, 0)
	for _, partitionLag := range lag.Lag.Sorted() {
		item := map[string]any{
			"topic":            partitionLag.Topic,
			"partition":        partitionLag.Partition,
			"committed_offset": partitionLag.Commit.At,
			"end_offset":       partitionLag.End.Offset,
			"lag":              partitionLag.Lag,
		}
		if partitionLag.Member != nil {
			item["member_id"] = partitionLag.Member.MemberID
		}
		if partitionLag.Err != nil {
			item["error"] = partitionLag.Err.Error()
		}
		out = append(out, item)
	}
	return out
}

func kafkaACLOperations(ops []kadm.ACLOperation) []string {
	if len(ops) == 0 {
		return nil
	}
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.String())
	}
	return out
}

func normalizeKafkaPage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}
	return page, pageSize
}

func argBool(args map[string]any, key string) bool {
	if v, ok := args[key]; ok {
		switch b := v.(type) {
		case bool:
			return b
		case string:
			return strings.EqualFold(strings.TrimSpace(b), "true")
		}
	}
	return false
}
