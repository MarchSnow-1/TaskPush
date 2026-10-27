package common

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// http.go 的单元测试: 全部走 httptest 本地服务器 (127.0.0.1), 不访问公网
// 数据一律用 example 占位, 断言覆盖状态码分类、业务码、坏 JSON、空 body、1MB 边界与 header 处理
//
// Unit tests for http.go: they run against a local httptest server on 127.0.0.1 only
// Fixtures use example placeholders, and the assertions cover status classification, business
// codes, bad JSON, empty bodies, the 1MB boundary and header handling
// ---------------------------------------------------------------------------

// statusBodyServer 起一个固定状态码与响应体的服务器
// 204 按协议不能带 body, 所以跳过写体, 免得 net/http 记录一条无意义的告警
//
// statusBodyServer starts a server with a fixed status code and body
// A 204 cannot carry a body by protocol, so the write is skipped to keep net/http from logging a
// pointless warning
func statusBodyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if status != http.StatusNoContent && body != "" {
			_, _ = io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// noRedirectClient 复制 srv 的客户端并禁掉重定向跟随, 与 core.BuildClient 的口径一致
// 不禁的话 3xx 会被客户端吞掉, Check2xx 就收不到 3xx, 301/302 分支也就测不到了
//
// noRedirectClient copies the client of srv with redirect following disabled, matching
// core.BuildClient
// Left enabled, the client swallows a 3xx, so Check2xx never sees one and the 301/302 branches
// go untested
func noRedirectClient(srv *httptest.Server) *http.Client {
	clone := *srv.Client()
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &clone
}

// get 向服务器发一个 GET, 并把响应体注册进 cleanup
// Check2xx 与 Verify* 都不关闭 body, 关闭责任在调用方, 测试这里就按调用方做法来
//
// get sends a GET to the server and registers the body for cleanup
// Check2xx and the Verify* helpers leave the body open because closing is the caller's job, so
// the test behaves like a caller
func get(t *testing.T, srv *httptest.Server) *http.Response {
	t.Helper()
	resp, err := DoRequest(context.Background(), noRedirectClient(srv), mustNewRequest(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// mustNewRequest 构造请求, 失败即终止测试
//
// mustNewRequest builds a request and stops the test on failure
func mustNewRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return req
}

// asProblem 取出错误链上的 *apperr.Problem
//
// asProblem pulls the *apperr.Problem out of the error chain
func asProblem(t *testing.T, err error) *apperr.Problem {
	t.Helper()
	if err == nil {
		t.Fatalf("want a Problem, got nil")
	}
	var p *apperr.Problem
	if !errors.As(err, &p) {
		t.Fatalf("want *apperr.Problem, got %T: %v", err, err)
	}
	return p
}

// wantDataString 读取 Problem.Data 里的字符串字段
//
// wantDataString reads a string field from Problem.Data
func wantDataString(t *testing.T, p *apperr.Problem, key string) string {
	t.Helper()
	v, ok := p.Data[key]
	if !ok {
		t.Fatalf("Data missing key %q: %#v", key, p.Data)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("Data[%q] is %T, want string", key, v)
	}
	return s
}

// TestDoJSONDefaultContentType 验证默认 Content-Type 与请求体序列化
//
// TestDoJSONDefaultContentType checks the default Content-Type and payload serialization
func TestDoJSONDefaultContentType(t *testing.T) {
	var gotCT, gotAuth, gotBody string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	resp, err := DoJSON(context.Background(), srv.Client(), http.MethodPost, srv.URL,
		map[string]string{"title": "example"}, map[string]string{"Authorization": "Bearer example-token"})
	if err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	defer resp.Body.Close()

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotCT != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the default JSON type", gotCT)
	}
	if gotAuth != "Bearer example-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotBody != `{"title":"example"}` {
		t.Errorf("body = %q", gotBody)
	}
}

// TestDoJSONContentTypeOverride 验证 headers 里已有的 Content-Type 不被默认值覆盖, 且键名大小写不敏感
//
// TestDoJSONContentTypeOverride checks that a Content-Type already in headers survives the
// default, with the key matched case-insensitively
func TestDoJSONContentTypeOverride(t *testing.T) {
	var gotCT, gotCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotCustom = r.Header.Get("X-Example")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := DoJSON(context.Background(), srv.Client(), http.MethodPost, srv.URL,
		map[string]string{"title": "example"},
		map[string]string{"content-type": "application/vnd.example+json", "x-example": "example"})
	if err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	defer resp.Body.Close()

	if gotCT != "application/vnd.example+json" {
		t.Errorf("Content-Type = %q, want the caller value kept", gotCT)
	}
	if gotCustom != "example" {
		t.Errorf("X-Example = %q", gotCustom)
	}
}

// TestDoJSONNilHeaders 验证 headers 传 nil 时仍然补默认 Content-Type 且不 panic
//
// TestDoJSONNilHeaders checks that nil headers still get the default Content-Type and no panic
func TestDoJSONNilHeaders(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := DoJSON(context.Background(), srv.Client(), http.MethodPost, srv.URL, map[string]string{}, nil)
	if err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	defer resp.Body.Close()
	if gotCT != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", gotCT)
	}
}

// TestDoJSONMarshalError 验证不可序列化 payload 在发请求之前返回错误
//
// TestDoJSONMarshalError checks that an unserializable payload fails before any request goes out
func TestDoJSONMarshalError(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := DoJSON(context.Background(), srv.Client(), http.MethodPost, srv.URL, make(chan int), nil); err == nil {
		t.Fatalf("want a marshal error, got nil")
	}
	if hits != 0 {
		t.Errorf("server got %d requests, want 0: the payload must fail before sending", hits)
	}
}

// TestDoJSONContextCanceled 验证 ctx 取消时错误能穿透 SanitizeError 被 errors.Is 认出
//
// TestDoJSONContextCanceled checks that a canceled ctx still reaches errors.Is through
// SanitizeError
func TestDoJSONContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err := DoJSON(ctx, srv.Client(), http.MethodPost, srv.URL, map[string]string{"title": "example"}, nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("want a canceled-context error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false for %v", err)
	}
	if resp != nil {
		t.Errorf("want nil response on error")
	}
}

// failTransport 总是返回给定错误, 用来稳定复现连接层失败
//
// failTransport always answers with the given error, which reproduces a transport failure
// deterministically
type failTransport struct{ err error }

func (f failTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

// TestDoRequestSanitizesError 验证传输层错误文本里的 URL 被脱敏, 而 errors.Is 仍可穿透原始错误
//
// TestDoRequestSanitizesError checks that a URL inside a transport error text is masked while
// errors.Is still reaches the original error
func TestDoRequestSanitizesError(t *testing.T) {
	sentinel := errors.New("dial failed")
	client := &http.Client{Transport: failTransport{err: sentinel}}

	req := mustNewRequest(t, http.MethodGet, "http://127.0.0.1:1/send?token=secret-example", nil)
	resp, err := DoRequest(context.Background(), client, req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("want a transport error, got nil")
	}
	if resp != nil {
		t.Errorf("want nil response on error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is(err, sentinel) = false: %v", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "secret-example") {
		t.Errorf("error text leaks the token: %q", msg)
	}
	if strings.Contains(msg, "http://127.0.0.1:1") {
		t.Errorf("error text leaks the URL: %q", msg)
	}
	if !strings.Contains(msg, "redacted") {
		t.Errorf("error text has no redaction marker: %q", msg)
	}
}

// TestCheck2xxClasses 覆盖状态码分类: 2xx 通过, 3xx/多数 4xx/501/505/511 走永久,
// 408/425/429 与其余 5xx 保持普通错误
//
// TestCheck2xxClasses covers the classification: 2xx passes, 3xx, most 4xx and 501/505/511 are
// marked permanent, while 408/425/429 and the remaining 5xx stay plain errors
func TestCheck2xxClasses(t *testing.T) {
	cases := []struct {
		status    int
		wantError bool
		wantPerm  bool
	}{
		{http.StatusOK, false, false},
		{http.StatusCreated, false, false},
		{http.StatusNoContent, false, false},
		{http.StatusMovedPermanently, true, true},
		{http.StatusFound, true, true},
		{http.StatusBadRequest, true, true},
		{http.StatusUnauthorized, true, true},
		{http.StatusForbidden, true, true},
		{http.StatusNotFound, true, true},
		{http.StatusRequestTimeout, true, false},
		{http.StatusTooEarly, true, false},
		{http.StatusTooManyRequests, true, false},
		{http.StatusInternalServerError, true, false},
		{http.StatusBadGateway, true, false},
		{http.StatusServiceUnavailable, true, false},
		{http.StatusGatewayTimeout, true, false},
		{http.StatusNotImplemented, true, true},
		{http.StatusHTTPVersionNotSupported, true, true},
		{http.StatusNetworkAuthenticationRequired, true, true},
	}
	for _, tc := range cases {
		srv := statusBodyServer(t, tc.status, `{"message":"example"}`)
		resp := get(t, srv)
		err := Check2xx(resp)
		if !tc.wantError {
			if err != nil {
				t.Errorf("status %d: Check2xx = %v, want nil", tc.status, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("status %d: Check2xx = nil, want an error", tc.status)
			continue
		}
		p := asProblem(t, err)
		if p.ID != "common.error.http_status" {
			t.Errorf("status %d: Problem ID = %q, want common.error.http_status", tc.status, p.ID)
		}
		if got, ok := p.Data["Status"].(int); !ok || got != tc.status {
			t.Errorf("status %d: Data.Status = %#v", tc.status, p.Data["Status"])
		}
		if apperr.IsPermanent(err) != tc.wantPerm {
			t.Errorf("status %d: IsPermanent = %v, want %v", tc.status, apperr.IsPermanent(err), tc.wantPerm)
		}
	}
}

// TestCheck2xxBodySummary 验证摘要长度上限与内容
//
// TestCheck2xxBodySummary checks the summary cap and its content
func TestCheck2xxBodySummary(t *testing.T) {
	long := strings.Repeat("例", 500)
	srv := statusBodyServer(t, http.StatusBadRequest, long)
	resp := get(t, srv)
	err := Check2xx(resp)
	p := asProblem(t, err)
	summary := wantDataString(t, p, "Body")
	if r := []rune(summary); len(r) > bodySummaryLimit+1 {
		t.Errorf("summary runes = %d, want at most %d (limit plus ellipsis)", len(r), bodySummaryLimit+1)
	}
	if summary == "" {
		t.Errorf("summary is empty, want the body text")
	}

	srv2 := statusBodyServer(t, http.StatusInternalServerError, "short error page")
	resp2 := get(t, srv2)
	p2 := asProblem(t, Check2xx(resp2))
	if got := wantDataString(t, p2, "Body"); got != "short error page" {
		t.Errorf("summary = %q, want the trimmed body", got)
	}
}

// TestCheck2xxEmptyBody 验证空响应体时状态码错误仍然返回, 摘要为空
//
// TestCheck2xxEmptyBody checks that the status error is still returned for an empty body, with an
// empty summary
func TestCheck2xxEmptyBody(t *testing.T) {
	srv := statusBodyServer(t, http.StatusNotFound, "")
	resp := get(t, srv)
	p := asProblem(t, Check2xx(resp))
	if p.ID != "common.error.http_status" {
		t.Errorf("Problem ID = %q", p.ID)
	}
	if got := wantDataString(t, p, "Body"); got != "" {
		t.Errorf("summary = %q, want empty", got)
	}
}

// TestCheck2xxBodyURLRedacted 验证服务端回显的带凭据 URL 不会原样进 Data
//
// TestCheck2xxBodyURLRedacted checks that a credential-carrying URL echoed by the server does not
// land in Data as it came
func TestCheck2xxBodyURLRedacted(t *testing.T) {
	body := `{"error":"bad url http://127.0.0.1:1/hooks/example?token=secret-example"}`
	srv := statusBodyServer(t, http.StatusBadRequest, body)
	resp := get(t, srv)
	p := asProblem(t, Check2xx(resp))
	summary := wantDataString(t, p, "Body")
	if strings.Contains(summary, "secret-example") {
		t.Errorf("summary leaks the token: %q", summary)
	}
}

// trackingBody 记录 Body 是否被关闭, 用来验证 ReadBody 不越权关闭
//
// trackingBody records whether the body was closed, which is how the test proves ReadBody leaves
// that to the caller
type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

// TestReadBodyLimitBoundary 验证 1MB 恰好通过, 超过 1MB 报 body_too_large
//
// TestReadBodyLimitBoundary checks that exactly 1MB passes and anything larger reports
// body_too_large
func TestReadBodyLimitBoundary(t *testing.T) {
	cases := []struct {
		name      string
		size      int
		wantError bool
	}{
		{"exactly one megabyte", maxHTTPBodyBytes, false},
		{"one byte over", maxHTTPBodyBytes + 1, true},
		{"far over", maxHTTPBodyBytes + 4096, true},
	}
	for _, tc := range cases {
		srv := statusBodyServer(t, http.StatusOK, strings.Repeat("a", tc.size))
		resp := get(t, srv)
		data, err := ReadBody(resp)
		if tc.wantError {
			if err == nil {
				t.Errorf("%s: ReadBody = nil error, want body_too_large", tc.name)
				continue
			}
			p := asProblem(t, err)
			if p.ID != "common.error.body_too_large" {
				t.Errorf("%s: Problem ID = %q", tc.name, p.ID)
			}
			if got, ok := p.Data["Limit"].(int); !ok || got != maxHTTPBodyBytes {
				t.Errorf("%s: Data.Limit = %#v, want %d", tc.name, p.Data["Limit"], maxHTTPBodyBytes)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: ReadBody: %v", tc.name, err)
			continue
		}
		if len(data) != tc.size {
			t.Errorf("%s: read %d bytes, want %d", tc.name, len(data), tc.size)
		}
	}
}

// TestReadBodyKeepsBodyOpen 验证 ReadBody 不关闭响应体
//
// TestReadBodyKeepsBodyOpen checks that ReadBody leaves the response body open
func TestReadBodyKeepsBodyOpen(t *testing.T) {
	track := &trackingBody{Reader: strings.NewReader("example")}
	resp := &http.Response{Body: track}
	if _, err := ReadBody(resp); err != nil {
		t.Fatalf("ReadBody: %v", err)
	}
	if track.closed {
		t.Errorf("ReadBody closed the body, which is the caller's job")
	}
}

// TestReadBodyNilBody 验证手工构造的无 body 响应不会 panic
//
// TestReadBodyNilBody checks that a hand-built response without a body does not panic
func TestReadBodyNilBody(t *testing.T) {
	data, err := ReadBody(&http.Response{})
	if err != nil {
		t.Fatalf("ReadBody: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("read %d bytes, want 0", len(data))
	}
}

// TestVerifyCodeOK 验证业务码相符时通过, 并且不关闭 body
//
// TestVerifyCodeOK checks that a matching business code passes and the body stays open
func TestVerifyCodeOK(t *testing.T) {
	srv := statusBodyServer(t, http.StatusOK, `{"code":0,"message":"ok example"}`)
	resp := get(t, srv)
	if err := VerifyCode(resp, 0); err != nil {
		t.Errorf("VerifyCode: %v", err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Errorf("body still readable after VerifyCode: %v", err)
	}
}

// TestVerifyCodeMismatch 验证业务码不符走 business_code, 默认判永久, 限流码放行重试
//
// TestVerifyCodeMismatch checks that a mismatching code goes through business_code, permanent by
// default, with a throttling code left retryable
func TestVerifyCodeMismatch(t *testing.T) {
	cases := []struct {
		code     int
		body     string
		wantPerm bool
		wantMsg  string
	}{
		{43000, `{"code":43000,"message":"rejected"}`, true, "rejected"},
		{45009, `{"code":45009,"message":"too many"}`, false, "too many"},
		{40000, `{"code":40000}`, true, ""},
	}
	for _, tc := range cases {
		srv := statusBodyServer(t, http.StatusOK, tc.body)
		resp := get(t, srv)
		err := VerifyCode(resp, 0)
		if err == nil {
			t.Fatalf("code %d: VerifyCode = nil, want an error", tc.code)
		}
		p := asProblem(t, err)
		if p.ID != "common.error.business_code" {
			t.Errorf("code %d: Problem ID = %q", tc.code, p.ID)
		}
		if got, ok := p.Data["Code"].(int); !ok || got != tc.code {
			t.Errorf("code %d: Data.Code = %#v", tc.code, p.Data["Code"])
		}
		if got := wantDataString(t, p, "Message"); got != tc.wantMsg {
			t.Errorf("code %d: Data.Message = %q, want %q", tc.code, got, tc.wantMsg)
		}
		if apperr.IsPermanent(err) != tc.wantPerm {
			t.Errorf("code %d: IsPermanent = %v, want %v", tc.code, apperr.IsPermanent(err), tc.wantPerm)
		}
	}
}

// TestVerifyCodeBadJSON 验证坏 JSON 报 bad_json 且保持可重试
//
// TestVerifyCodeBadJSON checks that invalid JSON reports bad_json and stays retryable
func TestVerifyCodeBadJSON(t *testing.T) {
	srv := statusBodyServer(t, http.StatusOK, `<html>not json</html>`)
	resp := get(t, srv)
	err := VerifyCode(resp, 0)
	p := asProblem(t, err)
	if p.ID != "common.error.bad_json" {
		t.Errorf("Problem ID = %q, want common.error.bad_json", p.ID)
	}
	if wantDataString(t, p, "Field") != "code" {
		t.Errorf("Data.Field = %q, want code", p.Data["Field"])
	}
	if wantDataString(t, p, "Detail") == "" {
		t.Errorf("Data.Detail is empty")
	}
	if apperr.IsPermanent(err) {
		t.Errorf("bad JSON is marked permanent, want retryable")
	}
}

// TestVerifyCodeEmptyBody 验证 200 + 空 body 报 bad_response 而不是误判成功
//
// TestVerifyCodeEmptyBody checks that 200 with an empty body reports bad_response instead of a
// false success
func TestVerifyCodeEmptyBody(t *testing.T) {
	srv := statusBodyServer(t, http.StatusOK, "")
	resp := get(t, srv)
	err := VerifyCode(resp, 0)
	p := asProblem(t, err)
	if p.ID != "common.error.bad_response" {
		t.Errorf("Problem ID = %q, want common.error.bad_response", p.ID)
	}
	if wantDataString(t, p, "Field") != "code" {
		t.Errorf("Data.Field = %q", p.Data["Field"])
	}
}

// TestVerifyCodeMissingField 验证合法 JSON 但缺 code 字段时不被零值误判成成功
//
// TestVerifyCodeMissingField checks that valid JSON without a code field is not read as a
// successful zero
func TestVerifyCodeMissingField(t *testing.T) {
	srv := statusBodyServer(t, http.StatusOK, `{"message":"example"}`)
	resp := get(t, srv)
	p := asProblem(t, VerifyCode(resp, 0))
	if p.ID != "common.error.bad_response" {
		t.Errorf("Problem ID = %q, want common.error.bad_response", p.ID)
	}
	if wantDataString(t, p, "Field") != "code" {
		t.Errorf("Data.Field = %q", p.Data["Field"])
	}
}

// TestVerifyCodeNon2xx 验证非 2xx 时短路返回 http_status, 不做业务码判定
//
// TestVerifyCodeNon2xx checks that a non-2xx short-circuits to http_status without a business
// code verdict
func TestVerifyCodeNon2xx(t *testing.T) {
	srv := statusBodyServer(t, http.StatusServiceUnavailable, `{"code":503,"message":"down"}`)
	resp := get(t, srv)
	err := VerifyCode(resp, 0)
	p := asProblem(t, err)
	if p.ID != "common.error.http_status" {
		t.Errorf("Problem ID = %q, want common.error.http_status", p.ID)
	}
	if apperr.IsPermanent(err) {
		t.Errorf("503 is marked permanent, want retryable")
	}
}

// TestVerifyCodeBodyTooLarge 验证超大响应体按 body_too_large 报告, 由调用方决定是否忽略
//
// TestVerifyCodeBodyTooLarge checks that an oversized body reports body_too_large, leaving the
// verdict to the caller
func TestVerifyCodeBodyTooLarge(t *testing.T) {
	srv := statusBodyServer(t, http.StatusOK, `{"code":0,"pad":"`+strings.Repeat("a", maxHTTPBodyBytes)+`"}`)
	resp := get(t, srv)
	p := asProblem(t, VerifyCode(resp, 0))
	if p.ID != "common.error.body_too_large" {
		t.Errorf("Problem ID = %q, want common.error.body_too_large", p.ID)
	}
}

// TestVerifyErrcode 覆盖 errcode 字段的成功、不符、缺字段与限流分类
//
// TestVerifyErrcode covers the errcode field: success, mismatch, missing field and the throttling
// classification
func TestVerifyErrcode(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantErr   string
		wantPerm  bool
		wantCode  int
		wantField string
	}{
		{name: "match", body: `{"errcode":0,"errmsg":"ok"}`, wantErr: ""},
		{name: "invalid key", body: `{"errcode":93000,"errmsg":"invalid key"}`, wantErr: "common.error.business_code", wantPerm: true, wantCode: 93000},
		{name: "throttled", body: `{"errcode":410100,"errmsg":"send too fast"}`, wantErr: "common.error.business_code", wantPerm: false, wantCode: 410100},
		{name: "missing field", body: `{"errmsg":"example"}`, wantErr: "common.error.bad_response", wantField: "errcode"},
		{name: "bad json", body: `not-json`, wantErr: "common.error.bad_json", wantField: "errcode"},
		{name: "empty body", body: ``, wantErr: "common.error.bad_response", wantField: "errcode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := statusBodyServer(t, http.StatusOK, tc.body)
			resp := get(t, srv)
			err := VerifyErrcode(resp, 0)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("VerifyErrcode = %v, want nil", err)
				}
				return
			}
			p := asProblem(t, err)
			if p.ID != tc.wantErr {
				t.Fatalf("Problem ID = %q, want %q", p.ID, tc.wantErr)
			}
			if apperr.IsPermanent(err) != tc.wantPerm {
				t.Errorf("IsPermanent = %v, want %v", apperr.IsPermanent(err), tc.wantPerm)
			}
			if tc.wantCode != 0 {
				if got, ok := p.Data["Code"].(int); !ok || got != tc.wantCode {
					t.Errorf("Data.Code = %#v, want %d", p.Data["Code"], tc.wantCode)
				}
			}
			if tc.wantField != "" && wantDataString(t, p, "Field") != tc.wantField {
				t.Errorf("Data.Field = %q, want %q", p.Data["Field"], tc.wantField)
			}
		})
	}
}

// TestVerifyErrcodeMessagePriority 验证 errmsg 优先作为业务码文案
//
// TestVerifyErrcodeMessagePriority checks that errmsg wins as the business code message
func TestVerifyErrcodeMessagePriority(t *testing.T) {
	srv := statusBodyServer(t, http.StatusOK, `{"errcode":45009,"errmsg":"freq out of limit"}`)
	resp := get(t, srv)
	p := asProblem(t, VerifyErrcode(resp, 0))
	if got := wantDataString(t, p, "Message"); got != "freq out of limit" {
		t.Errorf("Data.Message = %q", got)
	}
}

// TestVerifyOKPass 覆盖 ok:true / 204 / 空 body 三条成功路径
// VerifyOK 对 204 与空 body 返回 nil, 空体语义交给调用方判断
//
// TestVerifyOKPass covers ok:true, 204 and an empty body: VerifyOK returns nil for 204 and an
// empty body because the meaning of an empty payload belongs to the caller
func TestVerifyOKPass(t *testing.T) {
	srv := statusBodyServer(t, http.StatusOK, `{"ok":true,"result":{"message_id":1}}`)
	if err := VerifyOK(get(t, srv)); err != nil {
		t.Errorf("VerifyOK ok=true: %v", err)
	}

	srv204 := statusBodyServer(t, http.StatusNoContent, "")
	if err := VerifyOK(get(t, srv204)); err != nil {
		t.Errorf("VerifyOK 204: %v", err)
	}

	srvEmpty := statusBodyServer(t, http.StatusOK, "   ")
	if err := VerifyOK(get(t, srvEmpty)); err != nil {
		t.Errorf("VerifyOK empty body: %v", err)
	}
}

// TestVerifyOKFail 覆盖 ok=false 的分类: error_code 缺省或限流区间可重试, 其余判永久
//
// TestVerifyOKFail covers the ok=false classification: an absent error_code and codes in the
// rate-limit region stay retryable, the rest are marked permanent
func TestVerifyOKFail(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantPerm bool
		wantCode int
		wantMsg  string
	}{
		{name: "no error code", body: `{"ok":false}`, wantPerm: false, wantCode: 0, wantMsg: "ok=false"},
		{name: "unauthorized", body: `{"ok":false,"error_code":401,"description":"Unauthorized"}`, wantPerm: true, wantCode: 401, wantMsg: "Unauthorized"},
		{name: "bad request", body: `{"ok":false,"error_code":400,"description":"bad chat"}`, wantPerm: true, wantCode: 400, wantMsg: "bad chat"},
		{name: "flood", body: `{"ok":false,"error_code":429,"description":"Too Many Requests"}`, wantPerm: false, wantCode: 429, wantMsg: "Too Many Requests"},
		{name: "server side", body: `{"ok":false,"error_code":500,"description":"server error"}`, wantPerm: false, wantCode: 500, wantMsg: "server error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := statusBodyServer(t, http.StatusOK, tc.body)
			err := VerifyOK(get(t, srv))
			p := asProblem(t, err)
			if p.ID != "common.error.business_code" {
				t.Fatalf("Problem ID = %q, want common.error.business_code", p.ID)
			}
			if got, ok := p.Data["Code"].(int); !ok || got != tc.wantCode {
				t.Errorf("Data.Code = %#v, want %d", p.Data["Code"], tc.wantCode)
			}
			if got := wantDataString(t, p, "Message"); got != tc.wantMsg {
				t.Errorf("Data.Message = %q, want %q", got, tc.wantMsg)
			}
			if apperr.IsPermanent(err) != tc.wantPerm {
				t.Errorf("IsPermanent = %v, want %v", apperr.IsPermanent(err), tc.wantPerm)
			}
		})
	}
}

// TestVerifyOKBadShapes 覆盖缺 ok 字段、坏 JSON 与非 2xx
//
// TestVerifyOKBadShapes covers a missing ok field, invalid JSON and a non-2xx status
func TestVerifyOKBadShapes(t *testing.T) {
	srvMissing := statusBodyServer(t, http.StatusOK, `{"result":{}}`)
	p := asProblem(t, VerifyOK(get(t, srvMissing)))
	if p.ID != "common.error.bad_response" || wantDataString(t, p, "Field") != "ok" {
		t.Errorf("missing field: ID = %q, Field = %#v", p.ID, p.Data["Field"])
	}

	srvBad := statusBodyServer(t, http.StatusOK, `{"ok":`)
	p = asProblem(t, VerifyOK(get(t, srvBad)))
	if p.ID != "common.error.bad_json" {
		t.Errorf("bad json: ID = %q", p.ID)
	}

	srvStatus := statusBodyServer(t, http.StatusNotFound, `{"ok":true}`)
	err := VerifyOK(get(t, srvStatus))
	p = asProblem(t, err)
	if p.ID != "common.error.http_status" {
		t.Errorf("404 with ok=true: ID = %q, want the status error", p.ID)
	}
	if !apperr.IsPermanent(err) {
		t.Errorf("404 is not marked permanent")
	}
}

// TestCheck2xxLeavesBodyOpenForCaller 验证 Check2xx 读完 body 后不关闭它
//
// TestCheck2xxLeavesBodyOpenForCaller checks that Check2xx reads the body without closing it
func TestCheck2xxLeavesBodyOpenForCaller(t *testing.T) {
	track := &trackingBody{Reader: strings.NewReader("example page")}
	resp := &http.Response{StatusCode: http.StatusBadRequest, Body: track}
	if err := Check2xx(resp); err == nil {
		t.Fatalf("want an error for 400")
	}
	if track.closed {
		t.Errorf("Check2xx closed the body, which is the caller's job")
	}
}

// TestProblemDataHasNoSecrets 汇总校验: 错误 Data 里没有 token 与完整 URL
//
// TestProblemDataHasNoSecrets is a combined check that no error Data holds a token or a full URL
func TestProblemDataHasNoSecrets(t *testing.T) {
	const secret = "secret-example"
	body := `{"code":1,"message":"http://127.0.0.1:1/push?token=` + secret + `"}`

	srv := statusBodyServer(t, http.StatusOK, body)
	p := asProblem(t, VerifyCode(get(t, srv), 0))
	for k, v := range p.Data {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if strings.Contains(s, secret) {
			t.Errorf("Data[%q] leaks the token: %q", k, s)
		}
		if strings.Contains(s, "http://127.0.0.1:1") {
			t.Errorf("Data[%q] leaks the URL: %q", k, s)
		}
	}
}
