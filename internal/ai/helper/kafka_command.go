package helper

import (
	"fmt"
	"strings"
)

// 这 7 个 Kafka*Command 把 (operation, target) 映射为策略匹配用的
// "<action> <resource>" 双 token 串，是 KafkaCommand.PolicyString 的实现地基
// （kafka_dsl.go 的 policyString 直接调它们，不重写一遍）。
// operation 的取值是 KafkaCommand.operation() 的产物，见 kafkaOperationFor。

// KafkaClusterCommand 将 cluster family 的 operation 映射为策略命令字符串。
func KafkaClusterCommand(operation string) (string, error) {
	switch operation {
	case "overview":
		return "cluster.read *", nil
	case "brokers", "list_brokers":
		return "broker.read *", nil
	case "get_broker_config":
		return "broker.config.read *", nil
	case "list_cluster_configs":
		return "cluster.config.read *", nil
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("cluster", operation))
	}
}

func KafkaTopicCommand(operation, topic string) (string, error) {
	switch operation {
	case "list":
		return "topic.list *", nil
	case "get", "describe":
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return "", fmt.Errorf("topic is required for %q", kafkaCommandForm("topic", operation))
		}
		return "topic.read " + topic, nil
	case "create":
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return "", fmt.Errorf("topic is required for %q", kafkaCommandForm("topic", operation))
		}
		return "topic.create " + topic, nil
	case "delete":
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return "", fmt.Errorf("topic is required for %q", kafkaCommandForm("topic", operation))
		}
		return "topic.delete " + topic, nil
	case "update_config":
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return "", fmt.Errorf("topic is required for %q", kafkaCommandForm("topic", operation))
		}
		return "topic.config.write " + topic, nil
	case "increase_partitions":
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return "", fmt.Errorf("topic is required for %q", kafkaCommandForm("topic", operation))
		}
		return "topic.partitions.write " + topic, nil
	case "delete_records":
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return "", fmt.Errorf("topic is required for %q", kafkaCommandForm("topic", operation))
		}
		return "topic.records.delete " + topic, nil
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("topic", operation))
	}
}

func KafkaConsumerGroupCommand(operation, group string) (string, error) {
	switch operation {
	case "list":
		return "consumer_group.list *", nil
	case "get", "describe":
		group = strings.TrimSpace(group)
		if group == "" {
			return "", fmt.Errorf("group is required for %q", kafkaCommandForm("consumer-group", operation))
		}
		return "consumer_group.read " + group, nil
	case "reset_offset":
		group = strings.TrimSpace(group)
		if group == "" {
			return "", fmt.Errorf("group is required for %q", kafkaCommandForm("consumer-group", operation))
		}
		return "consumer_group.offset.write " + group, nil
	case "delete":
		group = strings.TrimSpace(group)
		if group == "" {
			return "", fmt.Errorf("group is required for %q", kafkaCommandForm("consumer-group", operation))
		}
		return "consumer_group.delete " + group, nil
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("consumer-group", operation))
	}
}

func KafkaACLCommand(operation string) (string, error) {
	switch operation {
	case "list":
		return "acl.read *", nil
	case "create", "delete":
		return "acl.write *", nil
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("acl", operation))
	}
}

func KafkaSchemaCommand(operation, subject string) (string, error) {
	switch operation {
	case "list_subjects":
		return "schema.read *", nil
	case "list_versions", "get", "describe", "check_compatibility":
		subject = strings.TrimSpace(subject)
		if subject == "" {
			return "", fmt.Errorf("subject is required for %q", kafkaCommandForm("schema", operation))
		}
		return "schema.read " + subject, nil
	case "register":
		subject = strings.TrimSpace(subject)
		if subject == "" {
			return "", fmt.Errorf("subject is required for %q", kafkaCommandForm("schema", operation))
		}
		return "schema.write " + subject, nil
	case "delete":
		subject = strings.TrimSpace(subject)
		if subject == "" {
			return "", fmt.Errorf("subject is required for %q", kafkaCommandForm("schema", operation))
		}
		return "schema.delete " + subject, nil
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("schema", operation))
	}
}

func KafkaConnectCommand(operation, connector string) (string, error) {
	switch operation {
	case "list_clusters", "list_connectors":
		return "connect.read *", nil
	case "get_connector", "get", "describe":
		connector = strings.TrimSpace(connector)
		if connector == "" {
			return "", fmt.Errorf("connector is required for %q", kafkaCommandForm("connect", operation))
		}
		return "connect.read " + connector, nil
	case "create", "update_config":
		connector = strings.TrimSpace(connector)
		if connector == "" {
			return "", fmt.Errorf("connector is required for %q", kafkaCommandForm("connect", operation))
		}
		return "connect.write " + connector, nil
	case "pause", "resume", "restart":
		connector = strings.TrimSpace(connector)
		if connector == "" {
			return "", fmt.Errorf("connector is required for %q", kafkaCommandForm("connect", operation))
		}
		return "connect.state.write " + connector, nil
	case "delete":
		connector = strings.TrimSpace(connector)
		if connector == "" {
			return "", fmt.Errorf("connector is required for %q", kafkaCommandForm("connect", operation))
		}
		return "connect.delete " + connector, nil
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("connect", operation))
	}
}

func KafkaMessageCommand(operation, topic string) (string, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "", fmt.Errorf("topic is required for %q", kafkaCommandForm("message", operation))
	}
	switch operation {
	case "browse", "inspect":
		return "message.read " + topic, nil
	case "produce":
		return "message.write " + topic, nil
	default:
		return "", fmt.Errorf("unsupported kafka command %q", kafkaCommandForm("message", operation))
	}
}
