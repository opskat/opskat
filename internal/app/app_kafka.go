package app

import (
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/kafka_svc"
)

func (a *App) kafkaSvc() *kafka_svc.Service {
	if a.kafkaService == nil {
		a.kafkaService = kafka_svc.New(a.sshPool)
	}
	return a.kafkaService
}

// TestKafkaConnection 测试 Kafka 连接
// configJSON: KafkaConfig JSON，plainPassword: 明文密码
func (a *App) TestKafkaConnection(configJSON string, plainPassword string) error {
	var cfg asset_entity.KafkaConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}
	return a.kafkaSvc().TestConnection(a.langCtx(), &cfg, plainPassword, 0)
}

func (a *App) KafkaClusterOverview(assetID int64) (kafka_svc.ClusterOverview, error) {
	return a.kafkaSvc().ClusterOverview(a.langCtx(), assetID)
}

func (a *App) KafkaListBrokers(assetID int64) ([]kafka_svc.Broker, error) {
	return a.kafkaSvc().ListBrokers(a.langCtx(), assetID)
}

func (a *App) KafkaListTopics(req kafka_svc.ListTopicsRequest) (kafka_svc.ListTopicsResponse, error) {
	return a.kafkaSvc().ListTopics(a.langCtx(), req)
}

func (a *App) KafkaGetTopic(assetID int64, topic string) (kafka_svc.TopicDetail, error) {
	return a.kafkaSvc().GetTopic(a.langCtx(), assetID, topic)
}

func (a *App) KafkaListConsumerGroups(assetID int64) ([]kafka_svc.ConsumerGroup, error) {
	return a.kafkaSvc().ListConsumerGroups(a.langCtx(), assetID)
}

func (a *App) KafkaGetConsumerGroup(assetID int64, group string) (kafka_svc.ConsumerGroupDetail, error) {
	return a.kafkaSvc().GetConsumerGroup(a.langCtx(), assetID, group)
}

func (a *App) KafkaBrowseMessages(req kafka_svc.BrowseMessagesRequest) (kafka_svc.BrowseMessagesResponse, error) {
	return a.kafkaSvc().BrowseMessages(a.langCtx(), req)
}

func (a *App) KafkaProduceMessage(req kafka_svc.ProduceMessageRequest) (kafka_svc.ProduceMessageResponse, error) {
	return a.kafkaSvc().ProduceMessage(a.langCtx(), req)
}

func (a *App) KafkaCreateTopic(req kafka_svc.CreateTopicRequest) (kafka_svc.TopicOperationResponse, error) {
	return a.kafkaSvc().CreateTopic(a.langCtx(), req)
}

func (a *App) KafkaDeleteTopic(assetID int64, topic string) (kafka_svc.TopicOperationResponse, error) {
	return a.kafkaSvc().DeleteTopic(a.langCtx(), assetID, topic)
}

func (a *App) KafkaAlterTopicConfig(req kafka_svc.AlterTopicConfigRequest) (kafka_svc.TopicOperationResponse, error) {
	return a.kafkaSvc().AlterTopicConfig(a.langCtx(), req)
}

func (a *App) KafkaIncreasePartitions(req kafka_svc.IncreasePartitionsRequest) (kafka_svc.TopicOperationResponse, error) {
	return a.kafkaSvc().IncreasePartitions(a.langCtx(), req)
}

func (a *App) KafkaDeleteRecords(req kafka_svc.DeleteRecordsRequest) (kafka_svc.DeleteRecordsResponse, error) {
	return a.kafkaSvc().DeleteRecords(a.langCtx(), req)
}
