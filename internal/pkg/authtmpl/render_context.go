package authtmpl

import "time"

// RequestInfo 是仅在 HTTP 认证渲染时才有意义的请求上下文，对应模板里的
// request.method / request.path / request.body。Path 不含 query string。
type RequestInfo struct {
	Method string
	Path   string
	Body   []byte
}

// RenderContext 携带一次 Render 需要的全部输入：字段取值、固定下来的 now，
// 以及可选的请求信息。同一个 RenderContext 在多次 Render 调用（例如同一次
// HTTP 请求里既渲染 URL 又渲染认证头）之间应当复用，这样 now 与 uuid 之外的
// 取值才会保持一致；不同的 RenderContext（例如两次独立的调用）应各自构造。
type RenderContext struct {
	fields  map[string]string
	now     time.Time
	request *RequestInfo
}

// RenderOption 用于在构造 RenderContext 时附加可选信息。
type RenderOption func(*RenderContext)

// WithClock 让调用方注入取时刻的函数，供测试固定 now 使用；不传时默认用
// time.Now。
func WithClock(clock func() time.Time) RenderOption {
	return func(rc *RenderContext) {
		rc.now = clock()
	}
}

// WithRequest 提供 request.* 可用的请求信息；不传时模板里出现 request.*
// 会在 Render 阶段报错（即使 Parse 阶段因 AllowRequest 而放行了语法）。
func WithRequest(req RequestInfo) RenderOption {
	return func(rc *RenderContext) {
		rc.request = &req
	}
}

// NewRenderContext 构造一次渲染所需的上下文。now 在这里取一次并固定下来，
// 之后这个 RenderContext 参与的所有渲染看到的都是同一个时刻。
func NewRenderContext(fields map[string]string, opts ...RenderOption) *RenderContext {
	rc := &RenderContext{fields: fields, now: time.Now()}
	for _, opt := range opts {
		opt(rc)
	}
	return rc
}
