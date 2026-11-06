package transfer

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// Workers 是多文件传输（SFTP 目录传输、OSS 文件夹下载、AI / opsctl 多源 cp）同时在传的
// 条目数上限。小文件的耗时几乎全是往返等待，多个条目同时在传才能把这些等待叠起来；设上限
// 是因为每个条目自己还可能有并发读写窗口（sftpio），条目数不加限制时在途请求与缓冲会随之
// 成倍增长。
const Workers = 8

// Dispatch 按 entries 的顺序派发一批传输条目。isDir 为真的条目由派发方当场调用 mkdir，因此
// 文件被派发时，清单里排在它前面的目录已经存在；其余条目交给最多 Workers 个 worker 并发
// send。isDir 为 nil 表示清单里没有目录。
//
// 快速失败：第一个失败的条目（或 mkdir）取消其余正在传的条目，尚未开始的不再开始，它的
// 错误作为返回值；ctx 取消时所有 worker 随之停止，取消落在两个条目之间、没有条目报错时
// 返回 ctx 的错误。返回前等待全部 worker 退出。
func Dispatch[E any](
	ctx context.Context, entries []E, isDir func(E) bool, mkdir func(E) error, send func(context.Context, E) error,
) error {
	dispatchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	g, workerCtx := errgroup.WithContext(dispatchCtx)
	g.SetLimit(Workers)

	var mkdirErr error
	for _, e := range entries {
		if workerCtx.Err() != nil {
			break
		}
		if isDir != nil && isDir(e) {
			if mkdirErr = mkdir(e); mkdirErr != nil {
				cancel()
				break
			}
			continue
		}
		g.Go(func() error {
			// g.Go 在 worker 占满时会等空位，等到时可能已有条目失败或调用方已取消。
			if err := workerCtx.Err(); err != nil {
				return err
			}
			return send(workerCtx, e)
		})
	}
	waitErr := g.Wait()
	if mkdirErr != nil {
		return mkdirErr
	}
	if waitErr != nil {
		return waitErr
	}
	return ctx.Err()
}
