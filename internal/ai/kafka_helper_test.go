package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/helper"
)

// TestKafkaPolicyCommandMapping 锁住 7 个 Kafka*Command 的输出串。
//
// 它们曾经是 7 个 kafka_* 工具的权限命令映射；工具删了，这些函数没删——
// KafkaCommand.PolicyString 直接调它们（internal/ai/helper/kafka_dsl.go 的
// policyString），所以本用例现在是策略串的地基，也是防止策略串漂移的第一道防线：
// 串的形式一变，用户库里既有的 CmdPolicy 与 grant 就全部失配，而失配是静默的
// （splitKafkaRule 在非 2-token 上返回 false，deny 规则一条都不匹配）。
func TestKafkaPolicyCommandMapping(t *testing.T) {
	cmd, err := helper.KafkaClusterCommand("overview")
	require.NoError(t, err)
	assert.Equal(t, "cluster.read *", cmd)

	cmd, err = helper.KafkaClusterCommand("brokers")
	require.NoError(t, err)
	assert.Equal(t, "broker.read *", cmd)

	cmd, err = helper.KafkaTopicCommand("list", "")
	require.NoError(t, err)
	assert.Equal(t, "topic.list *", cmd)

	cmd, err = helper.KafkaTopicCommand("get", "orders")
	require.NoError(t, err)
	assert.Equal(t, "topic.read orders", cmd)

	cmd, err = helper.KafkaTopicCommand("create", "orders")
	require.NoError(t, err)
	assert.Equal(t, "topic.create orders", cmd)

	cmd, err = helper.KafkaTopicCommand("delete", "orders")
	require.NoError(t, err)
	assert.Equal(t, "topic.delete orders", cmd)

	cmd, err = helper.KafkaTopicCommand("update_config", "orders")
	require.NoError(t, err)
	assert.Equal(t, "topic.config.write orders", cmd)

	cmd, err = helper.KafkaTopicCommand("increase_partitions", "orders")
	require.NoError(t, err)
	assert.Equal(t, "topic.partitions.write orders", cmd)

	cmd, err = helper.KafkaTopicCommand("delete_records", "orders")
	require.NoError(t, err)
	assert.Equal(t, "topic.records.delete orders", cmd)

	cmd, err = helper.KafkaConsumerGroupCommand("get", "billing-worker")
	require.NoError(t, err)
	assert.Equal(t, "consumer_group.read billing-worker", cmd)

	cmd, err = helper.KafkaConsumerGroupCommand("reset_offset", "billing-worker")
	require.NoError(t, err)
	assert.Equal(t, "consumer_group.offset.write billing-worker", cmd)

	cmd, err = helper.KafkaConsumerGroupCommand("delete", "billing-worker")
	require.NoError(t, err)
	assert.Equal(t, "consumer_group.delete billing-worker", cmd)

	cmd, err = helper.KafkaACLCommand("list")
	require.NoError(t, err)
	assert.Equal(t, "acl.read *", cmd)

	cmd, err = helper.KafkaACLCommand("create")
	require.NoError(t, err)
	assert.Equal(t, "acl.write *", cmd)

	cmd, err = helper.KafkaACLCommand("delete")
	require.NoError(t, err)
	assert.Equal(t, "acl.write *", cmd)

	cmd, err = helper.KafkaSchemaCommand("list_subjects", "")
	require.NoError(t, err)
	assert.Equal(t, "schema.read *", cmd)

	cmd, err = helper.KafkaSchemaCommand("get", "orders-value")
	require.NoError(t, err)
	assert.Equal(t, "schema.read orders-value", cmd)

	cmd, err = helper.KafkaSchemaCommand("check_compatibility", "orders-value")
	require.NoError(t, err)
	assert.Equal(t, "schema.read orders-value", cmd)

	cmd, err = helper.KafkaSchemaCommand("register", "orders-value")
	require.NoError(t, err)
	assert.Equal(t, "schema.write orders-value", cmd)

	cmd, err = helper.KafkaSchemaCommand("delete", "orders-value")
	require.NoError(t, err)
	assert.Equal(t, "schema.delete orders-value", cmd)

	cmd, err = helper.KafkaConnectCommand("list_connectors", "")
	require.NoError(t, err)
	assert.Equal(t, "connect.read *", cmd)

	cmd, err = helper.KafkaConnectCommand("get_connector", "sink-orders")
	require.NoError(t, err)
	assert.Equal(t, "connect.read sink-orders", cmd)

	cmd, err = helper.KafkaConnectCommand("create", "sink-orders")
	require.NoError(t, err)
	assert.Equal(t, "connect.write sink-orders", cmd)

	cmd, err = helper.KafkaConnectCommand("pause", "sink-orders")
	require.NoError(t, err)
	assert.Equal(t, "connect.state.write sink-orders", cmd)

	cmd, err = helper.KafkaConnectCommand("delete", "sink-orders")
	require.NoError(t, err)
	assert.Equal(t, "connect.delete sink-orders", cmd)

	cmd, err = helper.KafkaMessageCommand("browse", "orders")
	require.NoError(t, err)
	assert.Equal(t, "message.read orders", cmd)

	cmd, err = helper.KafkaMessageCommand("inspect", "orders")
	require.NoError(t, err)
	assert.Equal(t, "message.read orders", cmd)

	cmd, err = helper.KafkaMessageCommand("produce", "orders")
	require.NoError(t, err)
	assert.Equal(t, "message.write orders", cmd)

	_, err = helper.KafkaTopicCommand("get", "")
	assert.Error(t, err)

	_, err = helper.KafkaMessageCommand("browse", "")
	assert.Error(t, err)

	_, err = helper.KafkaMessageCommand("delete", "orders")
	assert.Error(t, err)

	_, err = helper.KafkaACLCommand("grant")
	assert.Error(t, err)

	_, err = helper.KafkaSchemaCommand("get", "")
	assert.Error(t, err)

	_, err = helper.KafkaConnectCommand("get_connector", "")
	assert.Error(t, err)
}

func TestKafkaMessageArgs(t *testing.T) {
	partition, err := helper.ArgOptionalPartition(map[string]any{"partition": float64(2)})
	require.NoError(t, err)
	require.NotNil(t, partition)
	assert.Equal(t, int32(2), *partition)

	headers, err := helper.KafkaProduceHeadersFromArgs(map[string]any{
		"headers": `[{"key":"trace","value":"abc","encoding":"text"}]`,
	})
	require.NoError(t, err)
	require.Len(t, headers, 1)
	assert.Equal(t, "trace", headers[0].Key)

	_, err = helper.KafkaProduceHeadersFromArgs(map[string]any{"headers": `{"key":"trace"}`})
	assert.Error(t, err)
}

func TestKafkaTopicAdminArgs(t *testing.T) {
	createReq, err := helper.KafkaCreateTopicRequestFromArgs(7, map[string]any{
		"topic":              "orders",
		"partitions":         float64(3),
		"replication_factor": float64(1),
		"configs":            `{"cleanup.policy":"compact"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(7), createReq.AssetID)
	assert.Equal(t, int32(3), createReq.Partitions)
	assert.Equal(t, int16(1), createReq.ReplicationFactor)
	assert.Equal(t, "compact", createReq.Configs["cleanup.policy"])

	updateReq, err := helper.KafkaAlterTopicConfigRequestFromArgs(7, map[string]any{
		"topic":          "orders",
		"config_updates": `[{"name":"retention.ms","value":"60000","op":"set"}]`,
	})
	require.NoError(t, err)
	require.Len(t, updateReq.Configs, 1)
	assert.Equal(t, "retention.ms", updateReq.Configs[0].Name)

	recordsReq, err := helper.KafkaDeleteRecordsRequestFromArgs(7, map[string]any{
		"topic":   "orders",
		"records": `[{"partition":0,"offset":123}]`,
	})
	require.NoError(t, err)
	require.Len(t, recordsReq.Partitions, 1)
	assert.Equal(t, int32(0), recordsReq.Partitions[0].Partition)
	assert.Equal(t, int64(123), recordsReq.Partitions[0].Offset)

	_, err = helper.KafkaStringMapFromJSON(`[{"bad":true}]`)
	assert.Error(t, err)

	_, err = helper.KafkaAlterTopicConfigRequestFromArgs(7, map[string]any{"topic": "orders"})
	assert.Error(t, err)

	_, err = helper.KafkaDeleteRecordsRequestFromArgs(7, map[string]any{"topic": "orders", "records": `{"partition":0}`})
	assert.Error(t, err)
}

func TestKafkaConsumerGroupAdminArgs(t *testing.T) {
	req, err := helper.KafkaResetConsumerGroupOffsetRequestFromArgs(7, map[string]any{
		"group":      "billing",
		"topic":      "orders",
		"mode":       "offset",
		"offset":     float64(123),
		"partitions": `[0,1]`,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(7), req.AssetID)
	assert.Equal(t, "billing", req.Group)
	assert.Equal(t, "orders", req.Topic)
	assert.Equal(t, int64(123), req.Offset)
	assert.Equal(t, []int32{0, 1}, req.Partitions)

	partitions, err := helper.KafkaInt32SliceFromJSON(`[2,3]`)
	require.NoError(t, err)
	assert.Equal(t, []int32{2, 3}, partitions)

	_, err = helper.KafkaInt32SliceFromJSON(`{"bad":true}`)
	assert.Error(t, err)
}

func TestKafkaACLArgs(t *testing.T) {
	listReq := helper.KafkaListACLsRequestFromArgs(7, map[string]any{
		"resource_type": "topic",
		"resource_name": "orders",
		"pattern_type":  "match",
		"principal":     "User:alice",
		"host":          "*",
		"acl_operation": "read",
		"permission":    "allow",
		"page":          float64(2),
		"page_size":     float64(10),
	})
	assert.Equal(t, int64(7), listReq.AssetID)
	assert.Equal(t, "topic", listReq.ResourceType)
	assert.Equal(t, "orders", listReq.ResourceName)
	assert.Equal(t, "read", listReq.Operation)
	assert.Equal(t, 2, listReq.Page)
	assert.Equal(t, 10, listReq.PageSize)

	createReq := helper.KafkaCreateACLRequestFromArgs(7, map[string]any{
		"resource_type": "group",
		"resource_name": "billing",
		"principal":     "User:alice",
		"host":          "*",
		"acl_operation": "read",
		"permission":    "deny",
	})
	assert.Equal(t, "group", createReq.ResourceType)
	assert.Equal(t, "deny", createReq.Permission)

	deleteReq := helper.KafkaDeleteACLRequestFromArgs(7, map[string]any{
		"resource_type": "cluster",
		"principal":     "User:admin",
		"host":          "*",
		"acl_operation": "describe",
		"permission":    "allow",
	})
	assert.Equal(t, "cluster", deleteReq.ResourceType)
	assert.Equal(t, "describe", deleteReq.Operation)
}

func TestKafkaSchemaArgs(t *testing.T) {
	registerReq, err := helper.KafkaRegisterSchemaRequestFromArgs(7, map[string]any{
		"subject":     "orders-value",
		"schema":      `{"type":"record","name":"Order","fields":[]}`,
		"schema_type": "AVRO",
		"references":  `[{"name":"Common","subject":"common-value","version":1}]`,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(7), registerReq.AssetID)
	assert.Equal(t, "orders-value", registerReq.Subject)
	assert.Equal(t, "AVRO", registerReq.SchemaType)
	require.Len(t, registerReq.References, 1)
	assert.Equal(t, "common-value", registerReq.References[0].Subject)

	checkReq, err := helper.KafkaCheckSchemaCompatibilityRequestFromArgs(7, map[string]any{
		"subject": "orders-value",
		"version": "latest",
		"schema":  `{"type":"record","name":"Order","fields":[]}`,
	})
	require.NoError(t, err)
	assert.Equal(t, "latest", checkReq.Version)

	deleteReq := helper.KafkaDeleteSchemaRequestFromArgs(7, map[string]any{
		"subject":   "orders-value",
		"version":   "2",
		"permanent": "true",
	})
	assert.True(t, deleteReq.Permanent)
	assert.Equal(t, "2", deleteReq.Version)

	_, err = helper.KafkaSchemaReferencesFromJSON(`{"bad":true}`)
	assert.Error(t, err)
}

func TestKafkaConnectArgs(t *testing.T) {
	configReq, err := helper.KafkaConnectorConfigRequestFromArgs(7, map[string]any{
		"cluster":   "local",
		"connector": "sink-orders",
		"config":    `{"connector.class":"FileStreamSink"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(7), configReq.AssetID)
	assert.Equal(t, "local", configReq.Cluster)
	assert.Equal(t, "sink-orders", configReq.Name)
	assert.Equal(t, "FileStreamSink", configReq.Config["connector.class"])

	restartReq := helper.KafkaRestartConnectorRequestFromArgs(7, map[string]any{
		"cluster":       "local",
		"connector":     "sink-orders",
		"include_tasks": "true",
		"only_failed":   "true",
	})
	assert.True(t, restartReq.IncludeTasks)
	assert.True(t, restartReq.OnlyFailed)

	_, err = helper.KafkaConnectorConfigRequestFromArgs(7, map[string]any{"config": `[]`})
	assert.Error(t, err)
}
