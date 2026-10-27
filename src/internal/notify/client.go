package notify

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// HTTP 客户端构造: proxy 三态 (直连/env/显式), 超时, 不跟随重定向, 默认 User-Agent
// 每个渠道按生效的 timeout/proxy 各建一个 client, 相同配置可以复用
//
// Building the HTTP client: the three proxy modes (direct, environment, explicit), the timeout,
// no redirect following and the default User-Agent
// Every channel gets a client built from its effective timeout and proxy, and an identical
// configuration may be reused
// ---------------------------------------------------------------------------

// defaultClientVersion 是调用方没有给出有效版本号时 NewClient 使用的版本
// 一次 Send 的版本经 Options.Version 传入, 空串落到这里
//
// defaultClientVersion is the version NewClient uses when the caller provides no valid one
// The version of one send arrives through Options.Version, and an empty string falls back here
const defaultClientVersion = "dev"

// userAgentTransport 在请求未显式设置 User-Agent 时补上 taskpush/<version>
// 渠道已经设置该头时保持原值, 因为渠道可能按平台要求填自己的 UA
//
// userAgentTransport adds taskpush/<version> when a request carries no explicit User-Agent
// A channel that already set the header keeps its value, since a platform may require its own UA
type userAgentTransport struct {
	base http.RoundTripper
	ua   string
}

// RoundTrip 复制请求后补写 User-Agent, 不改写调用方的 *http.Request
// 复制的是浅拷贝, Body 与其它字段继续由原请求持有, 因此重试仍能读取同一 body
//
// RoundTrip copies the request and adds the User-Agent without rewriting the caller's *http.Request
// The copy is shallow, so Body and the other fields stay with the original request and a retry can
// still read the same body
func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		clone := req.Clone(req.Context())
		clone.Header.Set("User-Agent", t.ua)
		req = clone
	}
	return t.base.RoundTrip(req)
}

// NewClient 按 proxy 与 timeout 构造 HTTP 客户端
//   - proxyMode 为空: 直连, 不读环境代理
//   - "env": 使用 http.ProxyFromEnvironment
//   - http/https URL: 显式代理
//   - socks5 或其它 scheme: 报错, 因为本项目只依赖标准库且不实现 socks5 拨号
//
// timeout 必须大于 0; 重定向不跟随 (返回 http.ErrUseLastResponse), 避免鉴权头随跳转外泄、
// 也避免 POST 变 GET 后丢掉正文; User-Agent 为 taskpush/<version>, version 去除首尾空白后为空时用 dev
//
// NewClient builds an HTTP client from a proxy mode and a timeout
//   - an empty proxyMode means a direct connection that ignores the environment proxy
//   - "env" uses http.ProxyFromEnvironment
//   - an http or https URL is an explicit proxy
//   - socks5 or any other scheme is an error, since this project depends on the standard library
//     only and implements no socks5 dialing
//
// The timeout has to be above zero, redirects are not followed (http.ErrUseLastResponse comes
// back), which keeps an auth header from travelling with a redirect and keeps a POST turned into a
// GET from losing its body, and the User-Agent is taskpush/<version>, with dev standing in for a
// version that is empty once the surrounding whitespace is trimmed
func NewClient(proxyMode string, timeout time.Duration, version string) (*http.Client, error) {
	if timeout <= 0 {
		return nil, clientProblem(fmt.Sprintf("timeout must be above zero, got %s", timeout))
	}
	// 首尾空白在拼进头部之前去掉: 带头部里出现换行或多余空格会构造出畸形请求,
	// 而纯空白版本不携带任何信息, 与空串一样落到 dev
	//
	// The surrounding whitespace is dropped before the value reaches the header: a newline or a
	// stray space there builds a malformed request, and an all-whitespace version carries no
	// information and falls back to dev like an empty one
	version = strings.TrimSpace(version)
	if version == "" {
		version = defaultClientVersion
	}

	transport, err := proxyTransport(proxyMode)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     &userAgentTransport{base: transport, ua: "taskpush/" + version},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// proxyTransport 按 proxyMode 返回基础 transport, 只替换 Proxy, 其余默认调优保留
// 错误只描述 scheme 或缺失 host, 不回显带凭据的完整代理地址
//
// proxyTransport returns the base transport for a proxy mode, replacing Proxy while keeping the
// other default tuning in place
// An error names the scheme or the missing host only, so a proxy address holding credentials is not
// echoed
func proxyTransport(proxyMode string) (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()

	mode := strings.TrimSpace(proxyMode)
	switch {
	case mode == "":
		// 直连: Proxy 为 nil, 环境代理不参与
		//
		// Direct: Proxy stays nil and the environment proxy plays no part
		transport.Proxy = nil
	case mode == "env":
		transport.Proxy = http.ProxyFromEnvironment
	default:
		u, err := url.Parse(mode)
		if err != nil {
			return nil, clientProblem("invalid proxy url")
		}
		switch u.Scheme {
		case "http", "https":
		case "socks5":
			return nil, clientProblem("socks5 proxy is not supported, use an http or https proxy")
		default:
			// 只回显 scheme, 不含 user:pass; 漏写 scheme 的配置是最常见的代理填错
			//
			// Only the scheme is echoed, never user:pass, since a configuration that omits the
			// scheme is the most common proxy mistake
			return nil, clientProblem(fmt.Sprintf("invalid proxy scheme %q, expect http:// or https://", u.Scheme))
		}
		if u.Host == "" {
			return nil, clientProblem("invalid proxy url, missing host")
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return transport, nil
}

// clientProblem 构造 notify.error.client, Detail 只放技术细节, 不含凭据
//
// clientProblem builds notify.error.client with technical detail in Detail and no credentials
func clientProblem(detail string) *apperr.Problem {
	return &apperr.Problem{
		ID:   keyErrorClient,
		Data: map[string]any{"Detail": detail},
	}
}
