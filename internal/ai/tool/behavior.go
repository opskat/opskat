package tool

// Behavior 描述一个工具对外部环境的副作用，供在调用前向用户展示的客户端使用
// （opsctl mcp 把它映射成 MCP tool annotations）。它是提示，不是权限边界：
// 真正的拦截仍由策略检查与审批完成。
type Behavior struct {
	// ReadOnly 为真表示不改动任何环境。
	ReadOnly bool
	// Destructive 为真表示可能做破坏性修改（仅在 ReadOnly 为假时有意义）。
	Destructive bool
	// Idempotent 为真表示同参数重复调用没有额外效果。
	Idempotent bool
	// OpenWorld 为真表示会触达 OpsKat 自身数据之外的外部系统（远端服务器、数据库……）。
	OpenWorld bool
}

var (
	behaviorRead       = Behavior{ReadOnly: true, Idempotent: true}
	behaviorRemote     = Behavior{Destructive: true, OpenWorld: true}
	behaviorDelete     = Behavior{Destructive: true}
	behaviorLocalWrite = Behavior{}
)

// toolBehaviors 与 Tools() 里的定义一一对应；新增工具时在这里声明它的副作用，
// TestEveryToolDeclaresBehavior 会拦住漏声明的工具。
var toolBehaviors = map[string]Behavior{
	"list_assets":      behaviorRead,
	"get_asset":        behaviorRead,
	"list_groups":      behaviorRead,
	"get_group":        behaviorRead,
	"list_credentials": behaviorRead,
	"get_credential":   behaviorRead,
	"help":             behaviorRead,

	"exec":       behaviorRemote,
	"batch_exec": behaviorRemote,
	"cp":         behaviorRemote,

	"delete_asset": behaviorDelete,
	"delete_group": behaviorDelete,

	"put_asset":          behaviorLocalWrite,
	"put_group":          behaviorLocalWrite,
	"request_permission": behaviorLocalWrite,
	"get_asset_secret":   behaviorLocalWrite,
}

// BehaviorOf 返回工具声明的副作用；未声明的工具返回 false。
func BehaviorOf(name string) (Behavior, bool) {
	b, ok := toolBehaviors[name]
	return b, ok
}
