package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/cmdline"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/pkg/authtmpl"
)

// 通用资产的 HTTP 执行方式（docs/specs/2026-09-28-generic-asset.md「HTTP 请求」）：
//
//	<METHOD> <PATH> [-H 'Name: value']... [-d <data> | -d @<file> | -d @-] [-i]
//
// PATH 拼在渲染后的 Base URL 后面；宿主渲染认证绑定并注入 header / query / basic auth，
// 覆盖调用方写的同名 header / query。同源重定向跟随（最多 10 次）并重新注入认证，跨源
// 重定向报错、不把认证发往别的源。拨号经 connpool.NewHTTPTransport（隧道 / 代理链 / TLS）。
//
// 注入值（认证渲染结果、密钥）只出现在发出的请求里：不进日志、错误信息、审批与审计。

const httpUsage = "usage: <METHOD> <PATH> [-H 'Name: value']... [-d <data> | -d @<file> | -d @-] [-i]"

const maxGenericRedirects = 10

// GenericSecretMask 在展示用的地址（审批、help、详情页、错误信息）里顶替密钥字段的值：
// Base URL 模板可以引用密钥字段，实际请求用真值，展示结果里不能出现明文。
const GenericSecretMask = "****"

var httpMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
	http.MethodDelete, http.MethodHead, http.MethodOptions,
}

func init() {
	RegisterGenericMode(custom_type_entity.ExecModeHTTP, GenericMode{
		Canonicalize: canonicalizeHTTPCommand,
		Describe:     describeHTTPCommand,
		Exec:         execHTTPForAI,
		Stream:       streamHTTPMode,
	})
}

// httpHeader 是调用方用 -H 给出的一个请求头。
type httpHeader struct {
	name, value string
}

// httpBodySource 是 -d 的三种来源。
type httpBodySource int

const (
	httpBodyNone httpBodySource = iota
	httpBodyLiteral
	httpBodyFile
	httpBodyStdin
)

// HTTPCommand 是解析后的一次 HTTP 调用。exec 传入的内容一律按字面处理。
type HTTPCommand struct {
	Method string
	// rawPath 是调用方写的路径（不含 query），原样发出；path 是解码后的路径，用于策略匹配。
	rawPath  string
	path     string
	rawQuery string
	headers  []httpHeader
	body     httpBodySource
	bodyArg  string // 字面数据或文件路径
	Include  bool
}

// PolicySubject 是策略匹配对象：`<大写方法> <路径>`，路径不含 query（spec「策略、审批与审计」）。
// 用解码后的路径，这样 `%61dmin` 这类编码写法躲不过 `/admin/*` 的 deny 规则。
func (c *HTTPCommand) PolicySubject() string {
	return c.Method + " " + c.path
}

// requestURI 是拼在 Base URL 之后的部分：调用方写的路径与 query。
func (c *HTTPCommand) requestURI() string {
	if c.rawQuery == "" {
		return c.rawPath
	}
	return c.rawPath + "?" + c.rawQuery
}

// ParseHTTPCommand 解析 HTTP 方式的 argv。
func ParseHTTPCommand(argv []string) (*HTTPCommand, error) {
	if len(argv) < 2 {
		return nil, errors.New(httpUsage)
	}
	cmd := &HTTPCommand{Method: strings.ToUpper(argv[0])}
	if !slices.Contains(httpMethods, cmd.Method) {
		return nil, fmt.Errorf("unsupported HTTP method %q; use one of %s", argv[0], strings.Join(httpMethods, ", "))
	}
	if err := cmd.parsePath(argv[1]); err != nil {
		return nil, err
	}
	for i := 2; i < len(argv); i++ {
		arg := argv[i]
		switch arg {
		case "-i":
			cmd.Include = true
			continue
		case "-H", "-d":
		default:
			return nil, fmt.Errorf("unexpected argument %q; %s", arg, httpUsage)
		}
		if i+1 >= len(argv) {
			return nil, fmt.Errorf("%s requires a value", arg)
		}
		i++
		value := argv[i] //nolint:gosec // guarded by the i+1 >= len(argv) check above
		if arg == "-H" {
			name, v, ok := strings.Cut(value, ":")
			name = strings.TrimSpace(name)
			if !ok || name == "" || strings.ContainsAny(name, " \t") {
				return nil, fmt.Errorf("invalid header %q; write -H 'Name: value'", value)
			}
			cmd.headers = append(cmd.headers, httpHeader{name: name, value: strings.TrimSpace(v)})
			continue
		}
		if cmd.body != httpBodyNone {
			return nil, errors.New("-d can only be given once")
		}
		switch {
		case value == "@-":
			cmd.body = httpBodyStdin
		case strings.HasPrefix(value, "@"):
			cmd.body, cmd.bodyArg = httpBodyFile, strings.TrimPrefix(value, "@")
		default:
			cmd.body, cmd.bodyArg = httpBodyLiteral, value
		}
	}
	return cmd, nil
}

func (c *HTTPCommand) parsePath(raw string) error {
	if !strings.HasPrefix(raw, "/") {
		if strings.Contains(raw, "://") {
			return fmt.Errorf("absolute URL %q is not allowed: PATH is appended to the asset's Base URL and must start with /", raw)
		}
		return fmt.Errorf("PATH %q must start with /", raw)
	}
	if strings.Contains(raw, "#") {
		return fmt.Errorf("PATH %q must not contain a fragment (#)", raw)
	}
	c.rawPath, c.rawQuery, _ = strings.Cut(raw, "?")
	decoded, err := url.PathUnescape(c.rawPath)
	if err != nil {
		return fmt.Errorf("invalid PATH %q: %w", raw, err)
	}
	for _, seg := range strings.Split(decoded, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("PATH %q must not contain . or .. segments", raw)
		}
	}
	c.path = decoded
	return nil
}

func parseHTTPCommandLine(command string) (*HTTPCommand, error) {
	words, err := cmdline.Words(command)
	if err != nil {
		return nil, err
	}
	return ParseHTTPCommand(words)
}

func canonicalizeHTTPCommand(command string) (string, error) {
	cmd, err := parseHTTPCommandLine(command)
	if err != nil {
		return "", err
	}
	return cmd.PolicySubject(), nil
}

// RenderGenericBaseURL 渲染 HTTP 方式的 Base URL，结果必须是 http / https 绝对 URL。
// now 固定了这一次调用的时刻，与认证渲染共用（authtmpl.RenderContext 的 now 一致性）。
func RenderGenericBaseURL(ct *custom_type_entity.CustomType, values map[string]string, now time.Time) (*url.URL, error) {
	rendered, err := renderBaseURLText(ct, values, now)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(rendered)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("the rendered Base URL of custom type %q is not an absolute http(s) URL", ct.Slug)
	}
	return u, nil
}

func renderBaseURLText(ct *custom_type_entity.CustomType, values map[string]string, now time.Time) (string, error) {
	tmpl, err := authtmpl.Parse(ct.HTTP.BaseURL, authtmpl.ParseOptions{Fields: ct.FieldNames()})
	if err != nil {
		return "", fmt.Errorf("custom type %q Base URL: %w", ct.Slug, err)
	}
	rendered, err := tmpl.Render(authtmpl.NewRenderContext(values, authtmpl.WithClock(func() time.Time { return now })))
	if err != nil {
		return "", fmt.Errorf("render Base URL of custom type %q: %w", ct.Slug, err)
	}
	return rendered, nil
}

// RenderGenericDisplayBaseURL 渲染给人和模型看的 Base URL（审批、help、详情页）：先确认用真值
// 渲染出的是 http(s) 绝对 URL（exec 会因同一原因失败），再以 GenericSecretMask 代替密钥字段
// 重新渲染，只有真正发出的请求才用真值。
func RenderGenericDisplayBaseURL(ct *custom_type_entity.CustomType, values map[string]string, now time.Time) (string, error) {
	if _, err := RenderGenericBaseURL(ct, values, now); err != nil {
		return "", err
	}
	return displayAddress(ct, values, "", now)
}

// displayAddress 以掩码代替密钥字段渲染 Base URL 并接上调用方的 requestURI。掩码后的文本不一定
// 还是 URL（整条 Base URL 来自一个密钥字段时只剩掩码），所以返回展示文本；能解析成绝对 URL 时
// 再经 Redacted 隐去 userinfo 里的密码。
func displayAddress(ct *custom_type_entity.CustomType, values map[string]string, requestURI string, now time.Time) (string, error) {
	base, err := renderBaseURLText(ct, maskedGenericValues(ct, values), now)
	if err != nil {
		return "", err
	}
	text := base
	if requestURI != "" {
		text = strings.TrimRight(base, "/") + requestURI
	}
	if u, err := url.Parse(text); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Redacted(), nil
	}
	return text, nil
}

// maskedGenericValues 返回展示用的字段值：有值的密钥字段换成 GenericSecretMask。
func maskedGenericValues(ct *custom_type_entity.CustomType, values map[string]string) map[string]string {
	masked := make(map[string]string, len(values))
	for name, v := range values {
		masked[name] = v
	}
	for _, f := range ct.Fields {
		if f.Secret && masked[f.Name] != "" {
			masked[f.Name] = GenericSecretMask
		}
	}
	return masked
}

// targetURL 是这次请求实际发往的目标（不含注入的 query）：Base URL + 调用方的路径与 query。
func targetURL(t *GenericTarget, cmd *HTTPCommand, now time.Time) (*url.URL, error) {
	base, err := RenderGenericBaseURL(t.Type, t.Values, now)
	if err != nil {
		return nil, err
	}
	if cmd.rawPath == "" {
		// 只有「测试连接」这样构造：GET Base URL 本身（ParseHTTPCommand 要求 PATH 以 / 开头）。
		return base, nil
	}
	u, err := url.Parse(strings.TrimRight(base.String(), "/") + cmd.requestURI())
	if err != nil {
		// *url.Error 会带出拼好的完整 URL，而 Base URL 可能引用了密钥字段：只报调用方自己
		// 写的路径与解析原因。
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("invalid request path %q: %w", cmd.requestURI(), err)
	}
	return u, nil
}

// displayTarget 是 targetURL 的展示版本：Base URL 里的密钥字段以掩码代替。
func displayTarget(t *GenericTarget, cmd *HTTPCommand, now time.Time) (string, error) {
	return displayAddress(t.Type, t.Values, cmd.requestURI(), now)
}

func describeHTTPCommand(_ context.Context, t *GenericTarget, command string) (string, error) {
	cmd, err := parseHTTPCommandLine(command)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if _, err := targetURL(t, cmd, now); err != nil {
		return "", err
	}
	display, err := displayTarget(t, cmd, now)
	if err != nil {
		return "", err
	}
	return "HTTP request: " + cmd.Method + " " + display, nil
}

// genericHTTPResponse 是一次已完成请求的响应；调用方读完 Body 后必须 close。
type genericHTTPResponse struct {
	*http.Response
	route connpool.HTTPRoute
	close func()
}

// localInput 是调用方所在机器上的输入：-d @<文件> 读本地文件、-d @- 读 stdin。只有 opsctl
// 提供它；AI exec 传 nil——模型不能借 exec 读取桌面端的本地文件（那要经过本地工具的审批
// 门禁），也没有 stdin。
type localInput struct {
	stdin io.Reader
}

// sendGenericHTTP 发出一次请求。返回错误时请求没有完成，也没有任何响应可输出。
func sendGenericHTTP(ctx context.Context, t *GenericTarget, cmd *HTTPCommand, local *localInput) (*genericHTTPResponse, error) {
	body, err := readHTTPBody(cmd, local)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	target, err := targetURL(t, cmd, now)
	if err != nil {
		return nil, err
	}
	display, err := displayTarget(t, cmd, now)
	if err != nil {
		return nil, err
	}
	auth, err := parseAuthBindings(t.Type)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, cmd.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for _, h := range cmd.headers {
		req.Header.Add(h.name, h.value)
	}
	if err := auth.apply(req, t.Values, body, now); err != nil {
		return nil, err
	}

	transport, route, err := connpool.NewHTTPTransport(ctx, connpool.GenericHTTPConn(t.Asset, t.Config), getSSHPool(ctx))
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= maxGenericRedirects {
				return fmt.Errorf("stopped after %d redirects", maxGenericRedirects)
			}
			if !sameOrigin(next.URL, via[0].URL) {
				return fmt.Errorf("cross-origin redirect to %s refused: credentials are only sent to the origin of %s",
					originOf(next.URL), display)
			}
			// 307/308 保留请求体（GetBody 非 nil），301/302/303 改成无体的 GET。
			var redirectBody []byte
			if next.GetBody != nil {
				redirectBody = body
			}
			return auth.apply(next, t.Values, redirectBody, now)
		},
	}

	log := logger.Ctx(ctx).With(zap.Int64("assetID", t.Asset.ID), zap.String("customType", t.Type.Slug),
		zap.String("method", cmd.Method), zap.String("path", cmd.path))
	log.Info("generic http request start", zap.String("route", string(route)))
	started := time.Now()
	resp, err := client.Do(req) //nolint:bodyclose // closed by genericHTTPResponse.close, which every caller defers
	if err != nil {
		transport.CloseIdleConnections()
		err = redactURLError(err, cmd.Method, display)
		log.Error("generic http request failed", zap.Duration("elapsed", time.Since(started)), zap.Error(err))
		return nil, err
	}
	log.Info("generic http request end", zap.Int("status", resp.StatusCode), zap.Duration("elapsed", time.Since(started)))
	return &genericHTTPResponse{Response: resp, route: route, close: func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("close generic http response body", zap.Error(err))
		}
		transport.CloseIdleConnections()
	}}, nil
}

func readHTTPBody(cmd *HTTPCommand, local *localInput) ([]byte, error) {
	switch cmd.body {
	case httpBodyLiteral:
		return []byte(cmd.bodyArg), nil
	case httpBodyFile:
		if local == nil {
			return nil, errors.New("-d @<file> reads a local file, which is only available through opsctl; pass the data inline with -d")
		}
		data, err := os.ReadFile(cmd.bodyArg)
		if err != nil {
			return nil, fmt.Errorf("read request body file: %w", err)
		}
		return data, nil
	case httpBodyStdin:
		if local == nil || local.stdin == nil {
			return nil, errors.New("-d @- reads the request body from stdin, which is only available through opsctl; pass the data inline with -d")
		}
		data, err := io.ReadAll(local.stdin)
		if err != nil {
			return nil, fmt.Errorf("read request body from stdin: %w", err)
		}
		return data, nil
	default:
		return nil, nil
	}
}

// httpAuth 是一个类型的认证绑定，模板已解析。
type httpAuth []httpAuthBinding

type httpAuthBinding struct {
	kind   authtmpl.AuthType
	name   string
	values []*authtmpl.Template
}

func parseAuthBindings(ct *custom_type_entity.CustomType) (httpAuth, error) {
	fields := ct.FieldNames()
	auth := make(httpAuth, 0, len(ct.HTTP.Auth))
	for i, b := range ct.HTTP.Auth {
		kind, ok := authtmpl.AuthTypeFor(b.Type)
		if !ok {
			return nil, fmt.Errorf("custom type %q auth binding %d: auth type %q is not registered", ct.Slug, i, b.Type)
		}
		binding := httpAuthBinding{kind: kind, name: b.Name}
		for _, src := range b.Values {
			tmpl, err := authtmpl.Parse(src, authtmpl.ParseOptions{Fields: fields, AllowRequest: true})
			if err != nil {
				return nil, fmt.Errorf("custom type %q auth binding %d: %w", ct.Slug, i, err)
			}
			binding.values = append(binding.values, tmpl)
		}
		auth = append(auth, binding)
	}
	return auth, nil
}

// apply 渲染并注入全部认证绑定。request.path 是实际发出的路径（含 Base URL 的前缀，不含 query）。
func (a httpAuth) apply(req *http.Request, values map[string]string, body []byte, now time.Time) error {
	rc := authtmpl.NewRenderContext(values,
		authtmpl.WithClock(func() time.Time { return now }),
		authtmpl.WithRequest(authtmpl.RequestInfo{Method: req.Method, Path: req.URL.Path, Body: body}))
	for i, b := range a {
		rendered := make([]string, 0, len(b.values))
		for _, tmpl := range b.values {
			v, err := tmpl.Render(rc)
			if err != nil {
				return fmt.Errorf("render auth binding %d: %w", i, err)
			}
			rendered = append(rendered, v)
		}
		if err := b.kind.Apply(req, b.name, rendered); err != nil {
			return fmt.Errorf("apply auth binding %d: %w", i, err)
		}
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func originOf(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

// redactURLError 去掉 *url.Error 里带出的完整请求 URL：注入的 query 认证就在那条 URL 里。
// 改用调用方视角的目标地址（Base URL + 调用方写的路径与 query）点名这次请求。
func redactURLError(err error, method, display string) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s %s: %w", method, display, ue.Err)
	}
	return err
}

func statusLine(resp *http.Response) string {
	return "HTTP " + resp.Status
}

// writeHeaders 按名称排序写出响应头，末尾空一行。
func writeHeaders(w io.Writer, h http.Header) error {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		for _, v := range h[name] {
			b.WriteString(name + ": " + v + "\n")
		}
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func streamHTTPMode(ctx context.Context, t *GenericTarget, argv []string, stdio permission.Stdio) (permission.StreamResult, error) {
	cmd, err := ParseHTTPCommand(argv)
	if err != nil {
		return permission.StreamResult{}, err
	}
	resp, err := sendGenericHTTP(ctx, t, cmd, &localInput{stdin: stdio.Stdin})
	if err != nil {
		return permission.StreamResult{}, err
	}
	defer resp.close()

	status := statusLine(resp.Response)
	result := permission.StreamResult{ExitCode: 1, AuditResult: status}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.ExitCode = 0
	}
	if _, err := io.WriteString(stdio.Stderr, status+"\n"); err != nil {
		return result, err
	}
	if cmd.Include {
		if _, err := io.WriteString(stdio.Stdout, status+"\n"); err != nil {
			return result, err
		}
		if err := writeHeaders(stdio.Stdout, resp.Header); err != nil {
			return result, err
		}
	}
	if _, err := io.Copy(stdio.Stdout, resp.Body); err != nil {
		return result, fmt.Errorf("read response body: %w", err)
	}
	return result, nil
}

// execHTTPForAI 给模型返回状态行加响应体；非文本响应只给摘要，不把二进制塞进模型上下文。
// 截断沿用 exec 结果的既有规则，这里不另设上限。审计摘要只是状态行（spec「策略、审批与
// 审计」：HTTP 记录方法、路径和状态码——方法与路径在审计 command 列）：响应体可能回显注入的
// 认证头，不得进入审计。
func execHTTPForAI(ctx context.Context, t *GenericTarget, command string) (string, string, error) {
	cmd, err := parseHTTPCommandLine(command)
	if err != nil {
		return "", "", err
	}
	resp, err := sendGenericHTTP(ctx, t, cmd, nil)
	if err != nil {
		return "", "", err
	}
	defer resp.close()

	status := statusLine(resp.Response)
	var b strings.Builder
	b.WriteString(status + "\n")
	if cmd.Include {
		if err := writeHeaders(&b, resp.Header); err != nil {
			return "", "", err
		}
	} else {
		b.WriteString("\n")
	}
	contentType := resp.Header.Get("Content-Type")
	if isTextContentType(contentType) {
		if _, err := io.Copy(&b, resp.Body); err != nil {
			return "", "", fmt.Errorf("read response body: %w", err)
		}
		return strings.TrimRight(b.String(), "\n"), status, nil
	}
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read response body: %w", err)
	}
	if n == 0 {
		return strings.TrimRight(b.String(), "\n"), status, nil
	}
	fmt.Fprintf(&b, "(binary response, %d bytes, %s)", n, contentType)
	return b.String(), status, nil
}

// isTextContentType：text/*、JSON（含 +json）、XML（含 +xml）按文本返回（spec「HTTP 请求」AI 一条）。
func isTextContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return strings.HasPrefix(mediaType, "text/") || strings.HasSuffix(mediaType, "json") || strings.HasSuffix(mediaType, "xml")
}
