package helper

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/service/kafka_svc"
)

// --- Topic admin ---

func KafkaCreateTopicRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.CreateTopicRequest, error) {
	configs, err := KafkaStringMapFromJSON(aictx.ArgString(args, "configs"))
	if err != nil {
		return kafka_svc.CreateTopicRequest{}, err
	}
	return kafka_svc.CreateTopicRequest{
		AssetID:           assetID,
		Topic:             aictx.ArgString(args, "topic"),
		Partitions:        int32(aictx.ArgInt(args, "partitions")),
		ReplicationFactor: int16(aictx.ArgInt(args, "replication_factor")),
		Configs:           configs,
	}, nil
}

func KafkaAlterTopicConfigRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.AlterTopicConfigRequest, error) {
	var updates []kafka_svc.TopicConfigMutation
	raw := strings.TrimSpace(aictx.ArgString(args, "config_updates"))
	if raw == "" {
		return kafka_svc.AlterTopicConfigRequest{}, fmt.Errorf("config_updates is required for kafka_topic update_config")
	}
	if err := json.Unmarshal([]byte(raw), &updates); err != nil {
		return kafka_svc.AlterTopicConfigRequest{}, fmt.Errorf("config_updates must be a JSON array: %w", err)
	}
	return kafka_svc.AlterTopicConfigRequest{
		AssetID: assetID,
		Topic:   aictx.ArgString(args, "topic"),
		Configs: updates,
	}, nil
}

func kafkaIncreasePartitionsRequestFromArgs(assetID int64, args map[string]any) kafka_svc.IncreasePartitionsRequest {
	return kafka_svc.IncreasePartitionsRequest{
		AssetID:    assetID,
		Topic:      aictx.ArgString(args, "topic"),
		Partitions: aictx.ArgInt(args, "partition_count"),
	}
}

func KafkaDeleteRecordsRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.DeleteRecordsRequest, error) {
	raw := strings.TrimSpace(aictx.ArgString(args, "records"))
	if raw == "" {
		return kafka_svc.DeleteRecordsRequest{}, fmt.Errorf("records is required for kafka_topic delete_records")
	}
	var partitions []kafka_svc.DeleteRecordsPartition
	if err := json.Unmarshal([]byte(raw), &partitions); err != nil {
		return kafka_svc.DeleteRecordsRequest{}, fmt.Errorf("records must be a JSON array: %w", err)
	}
	return kafka_svc.DeleteRecordsRequest{
		AssetID:    assetID,
		Topic:      aictx.ArgString(args, "topic"),
		Partitions: partitions,
	}, nil
}

func KafkaStringMapFromJSON(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	configs := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &configs); err != nil {
		return nil, fmt.Errorf("configs must be a JSON object: %w", err)
	}
	return configs, nil
}

// --- Consumer group admin ---

func KafkaResetConsumerGroupOffsetRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.ResetConsumerGroupOffsetRequest, error) {
	partitions, err := KafkaInt32SliceFromJSON(aictx.ArgString(args, "partitions"))
	if err != nil {
		return kafka_svc.ResetConsumerGroupOffsetRequest{}, err
	}
	return kafka_svc.ResetConsumerGroupOffsetRequest{
		AssetID:         assetID,
		Group:           aictx.ArgString(args, "group"),
		Topic:           aictx.ArgString(args, "topic"),
		Partitions:      partitions,
		Mode:            aictx.ArgString(args, "mode"),
		Offset:          aictx.ArgInt64(args, "offset"),
		TimestampMillis: aictx.ArgInt64(args, "timestamp_millis"),
	}, nil
}

func KafkaInt32SliceFromJSON(raw string) ([]int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var values []int32
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("partitions must be a JSON array of integers: %w", err)
	}
	return values, nil
}

// --- ACL ---

func KafkaListACLsRequestFromArgs(assetID int64, args map[string]any) kafka_svc.ListACLsRequest {
	return kafka_svc.ListACLsRequest{
		AssetID:      assetID,
		ResourceType: aictx.ArgString(args, "resource_type"),
		ResourceName: aictx.ArgString(args, "resource_name"),
		PatternType:  aictx.ArgString(args, "pattern_type"),
		Principal:    aictx.ArgString(args, "principal"),
		Host:         aictx.ArgString(args, "host"),
		Operation:    aictx.ArgString(args, "acl_operation"),
		Permission:   aictx.ArgString(args, "permission"),
		Page:         aictx.ArgInt(args, "page"),
		PageSize:     aictx.ArgInt(args, "page_size"),
	}
}

func KafkaCreateACLRequestFromArgs(assetID int64, args map[string]any) kafka_svc.CreateACLRequest {
	return kafka_svc.CreateACLRequest{
		AssetID:      assetID,
		ResourceType: aictx.ArgString(args, "resource_type"),
		ResourceName: aictx.ArgString(args, "resource_name"),
		PatternType:  aictx.ArgString(args, "pattern_type"),
		Principal:    aictx.ArgString(args, "principal"),
		Host:         aictx.ArgString(args, "host"),
		Operation:    aictx.ArgString(args, "acl_operation"),
		Permission:   aictx.ArgString(args, "permission"),
	}
}

func KafkaDeleteACLRequestFromArgs(assetID int64, args map[string]any) kafka_svc.DeleteACLRequest {
	return kafka_svc.DeleteACLRequest{
		AssetID:      assetID,
		ResourceType: aictx.ArgString(args, "resource_type"),
		ResourceName: aictx.ArgString(args, "resource_name"),
		PatternType:  aictx.ArgString(args, "pattern_type"),
		Principal:    aictx.ArgString(args, "principal"),
		Host:         aictx.ArgString(args, "host"),
		Operation:    aictx.ArgString(args, "acl_operation"),
		Permission:   aictx.ArgString(args, "permission"),
	}
}

// --- Schema Registry ---

func KafkaRegisterSchemaRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.RegisterSchemaRequest, error) {
	references, err := KafkaSchemaReferencesFromJSON(aictx.ArgString(args, "references"))
	if err != nil {
		return kafka_svc.RegisterSchemaRequest{}, err
	}
	return kafka_svc.RegisterSchemaRequest{
		AssetID:    assetID,
		Subject:    aictx.ArgString(args, "subject"),
		Schema:     aictx.ArgString(args, "schema"),
		SchemaType: aictx.ArgString(args, "schema_type"),
		References: references,
	}, nil
}

func KafkaCheckSchemaCompatibilityRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.CheckSchemaCompatibilityRequest, error) {
	references, err := KafkaSchemaReferencesFromJSON(aictx.ArgString(args, "references"))
	if err != nil {
		return kafka_svc.CheckSchemaCompatibilityRequest{}, err
	}
	return kafka_svc.CheckSchemaCompatibilityRequest{
		AssetID:    assetID,
		Subject:    aictx.ArgString(args, "subject"),
		Version:    aictx.ArgString(args, "version"),
		Schema:     aictx.ArgString(args, "schema"),
		SchemaType: aictx.ArgString(args, "schema_type"),
		References: references,
	}, nil
}

func KafkaDeleteSchemaRequestFromArgs(assetID int64, args map[string]any) kafka_svc.DeleteSchemaRequest {
	return kafka_svc.DeleteSchemaRequest{
		AssetID:   assetID,
		Subject:   aictx.ArgString(args, "subject"),
		Version:   aictx.ArgString(args, "version"),
		Permanent: argBool(args, "permanent"),
	}
}

func KafkaSchemaReferencesFromJSON(raw string) ([]kafka_svc.SchemaReference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var references []kafka_svc.SchemaReference
	if err := json.Unmarshal([]byte(raw), &references); err != nil {
		return nil, fmt.Errorf("references must be a JSON array: %w", err)
	}
	return references, nil
}

// --- Kafka Connect ---

func KafkaConnectorConfigRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.ConnectorConfigRequest, error) {
	config, err := KafkaStringMapFromJSON(aictx.ArgString(args, "config"))
	if err != nil {
		return kafka_svc.ConnectorConfigRequest{}, err
	}
	if len(config) == 0 {
		return kafka_svc.ConnectorConfigRequest{}, fmt.Errorf("config is required for kafka_connect connector config operation")
	}
	return kafka_svc.ConnectorConfigRequest{
		AssetID: assetID,
		Cluster: aictx.ArgString(args, "cluster"),
		Name:    aictx.ArgString(args, "connector"),
		Config:  config,
	}, nil
}

func KafkaRestartConnectorRequestFromArgs(assetID int64, args map[string]any) kafka_svc.RestartConnectorRequest {
	return kafka_svc.RestartConnectorRequest{
		AssetID:      assetID,
		Cluster:      aictx.ArgString(args, "cluster"),
		Name:         aictx.ArgString(args, "connector"),
		IncludeTasks: argBool(args, "include_tasks"),
		OnlyFailed:   argBool(args, "only_failed"),
	}
}

// --- Messages ---

func kafkaBrowseRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.BrowseMessagesRequest, error) {
	partition, err := ArgOptionalPartition(args)
	if err != nil {
		return kafka_svc.BrowseMessagesRequest{}, err
	}
	return kafka_svc.BrowseMessagesRequest{
		AssetID:         assetID,
		Topic:           aictx.ArgString(args, "topic"),
		Partition:       partition,
		StartMode:       aictx.ArgString(args, "start_mode"),
		Offset:          aictx.ArgInt64(args, "offset"),
		TimestampMillis: aictx.ArgInt64(args, "timestamp_millis"),
		Limit:           aictx.ArgInt(args, "limit"),
		MaxBytes:        aictx.ArgInt(args, "max_bytes"),
		DecodeMode:      aictx.ArgString(args, "decode_mode"),
		MaxWaitMillis:   aictx.ArgInt(args, "max_wait_millis"),
	}, nil
}

func kafkaInspectRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.BrowseMessagesRequest, error) {
	partition, err := ArgOptionalPartition(args)
	if err != nil {
		return kafka_svc.BrowseMessagesRequest{}, err
	}
	if partition == nil {
		return kafka_svc.BrowseMessagesRequest{}, fmt.Errorf("partition is required for kafka_message inspect")
	}
	if _, ok := args["offset"]; !ok {
		return kafka_svc.BrowseMessagesRequest{}, fmt.Errorf("offset is required for kafka_message inspect")
	}
	return kafka_svc.BrowseMessagesRequest{
		AssetID:       assetID,
		Topic:         aictx.ArgString(args, "topic"),
		Partition:     partition,
		StartMode:     "offset",
		Offset:        aictx.ArgInt64(args, "offset"),
		Limit:         1,
		MaxBytes:      aictx.ArgInt(args, "max_bytes"),
		DecodeMode:    aictx.ArgString(args, "decode_mode"),
		MaxWaitMillis: aictx.ArgInt(args, "max_wait_millis"),
	}, nil
}

func kafkaProduceRequestFromArgs(assetID int64, args map[string]any) (kafka_svc.ProduceMessageRequest, error) {
	partition, err := ArgOptionalPartition(args)
	if err != nil {
		return kafka_svc.ProduceMessageRequest{}, err
	}
	headers, err := KafkaProduceHeadersFromArgs(args)
	if err != nil {
		return kafka_svc.ProduceMessageRequest{}, err
	}
	return kafka_svc.ProduceMessageRequest{
		AssetID:         assetID,
		Topic:           aictx.ArgString(args, "topic"),
		Partition:       partition,
		Key:             aictx.ArgString(args, "key"),
		KeyEncoding:     aictx.ArgString(args, "key_encoding"),
		Value:           aictx.ArgString(args, "value"),
		ValueEncoding:   aictx.ArgString(args, "value_encoding"),
		Headers:         headers,
		TimestampMillis: aictx.ArgInt64(args, "timestamp_millis"),
	}, nil
}

func KafkaProduceHeadersFromArgs(args map[string]any) ([]kafka_svc.ProduceMessageHeader, error) {
	raw := strings.TrimSpace(aictx.ArgString(args, "headers"))
	if raw == "" {
		return nil, nil
	}
	var headers []kafka_svc.ProduceMessageHeader
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return nil, fmt.Errorf("headers must be a JSON array: %w", err)
	}
	return headers, nil
}

// --- Misc helpers ---

func marshalKafkaResult(result any) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		logger.Default().Error("marshal Kafka result", zap.Error(err))
		return "", fmt.Errorf("序列化 Kafka 结果失败: %w", err)
	}
	return string(data), nil
}

// ArgOptionalPartition parses the optional kafka_message partition field.
func ArgOptionalPartition(args map[string]any) (*int32, error) {
	const key = "partition"
	value, ok := args[key]
	if !ok || value == nil {
		return nil, nil
	}

	var n int64
	switch v := value.(type) {
	case int:
		n = int64(v)
	case int32:
		n = int64(v)
	case int64:
		n = v
	case float64:
		if math.Trunc(v) != v {
			return nil, fmt.Errorf("%s must be an integer", key)
		}
		n = int64(v)
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return nil, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		n = parsed
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		n = parsed
	default:
		return nil, fmt.Errorf("%s must be an integer", key)
	}
	const (
		minInt32 = -1 << 31
		maxInt32 = 1<<31 - 1
	)
	if n < minInt32 || n > maxInt32 {
		return nil, fmt.Errorf("%s is out of int32 range", key)
	}
	out := int32(n)
	return &out, nil
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
