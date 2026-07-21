package helper

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/service/kafka_svc"
)

// --- Kafka service context ---

type kafkaServiceKeyType struct{}

// WithKafkaService 将 Kafka 服务注入 context，让 AI handler 在同一次 AI Send 内复用
// franz-go client（避免每次工具调用都重新 dial+ping）。
func WithKafkaService(ctx context.Context, svc *kafka_svc.Service) context.Context {
	if svc == nil {
		return ctx
	}
	return context.WithValue(ctx, kafkaServiceKeyType{}, svc)
}

func getKafkaService(ctx context.Context) *kafka_svc.Service {
	if svc, ok := ctx.Value(kafkaServiceKeyType{}).(*kafka_svc.Service); ok {
		return svc
	}
	return nil
}

// kafkaServiceFromCtx 优先返回 context 中已注入的服务（release 为 no-op），
// 缺省时按旧行为创建一次性 Service 并由调用方在 release 中关闭。
func kafkaServiceFromCtx(ctx context.Context) (*kafka_svc.Service, func()) {
	if svc := getKafkaService(ctx); svc != nil {
		return svc, func() {}
	}
	svc := kafka_svc.New(getSSHPool(ctx))
	return svc, svc.Close
}

// --- Handlers ---
//
// 这 7 个 HandleKafka* 只有一个调用方了：ExecKafkaOnAsset（kafka_exec.go），统一 exec
// 工具的 kafka 执行体。它们**不做权限检查**——检查由 handleExec 在调用执行器之前统一
// 完成（internal/ai/tool/tool_handlers_unified.go），用的是 CanonicalizeKafkaCommand
// 产出的双 token 策略串。别在这里补一次"保险起见"的检查：同一条命令被检查两次时，
// 用户若选的是一次性"允许"而非"全部允许"，第二次检查会为同一条命令再弹一次审批
// 对话框（HandleConfirm 只在 allowAll 时持久化 grant），并多写一条审计行。

func HandleKafkaCluster(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	operation := NormalizeKafkaOperation(aictx.ArgString(args, "operation"), "overview")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	svc, release := kafkaServiceFromCtx(ctx)
	defer release()

	switch operation {
	case "overview":
		result, err := svc.ClusterOverview(ctx, assetID)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "brokers", "list_brokers":
		brokers, err := svc.ListBrokers(ctx, assetID)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(map[string]any{"brokers": brokers, "count": len(brokers)})
	case "get_broker_config":
		brokerID := int32(aictx.ArgInt64(args, "broker_id"))
		result, err := svc.GetBrokerConfig(ctx, assetID, brokerID)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "list_cluster_configs":
		result, err := svc.ListClusterConfigs(ctx, assetID)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("cluster", operation))
	}
}

func HandleKafkaTopic(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	operation := NormalizeKafkaOperation(aictx.ArgString(args, "operation"), "list")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	svc, release := kafkaServiceFromCtx(ctx)
	defer release()

	switch operation {
	case "list":
		req := kafka_svc.ListTopicsRequest{
			AssetID:         assetID,
			IncludeInternal: aictx.ArgBool(args, "include_internal"),
			Search:          strings.TrimSpace(aictx.ArgString(args, "search")),
			Page:            int(aictx.ArgInt64(args, "page")),
			PageSize:        int(aictx.ArgInt64(args, "page_size")),
		}
		result, err := svc.ListTopics(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "get", "describe":
		result, err := svc.GetTopic(ctx, assetID, strings.TrimSpace(aictx.ArgString(args, "topic")))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "create":
		req, err := KafkaCreateTopicRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.CreateTopic(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "delete":
		result, err := svc.DeleteTopic(ctx, assetID, strings.TrimSpace(aictx.ArgString(args, "topic")))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "update_config":
		req, err := KafkaAlterTopicConfigRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.AlterTopicConfig(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "increase_partitions":
		req := kafkaIncreasePartitionsRequestFromArgs(assetID, args)
		result, err := svc.IncreasePartitions(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "delete_records":
		req, err := KafkaDeleteRecordsRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.DeleteRecords(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("topic", operation))
	}
}

func HandleKafkaConsumerGroup(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	operation := NormalizeKafkaOperation(aictx.ArgString(args, "operation"), "list")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	svc, release := kafkaServiceFromCtx(ctx)
	defer release()

	switch operation {
	case "list":
		groups, err := svc.ListConsumerGroups(ctx, assetID)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(map[string]any{"groups": groups, "count": len(groups)})
	case "get", "describe":
		result, err := svc.GetConsumerGroup(ctx, assetID, strings.TrimSpace(aictx.ArgString(args, "group")))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "reset_offset":
		req, err := KafkaResetConsumerGroupOffsetRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.ResetConsumerGroupOffset(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "delete":
		result, err := svc.DeleteConsumerGroup(ctx, assetID, strings.TrimSpace(aictx.ArgString(args, "group")))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("consumer-group", operation))
	}
}

func HandleKafkaACL(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	operation := NormalizeKafkaOperation(aictx.ArgString(args, "operation"), "list")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	svc, release := kafkaServiceFromCtx(ctx)
	defer release()

	switch operation {
	case "list":
		result, err := svc.ListACLs(ctx, KafkaListACLsRequestFromArgs(assetID, args))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "create":
		result, err := svc.CreateACL(ctx, KafkaCreateACLRequestFromArgs(assetID, args))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "delete":
		result, err := svc.DeleteACL(ctx, KafkaDeleteACLRequestFromArgs(assetID, args))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("acl", operation))
	}
}

func HandleKafkaSchema(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	operation := NormalizeKafkaOperation(aictx.ArgString(args, "operation"), "list_subjects")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	svc, release := kafkaServiceFromCtx(ctx)
	defer release()

	switch operation {
	case "list_subjects":
		result, err := svc.ListSchemaSubjects(ctx, assetID)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(map[string]any{"subjects": result, "count": len(result)})
	case "list_versions":
		result, err := svc.GetSchemaSubjectVersions(ctx, assetID, strings.TrimSpace(aictx.ArgString(args, "subject")))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "get", "describe":
		result, err := svc.GetSchema(ctx, assetID, strings.TrimSpace(aictx.ArgString(args, "subject")), aictx.ArgString(args, "version"))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "check_compatibility":
		req, err := KafkaCheckSchemaCompatibilityRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.CheckSchemaCompatibility(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "register":
		req, err := KafkaRegisterSchemaRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.RegisterSchema(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "delete":
		result, err := svc.DeleteSchema(ctx, KafkaDeleteSchemaRequestFromArgs(assetID, args))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("schema", operation))
	}
}

func HandleKafkaConnect(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	operation := NormalizeKafkaOperation(aictx.ArgString(args, "operation"), "list_connectors")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	svc, release := kafkaServiceFromCtx(ctx)
	defer release()

	cluster := aictx.ArgString(args, "cluster")
	switch operation {
	case "list_clusters":
		result, err := svc.ListConnectClusters(ctx, assetID)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(map[string]any{"clusters": result, "count": len(result)})
	case "list_connectors":
		result, err := svc.ListConnectors(ctx, kafka_svc.ListConnectorsRequest{AssetID: assetID, Cluster: cluster})
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(map[string]any{"connectors": result, "count": len(result)})
	case "get_connector", "get", "describe":
		result, err := svc.GetConnector(ctx, assetID, cluster, strings.TrimSpace(aictx.ArgString(args, "connector")))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "create":
		req, err := KafkaConnectorConfigRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.CreateConnector(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "update_config":
		req, err := KafkaConnectorConfigRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.UpdateConnectorConfig(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "pause":
		result, err := svc.PauseConnector(ctx, assetID, cluster, strings.TrimSpace(aictx.ArgString(args, "connector")))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "resume":
		result, err := svc.ResumeConnector(ctx, assetID, cluster, strings.TrimSpace(aictx.ArgString(args, "connector")))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "restart":
		result, err := svc.RestartConnector(ctx, KafkaRestartConnectorRequestFromArgs(assetID, args))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "delete":
		result, err := svc.DeleteConnector(ctx, assetID, cluster, strings.TrimSpace(aictx.ArgString(args, "connector")))
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("connect", operation))
	}
}

func HandleKafkaMessage(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	operation := NormalizeKafkaOperation(aictx.ArgString(args, "operation"), "browse")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	svc, release := kafkaServiceFromCtx(ctx)
	defer release()

	switch operation {
	case "browse":
		req, err := kafkaBrowseRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.BrowseMessages(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "inspect":
		req, err := kafkaInspectRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.BrowseMessages(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	case "produce":
		req, err := kafkaProduceRequestFromArgs(assetID, args)
		if err != nil {
			return "", err
		}
		result, err := svc.ProduceMessage(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalKafkaResult(result)
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("message", operation))
	}
}

func NormalizeKafkaOperation(operation, fallback string) string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "" {
		return fallback
	}
	return operation
}
