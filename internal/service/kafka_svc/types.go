package kafka_svc

type ClusterOverview struct {
	AssetID                       int64  `json:"assetId"`
	ClusterID                     string `json:"clusterId"`
	ControllerID                  int32  `json:"controllerId"`
	BrokerCount                   int    `json:"brokerCount"`
	TopicCount                    int    `json:"topicCount"`
	InternalTopicCount            int    `json:"internalTopicCount"`
	PartitionCount                int    `json:"partitionCount"`
	OfflinePartitionCount         int    `json:"offlinePartitionCount"`
	UnderReplicatedPartitionCount int    `json:"underReplicatedPartitionCount"`
}

type Broker struct {
	NodeID int32  `json:"nodeId"`
	Host   string `json:"host"`
	Port   int32  `json:"port"`
	Rack   string `json:"rack,omitempty"`
}

type ListTopicsRequest struct {
	AssetID         int64  `json:"assetId"`
	IncludeInternal bool   `json:"includeInternal"`
	Search          string `json:"search,omitempty"`
	Page            int    `json:"page,omitempty"`
	PageSize        int    `json:"pageSize,omitempty"`
}

type ListTopicsResponse struct {
	Topics   []TopicSummary `json:"topics"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
}

type TopicSummary struct {
	Name                          string `json:"name"`
	ID                            string `json:"id,omitempty"`
	Internal                      bool   `json:"internal"`
	PartitionCount                int    `json:"partitionCount"`
	ReplicationFactor             int    `json:"replicationFactor"`
	OfflinePartitionCount         int    `json:"offlinePartitionCount"`
	UnderReplicatedPartitionCount int    `json:"underReplicatedPartitionCount"`
	Error                         string `json:"error,omitempty"`
}

type TopicDetail struct {
	TopicSummary
	Partitions           []TopicPartition `json:"partitions"`
	AuthorizedOperations []string         `json:"authorizedOperations,omitempty"`
}

type TopicPartition struct {
	Partition       int32   `json:"partition"`
	Leader          int32   `json:"leader"`
	LeaderEpoch     int32   `json:"leaderEpoch"`
	Replicas        []int32 `json:"replicas"`
	ISR             []int32 `json:"isr"`
	OfflineReplicas []int32 `json:"offlineReplicas"`
	Error           string  `json:"error,omitempty"`
}

type CreateTopicRequest struct {
	AssetID           int64             `json:"assetId"`
	Topic             string            `json:"topic"`
	Partitions        int32             `json:"partitions"`
	ReplicationFactor int16             `json:"replicationFactor"`
	Configs           map[string]string `json:"configs,omitempty"`
}

type TopicOperationResponse struct {
	Topic   string `json:"topic"`
	Message string `json:"message,omitempty"`
}

type AlterTopicConfigRequest struct {
	AssetID int64                 `json:"assetId"`
	Topic   string                `json:"topic"`
	Configs []TopicConfigMutation `json:"configs"`
}

type TopicConfigMutation struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	Op    string `json:"op,omitempty"`
}

type IncreasePartitionsRequest struct {
	AssetID    int64  `json:"assetId"`
	Topic      string `json:"topic"`
	Partitions int    `json:"partitions"`
}

type DeleteRecordsRequest struct {
	AssetID    int64                    `json:"assetId"`
	Topic      string                   `json:"topic"`
	Partitions []DeleteRecordsPartition `json:"partitions"`
}

type DeleteRecordsPartition struct {
	Partition int32 `json:"partition"`
	Offset    int64 `json:"offset"`
}

type DeleteRecordsResponse struct {
	Topic      string                         `json:"topic"`
	Partitions []DeleteRecordsPartitionResult `json:"partitions"`
}

type DeleteRecordsPartitionResult struct {
	Partition    int32  `json:"partition"`
	LowWatermark int64  `json:"lowWatermark"`
	Error        string `json:"error,omitempty"`
}

type ConsumerGroup struct {
	Group        string `json:"group"`
	Coordinator  int32  `json:"coordinator"`
	ProtocolType string `json:"protocolType,omitempty"`
	State        string `json:"state,omitempty"`
}

type ConsumerGroupDetail struct {
	Group        string                      `json:"group"`
	Coordinator  Broker                      `json:"coordinator"`
	State        string                      `json:"state,omitempty"`
	ProtocolType string                      `json:"protocolType,omitempty"`
	Protocol     string                      `json:"protocol,omitempty"`
	Members      []ConsumerGroupMember       `json:"members"`
	Lag          []ConsumerGroupPartitionLag `json:"lag,omitempty"`
	TotalLag     int64                       `json:"totalLag"`
	Error        string                      `json:"error,omitempty"`
	LagError     string                      `json:"lagError,omitempty"`
}

type ConsumerGroupMember struct {
	MemberID           string                     `json:"memberId"`
	InstanceID         string                     `json:"instanceId,omitempty"`
	ClientID           string                     `json:"clientId"`
	ClientHost         string                     `json:"clientHost"`
	AssignedPartitions []TopicPartitionAssignment `json:"assignedPartitions,omitempty"`
}

type TopicPartitionAssignment struct {
	Topic      string  `json:"topic"`
	Partitions []int32 `json:"partitions"`
}

type ConsumerGroupPartitionLag struct {
	Topic           string `json:"topic"`
	Partition       int32  `json:"partition"`
	CommittedOffset int64  `json:"committedOffset"`
	EndOffset       int64  `json:"endOffset"`
	Lag             int64  `json:"lag"`
	MemberID        string `json:"memberId,omitempty"`
	Error           string `json:"error,omitempty"`
}

type BrowseMessagesRequest struct {
	AssetID         int64  `json:"assetId"`
	Topic           string `json:"topic"`
	Partition       *int32 `json:"partition,omitempty"`
	StartMode       string `json:"startMode,omitempty"`
	Offset          int64  `json:"offset,omitempty"`
	TimestampMillis int64  `json:"timestampMillis,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	MaxBytes        int    `json:"maxBytes,omitempty"`
	DecodeMode      string `json:"decodeMode,omitempty"`
	MaxWaitMillis   int    `json:"maxWaitMillis,omitempty"`
}

type BrowseMessagesResponse struct {
	Topic      string          `json:"topic"`
	Partitions []int32         `json:"partitions"`
	StartMode  string          `json:"startMode"`
	Limit      int             `json:"limit"`
	MaxBytes   int             `json:"maxBytes"`
	Records    []KafkaRecord   `json:"records"`
	NextOffset map[int32]int64 `json:"nextOffset,omitempty"`
	Errors     []string        `json:"errors,omitempty"`
}

type KafkaRecord struct {
	Topic           string              `json:"topic"`
	Partition       int32               `json:"partition"`
	Offset          int64               `json:"offset"`
	Timestamp       string              `json:"timestamp"`
	TimestampMillis int64               `json:"timestampMillis"`
	Key             string              `json:"key,omitempty"`
	KeyBytes        int                 `json:"keyBytes"`
	KeyEncoding     string              `json:"keyEncoding"`
	KeyTruncated    bool                `json:"keyTruncated"`
	Value           string              `json:"value,omitempty"`
	ValueBytes      int                 `json:"valueBytes"`
	ValueEncoding   string              `json:"valueEncoding"`
	ValueTruncated  bool                `json:"valueTruncated"`
	Headers         []KafkaRecordHeader `json:"headers,omitempty"`
}

type KafkaRecordHeader struct {
	Key            string `json:"key"`
	Value          string `json:"value,omitempty"`
	ValueBytes     int    `json:"valueBytes"`
	ValueEncoding  string `json:"valueEncoding"`
	ValueTruncated bool   `json:"valueTruncated"`
}

type ProduceMessageRequest struct {
	AssetID         int64                  `json:"assetId"`
	Topic           string                 `json:"topic"`
	Partition       *int32                 `json:"partition,omitempty"`
	Key             string                 `json:"key,omitempty"`
	KeyEncoding     string                 `json:"keyEncoding,omitempty"`
	Value           string                 `json:"value,omitempty"`
	ValueEncoding   string                 `json:"valueEncoding,omitempty"`
	Headers         []ProduceMessageHeader `json:"headers,omitempty"`
	TimestampMillis int64                  `json:"timestampMillis,omitempty"`
}

type ProduceMessageHeader struct {
	Key      string `json:"key"`
	Value    string `json:"value,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

type ProduceMessageResponse struct {
	Topic           string `json:"topic"`
	Partition       int32  `json:"partition"`
	Offset          int64  `json:"offset"`
	Timestamp       string `json:"timestamp"`
	TimestampMillis int64  `json:"timestampMillis"`
}
