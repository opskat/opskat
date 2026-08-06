// Package ssh_agent_svc 是 SSH Agent 来源的服务层：来源 CRUD（仅显式保存、
// 结构校验不探测）、发现候选项、探测/检查（打开→列出→校验→关闭，返回有界身份
// 摘要）、显式复制公钥，以及端点变更时的连接失效回调。运行时身份/公钥/签名绝不
// 进入持久化或模块级状态。
package ssh_agent_svc

import (
	"errors"
	"fmt"

	"github.com/opskat/opskat/internal/sshagent"
)

// 来源生命周期相关的稳定错误码（与规格错误码表一致）。传输层错误码由
// internal/sshagent 提供，这里只定义来源层语义。
const (
	CodeSourceNotFound = "ssh_agent_source_not_found"
	CodeSourceInUse    = "ssh_agent_source_in_use"
)

// Error 是来源层的类型化错误，携带稳定错误码与清理后的消息。
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func newSvcError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// CodeOf 返回 err 的类型化错误码。来源层错误直接识别；传输层错误（Inspect/
// Probe/CopyPublicKey 原样透传的 sshagent 类型化错误）也识别，消费方只需一个
// 查找入口。
func CodeOf(err error) (string, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return sshagent.CodeOf(err)
}
