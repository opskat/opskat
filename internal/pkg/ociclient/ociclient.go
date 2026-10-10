// Package ociclient 是只依赖标准库的匿名 OCI distribution 拉取客户端：
// 按 registry 的 WWW-Authenticate 质询取匿名令牌，读取单层清单，下载唯一的层并校验 sha256。
package ociclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/pkg/netdial"
)

// 错误类别，调用方用 errors.Is 区分。
var (
	ErrNetwork  = errors.New("registry unreachable")      // 网络 / 镜像不可达
	ErrAuth     = errors.New("registry authentication")   // 认证质询或令牌获取失败
	ErrRegistry = errors.New("registry protocol")         // 清单缺失或不是单层产物等 registry 响应问题
	ErrSize     = errors.New("download exceeds max size") // 超出大小上限
	ErrDigest   = errors.New("sha256 mismatch")           // 摘要与索引不一致
)

// DigestError 携带 sha256 不符时的期望值与实际值；errors.Is(err, ErrDigest) 为真。
type DigestError struct {
	Expected string
	Actual   string
}

func (e *DigestError) Error() string {
	return fmt.Sprintf("%v: expected %s, got %s", ErrDigest, e.Expected, e.Actual)
}

func (e *DigestError) Is(target error) bool { return target == ErrDigest }

const manifestAccept = "application/vnd.oci.image.manifest.v1+json, " +
	"application/vnd.docker.distribution.manifest.v2+json"

var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         netdial.Default().DialContext,
		TLSHandshakeTimeout: 15 * time.Second,
	},
}

type manifest struct {
	Layers []struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"layers"`
}

// Registry 是拉取的 registry：Host 为主机名（可带端口），不含协议与路径。
// Prefix 是拼在仓库名前面的路径前缀，首尾不带斜杠：镜像站把上游 registry 放在
// 路径下（<镜像>/ghcr.io/<仓库>）时用，和 docker pull 里写在主机名后面的那一段
// 是同一个东西；空表示仓库直接在 Host 的根下。
// PlainHTTP 让这次拉取走明文 http，只给验证运行接本地模拟 registry 用；
// 调用方必须自己决定它，Host 里写的 "http://" 不会被当成协议。
type Registry struct {
	Host      string
	Prefix    string
	PlainHTTP bool
}

// stallTimeout 是一次拉取允许 registry 一个字节都不发的最长时间（等响应头、等下一段
// 正文都算）：卡住的镜像会让桌面端商店安装永远停在「下载中」——它没有别的取消途径。
// 只计沉默，不限总时长，慢而不断的下载不受影响。变量是为了测试能缩短它。
var stallTimeout = 60 * time.Second

// errStalled 是 registry 超过 stallTimeout 不发数据时取消拉取的原因，归为 ErrNetwork。
var errStalled = errors.New("registry stopped sending data")

// Pull 从 reg 匿名拉取 ref（<repository>:<tag>）的唯一一层到 dst，默认走 https；
// reg.Prefix 非空时取的是 <前缀>/<repository>。
// wantSHA256 是 zip 本身的摘要（来自已验签索引），下载超过 maxSize 或摘要不符时删除 dst 并返回对应类别的错误。
// registry 超过 stallTimeout 不发数据时以 ErrNetwork 失败；调用方取消时原样返回 ctx 错误。
// onProgress 可为 nil，参数为已下载 / 总大小。
func Pull(ctx context.Context, reg Registry, ref, wantSHA256 string, maxSize int64, dst string, onProgress func(done, total int64)) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := time.AfterFunc(stallTimeout, func() { cancel(errStalled) })
	defer watchdog.Stop()
	alive := func() { watchdog.Reset(stallTimeout) }

	err := pull(ctx, alive, reg, ref, wantSHA256, maxSize, dst, onProgress)
	if err != nil && ctx.Err() != nil {
		// 无论哪一步先察觉到取消（发请求、读正文、解码清单），都按取消的原因报告。
		return classify(ctx, err)
	}
	return err
}

func pull(ctx context.Context, alive func(), reg Registry, ref, wantSHA256 string, maxSize int64, dst string, onProgress func(done, total int64)) error {
	host, proto := reg.Host, "https"
	if reg.PlainHTTP {
		proto = "http"
	}
	if host == "" || strings.ContainsAny(host, "/\\ @?#") {
		return fmt.Errorf("invalid registry host %q", host)
	}
	i := strings.LastIndex(ref, ":")
	if i <= 0 || i == len(ref)-1 || strings.Contains(ref[i+1:], "/") {
		return fmt.Errorf("invalid reference %q, want <repository>:<tag>", ref)
	}
	repo, tag := ref[:i], ref[i+1:]
	if p := reg.Prefix; p != "" {
		if strings.ContainsAny(p, "\\ @?#") || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
			return fmt.Errorf("invalid registry prefix %q", p)
		}
		repo = p + "/" + repo
	}
	base := proto + "://" + host + "/v2/" + repo

	c := &session{ctx: ctx, alive: alive}
	resp, err := c.get(base+"/manifests/"+tag, manifestAccept)
	if err != nil {
		return err
	}
	var m manifest
	err = decodeManifest(resp, &m)
	if err != nil {
		return err
	}
	if len(m.Layers) != 1 {
		return fmt.Errorf("%w: manifest has %d layers, want 1", ErrRegistry, len(m.Layers))
	}
	layer := m.Layers[0]
	if layer.Size > maxSize {
		return fmt.Errorf("%w: layer is %d bytes, limit %d", ErrSize, layer.Size, maxSize)
	}

	resp, err = c.get(base+"/blobs/"+layer.Digest, "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: blob returned %s", ErrRegistry, resp.Status)
	}
	total := layer.Size
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	if total > maxSize {
		return fmt.Errorf("%w: blob is %d bytes, limit %d", ErrSize, total, maxSize)
	}
	return save(ctx, alive, resp.Body, dst, wantSHA256, maxSize, total, onProgress)
}

func decodeManifest(resp *http.Response, m *manifest) error {
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: manifest returned %s", ErrRegistry, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(m); err != nil {
		return fmt.Errorf("%w: invalid manifest: %v", ErrRegistry, err)
	}
	return nil
}

// save 把 r 写入 dst，边写边算 sha256，每收到数据就调用 alive；任何失败都删除 dst。
func save(ctx context.Context, alive func(), r io.Reader, dst, want string, maxSize, total int64, onProgress func(done, total int64)) (err error) {
	f, err := os.Create(dst) //nolint:gosec // dst is chosen by the caller
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var done int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			alive()
			done += int64(n)
			if done > maxSize {
				return fmt.Errorf("%w: download exceeds limit %d", ErrSize, maxSize)
			}
			h.Write(buf[:n])
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			if onProgress != nil {
				onProgress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return classify(ctx, rerr)
		}
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return &DigestError{Expected: want, Actual: got}
	}
	return nil
}

// session 在一次拉取内保存质询得到的令牌；每发一个请求就调用 alive，等响应的时间
// 从这里起算。
type session struct {
	ctx   context.Context
	alive func()
	token string
}

// get 发 GET；遇 401 Bearer 质询时取匿名令牌并重试一次。
func (s *session) get(u, accept string) (*http.Response, error) {
	resp, err := s.do(u, accept)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	_ = resp.Body.Close()
	if s.token != "" {
		return nil, fmt.Errorf("%w: token rejected by registry", ErrAuth)
	}
	if s.token, err = s.fetchToken(challenge); err != nil {
		return nil, err
	}
	resp, err = s.do(u, accept)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: registry rejected anonymous token", ErrAuth)
	}
	return resp, err
}

func (s *session) do(u, accept string) (*http.Response, error) {
	return s.request(u, accept, s.token)
}

func (s *session) request(u, accept, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRegistry, err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	s.alive()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, classify(s.ctx, err)
	}
	return resp, nil
}

func (s *session) fetchToken(challenge string) (string, error) {
	authScheme, params, ok := parseChallenge(challenge)
	if !ok || !strings.EqualFold(authScheme, "Bearer") || params["realm"] == "" {
		return "", fmt.Errorf("%w: unsupported challenge %q", ErrAuth, challenge)
	}
	tu, err := url.Parse(params["realm"])
	if err != nil {
		return "", fmt.Errorf("%w: bad realm: %v", ErrAuth, err)
	}
	q := tu.Query()
	for _, k := range []string{"service", "scope"} {
		if v := params[k]; v != "" {
			q.Set(k, v)
		}
	}
	tu.RawQuery = q.Encode()
	resp, err := s.request(tu.String(), "", "")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: token endpoint returned %s", ErrAuth, resp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("%w: invalid token response: %v", ErrAuth, err)
	}
	tok := body.Token
	if tok == "" {
		tok = body.AccessToken
	}
	if tok == "" {
		return "", fmt.Errorf("%w: token response has no token", ErrAuth)
	}
	return tok, nil
}

// classify 把传输层错误归为 ErrNetwork；调用方取消 / 超时原样返回 ctx 错误，
// registry 卡住（errStalled）归为 ErrNetwork。
func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		if cause := context.Cause(ctx); errors.Is(cause, errStalled) {
			return fmt.Errorf("%w: %v", ErrNetwork, cause)
		}
		return ctx.Err()
	}
	return fmt.Errorf("%w: %v", ErrNetwork, err)
}

// parseChallenge 解析 `Bearer realm="...",service="...",scope="..."`。
func parseChallenge(h string) (scheme string, params map[string]string, ok bool) {
	scheme, rest, found := strings.Cut(strings.TrimSpace(h), " ")
	if !found {
		return "", nil, false
	}
	params = map[string]string{}
	for rest = strings.TrimSpace(rest); rest != ""; {
		k, after, found := strings.Cut(rest, "=")
		if !found {
			break
		}
		k = strings.ToLower(strings.TrimSpace(k))
		after = strings.TrimSpace(after)
		var v string
		if strings.HasPrefix(after, `"`) {
			end := strings.Index(after[1:], `"`)
			if end < 0 {
				break
			}
			v, rest = after[1:end+1], after[end+2:]
		} else {
			v, rest, _ = strings.Cut(after, ",")
			rest = "," + rest
		}
		params[k] = v
		rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ","))
	}
	return scheme, params, true
}
