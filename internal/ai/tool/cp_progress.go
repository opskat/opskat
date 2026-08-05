package tool

import "context"

// CpProgress 是 cp 完成一条传输后的可观察进度。Observer 只由交互式调用方（opsctl）注入；
// AI 路径不注入，继续只接收最终汇总。
type CpProgress struct {
	Completed int
	Total     int
	Src       string
	Dst       string
	Bytes     int64
}

type cpProgressObserver func(CpProgress)
type cpProgressContextKey struct{}

// WithCpProgressObserver 给一次 cp 调用附加进度观察器。
func WithCpProgressObserver(ctx context.Context, observer func(CpProgress)) context.Context {
	return context.WithValue(ctx, cpProgressContextKey{}, cpProgressObserver(observer))
}

// ReportCpProgress 向当前调用方报告一条已经完成的传输；没有 observer 时为空操作。
func ReportCpProgress(ctx context.Context, progress CpProgress) {
	if observer, ok := ctx.Value(cpProgressContextKey{}).(cpProgressObserver); ok {
		observer(progress)
	}
}
