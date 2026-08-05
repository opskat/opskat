// Package ssh_agent_source_entity 定义 SSH Agent 来源的持久化实体。
//
// 来源是 Agent 端点配置（端点类型 + 端点值）+ 显示元数据，不含任何身份、公钥、
// 签名、私钥或运行时状态——那些只在运行时存在于传输层，绝不进入数据库。
package ssh_agent_source_entity

// EndpointType 是来源端点值的解释方式，与 internal/sshagent 的端点类型一致。
type EndpointType string

const (
	EndpointTypeEnvironment      EndpointType = "environment"
	EndpointTypeUnixSocket       EndpointType = "unix_socket"
	EndpointTypeWindowsNamedPipe EndpointType = "windows_named_pipe"
)

// SSHAgentSource 持久化的 SSH Agent 来源定义。
type SSHAgentSource struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name         string `gorm:"column:name;type:varchar(255);not null" json:"name"`
	EndpointType string `gorm:"column:endpoint_type;type:varchar(50);not null" json:"endpoint_type"`
	Endpoint     string `gorm:"column:endpoint;type:text;not null" json:"endpoint"`
	Description  string `gorm:"column:description;type:text" json:"description,omitempty"`
	Createtime   int64  `gorm:"column:createtime" json:"createtime"`
	Updatetime   int64  `gorm:"column:updatetime" json:"updatetime"`
}

// TableName GORM 表名
func (SSHAgentSource) TableName() string {
	return "ssh_agent_sources"
}
