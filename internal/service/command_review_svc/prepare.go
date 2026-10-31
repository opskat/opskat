package command_review_svc

import (
	"github.com/opskat/opskat/internal/pkg/cmdredact"
	"github.com/opskat/opskat/internal/pkg/shelldecode"
)

// Syntax 是命令的写法，决定怎样在里面找敏感信息；取值由 cmdredact 定义。
type Syntax = cmdredact.Syntax

const (
	SyntaxText  = cmdredact.Text
	SyntaxShell = cmdredact.Shell
	SyntaxRedis = cmdredact.Redis
)

// ErrUndecodable 见 shelldecode.ErrUndecodable。
var ErrUndecodable = shelldecode.ErrUndecodable

// commandForReview 返回真正交给替换敏感信息和审核器的命令：shell 里「解码后交给 shell 执行」
// 的写法换成解码出来的脚本，其余写法原样返回。
func commandForReview(in Input) (string, error) {
	if in.Syntax != SyntaxShell {
		return in.Command, nil
	}
	return shelldecode.Reveal(in.Command)
}

// cacheKeyCommand 让解码展开过的命令不命中展开前的缓存。没展开时键和原来一样。
func cacheKeyCommand(original, reviewed string) string {
	if reviewed == original {
		return original
	}
	return original + "\x00" + reviewed
}
