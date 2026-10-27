package notify

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// HTTP 客户端测试: proxy 三态, 超时, 不跟随重定向, User-Agent
// 网络用例只使用 httptest 本地服务器, 不访问公网
//
// HTTP client tests: the three proxy modes, the timeout, no redirect following and the User-Agent
// Network cases use a local httptest server only and never reach the public internet
// ---------------------------------------------------------------------------

// TestNewClient_ProxyModes 覆盖 proxyMode 的三态
//
// TestNewClient_ProxyModes covers the three proxy modes
func TestNewClient_ProxyModes(t *testing.T) {
	client, err := NewClient("", time.Second, "example")
	if err != nil {
		t.Fatalf("NewClient(direct): %v", err)
	}
	transport, ok := client.Transport.(*userAgentTransport)
	if !ok {
		t.Fatalf("transport = %T, want *userAgentTransport", client.Transport)
	}
	direct, ok := transport.base.(*http.Transport)
	if !ok {
		t.Fatalf("base transport = %T, want *http.Transport", transport.base)
	}
	if direct.Proxy != nil {
		t.Error("an empty proxy mode kept a Proxy function, want a direct connection")
	}

	envClient, err := NewClient("env", time.Second, "example")
	if err != nil {
		t.Fatalf("NewClient(env): %v", err)
	}
	envTransport := envClient.Transport.(*userAgentTransport).base.(*http.Transport)
	if envTransport.Proxy == nil {
		t.Fatal("the env mode has no Proxy function")
	}
	if got, want := reflect.ValueOf(envTransport.Proxy).Pointer(), reflect.ValueOf(http.ProxyFromEnvironment).Pointer(); got != want {
		t.Error("the env mode does not use http.ProxyFromEnvironment")
	}

	explicitClient, err := NewClient("http://proxy.example:8080", time.Second, "example")
	if err != nil {
		t.Fatalf("NewClient(explicit): %v", err)
	}
	explicitTransport := explicitClient.Transport.(*userAgentTransport).base.(*http.Transport)
	if explicitTransport.Proxy == nil {
		t.Fatal("the explicit mode has no Proxy function")
	}
	proxied, err := explicitTransport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "target.example"}})
	if err != nil {
		t.Fatalf("explicit Proxy: %v", err)
	}
	if proxied == nil || proxied.Host != "proxy.example:8080" {
		t.Errorf("explicit proxy = %v, want proxy.example:8080", proxied)
	}
}

// TestNewClient_ProxyErrors 覆盖 socks5 与非法 scheme 的错误, 错误里不含凭据
//
// TestNewClient_ProxyErrors covers the socks5 and invalid-scheme errors, with no credential in the
// error
func TestNewClient_ProxyErrors(t *testing.T) {
	cases := []string{
		"socks5://example:secret@proxy.example:1080",
		"proxy.example:8080",
		"ftp://proxy.example:21",
	}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			client, err := NewClient(mode, time.Second, "example")
			if err == nil {
				t.Fatal("NewClient accepted an unsupported proxy mode")
			}
			if client != nil {
				t.Error("NewClient returned a client together with an error")
			}
			var prob *apperr.Problem
			if !errors.As(err, &prob) {
				t.Fatalf("error = %v, want *apperr.Problem", err)
			}
			if prob.ID != keyErrorClient {
				t.Errorf("ID = %q, want %q", prob.ID, keyErrorClient)
			}
			if detail, _ := prob.Data["Detail"].(string); detail == "" {
				t.Error("Detail is empty")
			}
			if strings.Contains(detail(err), "secret") {
				t.Error("the error echoed the proxy credential")
			}
		})
	}
}

// TestNewClient_MissingProxyHost 覆盖带 scheme 但没有 host 的代理地址
//
// TestNewClient_MissingProxyHost covers a proxy address that carries a scheme but no host
func TestNewClient_MissingProxyHost(t *testing.T) {
	_, err := NewClient("http://", time.Second, "example")
	if err == nil {
		t.Fatal("NewClient accepted a proxy without a host")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) || prob.ID != keyErrorClient {
		t.Fatalf("error = %v, want %s", err, keyErrorClient)
	}
}

// TestNewClient_Timeout 覆盖 timeout <= 0 报错, 正值写入 Client.Timeout
//
// TestNewClient_Timeout covers a timeout at or below zero reporting an error and a positive one
// reaching Client.Timeout
func TestNewClient_Timeout(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, err := NewClient("", timeout, "example"); err == nil {
			t.Errorf("NewClient accepted timeout %s", timeout)
		}
	}
	client, err := NewClient("", 3*time.Second, "example")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.Timeout != 3*time.Second {
		t.Errorf("Timeout = %s, want 3s", client.Timeout)
	}
}

// TestNewClient_UserAgent 覆盖 User-Agent 为 taskpush/<version>, 版本为空时用 dev,
// 渠道已设置该头时不被覆盖
//
// TestNewClient_UserAgent covers the User-Agent being taskpush/<version>, dev standing in for an
// empty version, and a channel-set header not being overwritten
func TestNewClient_UserAgent(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cases := []struct {
		name    string
		version string
		want    string
	}{
		{name: "version", version: "1.2.3", want: "taskpush/1.2.3"},
		{name: "empty version", version: "", want: "taskpush/dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient("", 5*time.Second, tc.version)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			resp, err := client.Get(server.URL)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			resp.Body.Close()
			if got != tc.want {
				t.Errorf("User-Agent = %q, want %q", got, tc.want)
			}
		})
	}

	client, err := NewClient("", 5*time.Second, "example")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("User-Agent", "channel/1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if got != "channel/1" {
		t.Errorf("User-Agent = %q, want the channel value to win", got)
	}
}

// TestNewClient_Redirect 覆盖 3xx 不跟随重定向, 返回原响应
//
// TestNewClient_Redirect covers a 3xx not being followed, returning the original response
func TestNewClient_Redirect(t *testing.T) {
	var targetHits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	client, err := NewClient("", 5*time.Second, "example")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := client.Get(redirect.URL)
	// CheckRedirect 返回 http.ErrUseLastResponse 时 client.Do 返回响应且 err 为 nil;
	// 若 err 非 nil, 只要响应带 302 也算命中不跟随语义
	//
	// With CheckRedirect returning http.ErrUseLastResponse, client.Do returns the response and a nil
	// error; a non-nil error still satisfies the no-follow semantics as long as the 302 came back
	if err != nil {
		if resp == nil {
			t.Fatalf("Get: %v", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("StatusCode = %d, want 302", resp.StatusCode)
	}
	if targetHits != 0 {
		t.Errorf("redirect target hits = %d, want 0", targetHits)
	}
}

// TestNewClient_CloneKeepsDefaultTransport 覆盖 transport 只换 Proxy, 默认调优保留
//
// TestNewClient_CloneKeepsDefaultTransport covers the transport replacing Proxy only and keeping the
// default tuning
func TestNewClient_CloneKeepsDefaultTransport(t *testing.T) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport is not *http.Transport")
	}
	client, err := NewClient("env", time.Second, "example")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	transport := client.Transport.(*userAgentTransport).base.(*http.Transport)
	if transport.MaxIdleConns != base.MaxIdleConns {
		t.Errorf("MaxIdleConns = %d, want the default %d", transport.MaxIdleConns, base.MaxIdleConns)
	}
	if transport.TLSHandshakeTimeout != base.TLSHandshakeTimeout {
		t.Errorf("TLSHandshakeTimeout = %s, want the default %s", transport.TLSHandshakeTimeout, base.TLSHandshakeTimeout)
	}
	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect is nil")
	}
	if err := client.CheckRedirect(&http.Request{}, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("CheckRedirect = %v, want http.ErrUseLastResponse", err)
	}
}

// detail 返回错误的可读文本, 供脱敏断言使用
//
// detail returns the readable text of an error, for the redaction assertion
func detail(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
