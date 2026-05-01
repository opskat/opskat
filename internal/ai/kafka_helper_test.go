package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKafkaToolCommandMapping(t *testing.T) {
	cmd, err := kafkaClusterCommand("overview")
	require.NoError(t, err)
	assert.Equal(t, "cluster.read *", cmd)

	cmd, err = kafkaClusterCommand("brokers")
	require.NoError(t, err)
	assert.Equal(t, "broker.read *", cmd)

	cmd, err = kafkaTopicCommand("list", "")
	require.NoError(t, err)
	assert.Equal(t, "topic.list *", cmd)

	cmd, err = kafkaTopicCommand("get", "orders")
	require.NoError(t, err)
	assert.Equal(t, "topic.read orders", cmd)

	cmd, err = kafkaConsumerGroupCommand("get", "billing-worker")
	require.NoError(t, err)
	assert.Equal(t, "consumer_group.read billing-worker", cmd)

	_, err = kafkaTopicCommand("get", "")
	assert.Error(t, err)
}

func TestAllToolDefsContainsGroupedKafkaTools(t *testing.T) {
	tools := map[string]bool{}
	for _, def := range AllToolDefs() {
		tools[def.Name] = true
	}

	assert.True(t, tools["kafka_cluster"])
	assert.True(t, tools["kafka_topic"])
	assert.True(t, tools["kafka_consumer_group"])
	assert.False(t, tools["kafka_topic_delete"])
}
