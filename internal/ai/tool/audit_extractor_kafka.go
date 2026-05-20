package tool

import (
	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/helper"
)

// 注册 Kafka 协议的命令摘要提取器到 audit 包。
// 提取器引用本包内的 kafkaXxxCommand 助手，因此从本包 init() 完成注册。
func init() {
	audit.RegisterExtractor("kafka_cluster", func(a map[string]any) string {
		cmd, _ := helper.KafkaClusterCommand(helper.NormalizeKafkaOperation(aictx.ArgString(a, "operation"), "overview"))
		return cmd
	})
	audit.RegisterExtractor("kafka_topic", func(a map[string]any) string {
		cmd, _ := helper.KafkaTopicCommand(helper.NormalizeKafkaOperation(aictx.ArgString(a, "operation"), "list"), aictx.ArgString(a, "topic"))
		return cmd
	})
	audit.RegisterExtractor("kafka_consumer_group", func(a map[string]any) string {
		cmd, _ := helper.KafkaConsumerGroupCommand(helper.NormalizeKafkaOperation(aictx.ArgString(a, "operation"), "list"), aictx.ArgString(a, "group"))
		return cmd
	})
	audit.RegisterExtractor("kafka_acl", func(a map[string]any) string {
		cmd, _ := helper.KafkaACLCommand(helper.NormalizeKafkaOperation(aictx.ArgString(a, "operation"), "list"))
		return cmd
	})
	audit.RegisterExtractor("kafka_schema", func(a map[string]any) string {
		cmd, _ := helper.KafkaSchemaCommand(helper.NormalizeKafkaOperation(aictx.ArgString(a, "operation"), "list_subjects"), aictx.ArgString(a, "subject"))
		return cmd
	})
	audit.RegisterExtractor("kafka_connect", func(a map[string]any) string {
		cmd, _ := helper.KafkaConnectCommand(helper.NormalizeKafkaOperation(aictx.ArgString(a, "operation"), "list_connectors"), aictx.ArgString(a, "connector"))
		return cmd
	})
	audit.RegisterExtractor("kafka_message", func(a map[string]any) string {
		cmd, _ := helper.KafkaMessageCommand(helper.NormalizeKafkaOperation(aictx.ArgString(a, "operation"), "browse"), aictx.ArgString(a, "topic"))
		return cmd
	})
}
