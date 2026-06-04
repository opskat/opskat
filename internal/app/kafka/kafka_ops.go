package kafka

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/kafka_svc"
)

// testConnection 测试一份未保存的 Kafka 配置；经 conntest 注册表由
// System.TestAssetConnection 分发，信封（超时/取消/i18n ctx）由调用方统一施加。
func (k *Kafka) testConnection(ctx context.Context, configJSON string, plainPassword string) error {
	var cfg asset_entity.KafkaConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}
	return k.service.TestConnection(ctx, &cfg, plainPassword, 0)
}

func (k *Kafka) KafkaClusterOverview(assetID int64) (kafka_svc.ClusterOverview, error) {
	return k.service.ClusterOverview(i18n.Ctx(k.ctx, k.lang.Lang()), assetID)
}

func (k *Kafka) KafkaListBrokers(assetID int64) ([]kafka_svc.Broker, error) {
	return k.service.ListBrokers(i18n.Ctx(k.ctx, k.lang.Lang()), assetID)
}

func (k *Kafka) KafkaListTopics(req kafka_svc.ListTopicsRequest) (kafka_svc.ListTopicsResponse, error) {
	return k.service.ListTopics(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaGetTopic(assetID int64, topic string) (kafka_svc.TopicDetail, error) {
	return k.service.GetTopic(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, topic)
}

func (k *Kafka) KafkaListConsumerGroups(assetID int64) ([]kafka_svc.ConsumerGroup, error) {
	return k.service.ListConsumerGroups(i18n.Ctx(k.ctx, k.lang.Lang()), assetID)
}

func (k *Kafka) KafkaGetConsumerGroup(assetID int64, group string) (kafka_svc.ConsumerGroupDetail, error) {
	return k.service.GetConsumerGroup(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, group)
}

func (k *Kafka) KafkaBrowseMessages(req kafka_svc.BrowseMessagesRequest) (kafka_svc.BrowseMessagesResponse, error) {
	return k.service.BrowseMessages(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaProduceMessage(req kafka_svc.ProduceMessageRequest) (kafka_svc.ProduceMessageResponse, error) {
	return k.service.ProduceMessage(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaCreateTopic(req kafka_svc.CreateTopicRequest) (kafka_svc.TopicOperationResponse, error) {
	return k.service.CreateTopic(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaDeleteTopic(assetID int64, topic string) (kafka_svc.TopicOperationResponse, error) {
	return k.service.DeleteTopic(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, topic)
}

func (k *Kafka) KafkaAlterTopicConfig(req kafka_svc.AlterTopicConfigRequest) (kafka_svc.TopicOperationResponse, error) {
	return k.service.AlterTopicConfig(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaIncreasePartitions(req kafka_svc.IncreasePartitionsRequest) (kafka_svc.TopicOperationResponse, error) {
	return k.service.IncreasePartitions(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaDeleteRecords(req kafka_svc.DeleteRecordsRequest) (kafka_svc.DeleteRecordsResponse, error) {
	return k.service.DeleteRecords(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaGetBrokerConfig(assetID int64, brokerID int32) (kafka_svc.BrokerConfigResponse, error) {
	return k.service.GetBrokerConfig(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, brokerID)
}

func (k *Kafka) KafkaListClusterConfigs(assetID int64) (kafka_svc.ClusterConfigsResponse, error) {
	return k.service.ListClusterConfigs(i18n.Ctx(k.ctx, k.lang.Lang()), assetID)
}

func (k *Kafka) KafkaResetConsumerGroupOffset(req kafka_svc.ResetConsumerGroupOffsetRequest) (kafka_svc.ResetConsumerGroupOffsetResponse, error) {
	return k.service.ResetConsumerGroupOffset(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaDeleteConsumerGroup(assetID int64, group string) (kafka_svc.DeleteConsumerGroupResponse, error) {
	return k.service.DeleteConsumerGroup(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, group)
}

func (k *Kafka) KafkaListACLs(req kafka_svc.ListACLsRequest) (kafka_svc.ListACLsResponse, error) {
	return k.service.ListACLs(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaCreateACL(req kafka_svc.CreateACLRequest) (kafka_svc.ACLMutationResponse, error) {
	return k.service.CreateACL(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaDeleteACL(req kafka_svc.DeleteACLRequest) (kafka_svc.ACLMutationResponse, error) {
	return k.service.DeleteACL(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaListSchemaSubjects(assetID int64) ([]string, error) {
	return k.service.ListSchemaSubjects(i18n.Ctx(k.ctx, k.lang.Lang()), assetID)
}

func (k *Kafka) KafkaGetSchemaSubjectVersions(assetID int64, subject string) (kafka_svc.SchemaSubjectVersions, error) {
	return k.service.GetSchemaSubjectVersions(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, subject)
}

func (k *Kafka) KafkaGetSchema(assetID int64, subject string, version string) (kafka_svc.SchemaVersionDetail, error) {
	return k.service.GetSchema(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, subject, version)
}

func (k *Kafka) KafkaCheckSchemaCompatibility(req kafka_svc.CheckSchemaCompatibilityRequest) (kafka_svc.CheckSchemaCompatibilityResponse, error) {
	return k.service.CheckSchemaCompatibility(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaRegisterSchema(req kafka_svc.RegisterSchemaRequest) (kafka_svc.RegisterSchemaResponse, error) {
	return k.service.RegisterSchema(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaDeleteSchema(req kafka_svc.DeleteSchemaRequest) (kafka_svc.DeleteSchemaResponse, error) {
	return k.service.DeleteSchema(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaListConnectClusters(assetID int64) ([]kafka_svc.KafkaConnectCluster, error) {
	return k.service.ListConnectClusters(i18n.Ctx(k.ctx, k.lang.Lang()), assetID)
}

func (k *Kafka) KafkaListConnectors(req kafka_svc.ListConnectorsRequest) ([]kafka_svc.KafkaConnectorSummary, error) {
	return k.service.ListConnectors(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaGetConnector(assetID int64, cluster string, name string) (kafka_svc.KafkaConnectorDetail, error) {
	return k.service.GetConnector(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, cluster, name)
}

func (k *Kafka) KafkaCreateConnector(req kafka_svc.ConnectorConfigRequest) (kafka_svc.ConnectorOperationResponse, error) {
	return k.service.CreateConnector(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaUpdateConnectorConfig(req kafka_svc.ConnectorConfigRequest) (kafka_svc.ConnectorOperationResponse, error) {
	return k.service.UpdateConnectorConfig(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaPauseConnector(assetID int64, cluster string, name string) (kafka_svc.ConnectorOperationResponse, error) {
	return k.service.PauseConnector(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, cluster, name)
}

func (k *Kafka) KafkaResumeConnector(assetID int64, cluster string, name string) (kafka_svc.ConnectorOperationResponse, error) {
	return k.service.ResumeConnector(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, cluster, name)
}

func (k *Kafka) KafkaRestartConnector(req kafka_svc.RestartConnectorRequest) (kafka_svc.ConnectorOperationResponse, error) {
	return k.service.RestartConnector(i18n.Ctx(k.ctx, k.lang.Lang()), req)
}

func (k *Kafka) KafkaDeleteConnector(assetID int64, cluster string, name string) (kafka_svc.ConnectorOperationResponse, error) {
	return k.service.DeleteConnector(i18n.Ctx(k.ctx, k.lang.Lang()), assetID, cluster, name)
}
