package command_review_svc

import (
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/pkg/cmdredact"
	"github.com/opskat/opskat/internal/pkg/shelldecode"
)

// redactSyntax 是权限检查声明的命令写法在替换敏感信息时对应的写法。
func redactSyntax(s permission.Syntax) cmdredact.Syntax {
	switch s {
	case permission.SyntaxShell:
		return cmdredact.Shell
	case permission.SyntaxRedis:
		return cmdredact.Redis
	default:
		return cmdredact.Text
	}
}

// commandForReview 返回真正交给替换敏感信息和审核器的命令：shell 里「解码后交给 shell 执行」
// 的写法换成解码出来的脚本，其余写法原样返回。
func commandForReview(req permission.ReviewRequest) (string, error) {
	if req.Syntax != permission.SyntaxShell {
		return req.Command, nil
	}
	return shelldecode.Reveal(req.Command)
}

// cacheKeyCommand 让解码展开过的命令不命中展开前的缓存。没展开时键和原来一样。
func cacheKeyCommand(original, reviewed string) string {
	if reviewed == original {
		return original
	}
	return original + "\x00" + reviewed
}
