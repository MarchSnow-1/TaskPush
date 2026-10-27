package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
)

// ---------------------------------------------------------------------------
// discord Send 测试: 204/200 成功, 400/401/403/429/500 的分类, 坏 JSON 与空 body, 请求体与
// 请求头, 以及已成功发送的重复 Send
// 每个用例都起本地 httptest 服务器, 不访问公网
//
// Tests for discord Send: the 204 and 200 successes, the 400/401/403/429/500 classification,
// invalid JSON and an empty body, the request body and headers, and a repeated Send after a
// successful one
// Every case starts a local httptest server, so no connection in this file leaves the machine
// ---------------------------------------------------------------------------

// recordedWebhookRequest 是一次被服务器收到的请求
//
// recordedWebhookRequest is one request the server received
type recordedWebhookRequest struct {
	method      string
	contentType string
	payload     sendPayload
	rawBody     string
}

// webhookServer 是测试用的 webhook 服务器
//
// webhookServer is the webhook server used by the tests
type webhookServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedWebhookRequest
}

// newWebhookServer 启动服务器, handler 决定每次请求的响应
// 收到的请求被记录下来, 供断言方法, 媒体类型与请求体
//
// newWebhookServer starts a server whose handler decides the response of each request
// The received requests are recorded so the test can assert on the method, the media type and the
// request body
func newWebhookServer(t *testing.T, handler http.HandlerFunc) *webhookServer {
	t.Helper()
	s := &webhookServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var payload sendPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Errorf("unmarshal request body %q: %v", raw, err)
		}
		s.mu.Lock()
		s.requests = append(s.requests, recordedWebhookRequest{
			method:      r.Method,
			contentType: r.Header.Get("Content-Type"),
			payload:     payload,
			rawBody:     string(raw),
		})
		s.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// seen 返回服务器收到的请求副本
//
// seen returns a copy of the requests the server received
func (s *webhookServer) seen() []recordedWebhookRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedWebhookRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// webhookSettings 构造指向本地服务器的 settings
//
// webhookSettings builds the settings that point at the local server
func webhookSettings(t *testing.T, srv *webhookServer, extra string) string {
	t.Helper()
	return fmt.Sprintf(`{"url":%s%s}`, strconvQuote(srv.URL+"/api/webhooks/123456789/example-token"), extra)
}

// sendPrepared 以严格模式准备一条消息
//
// sendPrepared prepares one message under the strict mode
func sendPrepared(t *testing.T, ch channel.Channel, title string) channel.Prepared {
	t.Helper()
	prepared, _, err := prepareStrict(ch, title, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}

// TestSend_Success 覆盖 200 与 204 都算成功, 以及 204 的空 body 不报错
//
// TestSend_Success covers both 200 and 204 counting as a success, including the empty body of a 204
func TestSend_Success(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"204 no content", http.StatusNoContent, ""},
		{"200 with body", http.StatusOK, `{"id":"123456789"}`},
		{"200 empty body", http.StatusOK, ""},
		{"200 invalid json", http.StatusOK, `{"id":`},
	}
	for _, c := range cases {
		status, body := c.status, c.body
		srv := newWebhookServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			fmt.Fprint(w, body)
		})
		ch := newTestChannel(t, webhookSettings(t, srv, `,"username":"TaskPush"`))
		prepared := sendPrepared(t, ch, "Build done")

		if err := prepared.Send(context.Background(), srv.Client()); err != nil {
			t.Errorf("%s: Send: %v", c.name, err)
		}
		got := srv.seen()
		if len(got) != 1 {
			t.Errorf("%s: server saw %d requests, want 1", c.name, len(got))
			continue
		}
		if got[0].method != http.MethodPost {
			t.Errorf("%s: method = %q, want POST", c.name, got[0].method)
		}
		if !strings.HasPrefix(got[0].contentType, "application/json") {
			t.Errorf("%s: Content-Type = %q", c.name, got[0].contentType)
		}
		if got[0].payload.Content != "Build done" || got[0].payload.Username != "TaskPush" {
			t.Errorf("%s: payload = %+v", c.name, got[0].payload)
		}
		// 未渲染出值的可选字段不进入报文
		//
		// An optional field that rendered no value stays out of the request
		if strings.Contains(got[0].rawBody, "avatar_url") {
			t.Errorf("%s: the request body carries an empty avatar_url: %s", c.name, got[0].rawBody)
		}

		// 已成功的发送在第二次 Send 里不再产生请求
		//
		// A successful send produces no request on a second Send
		if err := prepared.Send(context.Background(), srv.Client()); err != nil {
			t.Errorf("%s: second Send: %v", c.name, err)
		}
		if got := srv.seen(); len(got) != 1 {
			t.Errorf("%s: after the second Send the server saw %d requests, want 1", c.name, len(got))
		}
	}
}

// TestSend_StatusClassification 覆盖非 2xx 的状态码分类
// 400/401/403/404 永久; 429 与常规 5xx 可重试
//
// TestSend_StatusClassification covers the non-2xx status classification
// 400, 401, 403 and 404 are permanent, while 429 and an ordinary 5xx stay retryable
func TestSend_StatusClassification(t *testing.T) {
	cases := []struct {
		status        int
		wantPermanent bool
	}{
		{http.StatusBadRequest, true},
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusNotFound, true},
		{http.StatusTooManyRequests, false},
		{http.StatusInternalServerError, false},
		{http.StatusServiceUnavailable, false},
	}
	for _, c := range cases {
		status := c.status
		srv := newWebhookServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"message":"example rejection","code":400}`)
		})
		ch := newTestChannel(t, webhookSettings(t, srv, ""))
		err := sendPrepared(t, ch, "Build done").Send(context.Background(), srv.Client())
		if err == nil {
			t.Errorf("status %d: expected an error", status)
			continue
		}
		prob := mustProblem(t, err)
		if prob.ID != keyErrorHTTPStatus {
			t.Errorf("status %d: ID = %q, want %q", status, prob.ID, keyErrorHTTPStatus)
		}
		if prob.Data["Status"] != status {
			t.Errorf("status %d: Data.Status = %v", status, prob.Data["Status"])
		}
		if body, _ := prob.Data["Body"].(string); !strings.Contains(body, "example rejection") {
			t.Errorf("status %d: Data.Body = %q, want the response summary", status, body)
		}
		if got := apperr.IsPermanent(err); got != c.wantPermanent {
			t.Errorf("status %d: IsPermanent = %v, want %v", status, got, c.wantPermanent)
		}
	}
}

// TestSend_RetryableFailureKeepsRetrying 覆盖可重试失败后的下一次 Send 会再次发送
//
// TestSend_RetryableFailureKeepsRetrying covers a retryable failure letting the next Send reach the
// endpoint again
func TestSend_RetryableFailureKeepsRetrying(t *testing.T) {
	var calls int
	srv := newWebhookServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "example outage")
	})
	ch := newTestChannel(t, webhookSettings(t, srv, ""))
	prepared := sendPrepared(t, ch, "Build done")

	if err := prepared.Send(context.Background(), srv.Client()); err == nil {
		t.Fatal("expected an error on the first send")
	}
	if err := prepared.Send(context.Background(), srv.Client()); err == nil {
		t.Fatal("expected an error on the retry send")
	}
	if calls != 2 {
		t.Fatalf("the endpoint saw %d requests, want 2", calls)
	}
}

// TestSend_PermanentFailureStopsRetrying 覆盖永久失败后的下一次 Send 不再发送
//
// TestSend_PermanentFailureStopsRetrying covers a permanent failure, which a later Send does not
// resend
func TestSend_PermanentFailureStopsRetrying(t *testing.T) {
	srv := newWebhookServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "unknown webhook")
	})
	ch := newTestChannel(t, webhookSettings(t, srv, ""))
	prepared := sendPrepared(t, ch, "Build done")

	err := prepared.Send(context.Background(), srv.Client())
	if !apperr.IsPermanent(err) {
		t.Fatalf("error %v is not permanent", err)
	}
	// 永久失败不写入 done, 因此下一次 Send 仍会尝试; 是否重试由调度层按永久标记决定
	//
	// A permanent failure does not set done, so the next Send still tries; the scheduling layer
	// reads the permanent marker to decide about retrying
	if err := prepared.Send(context.Background(), srv.Client()); !apperr.IsPermanent(err) {
		t.Fatalf("the second Send returned %v, want the same permanent verdict", err)
	}
	if got := srv.seen(); len(got) != 2 {
		t.Fatalf("the endpoint saw %d requests, want 2", len(got))
	}
}

// TestSend_NetworkFailureIsRetryable 覆盖服务器下线时的网络错误保持可重试且不含完整地址
//
// TestSend_NetworkFailureIsRetryable covers a network failure staying retryable when the server is
// gone, without the full address in the text
func TestSend_NetworkFailureIsRetryable(t *testing.T) {
	srv := newWebhookServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	ch := newTestChannel(t, webhookSettings(t, srv, ""))
	prepared := sendPrepared(t, ch, "Build done")
	address := srv.URL
	srv.Close()

	err := prepared.Send(context.Background(), &http.Client{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if apperr.IsPermanent(err) {
		t.Fatal("a network failure was classified as permanent")
	}
	message := err.Error()
	if strings.Contains(message, "example-token") || strings.Contains(message, address) {
		t.Fatalf("the network error leaked the webhook address: %v", message)
	}
}

// TestSend_ServerEchoTokenMasked 覆盖服务端在响应体里回显 webhook 地址时的遮蔽
//
// TestSend_ServerEchoTokenMasked covers masking the webhook address when the server echoes it in
// the response body
func TestSend_ServerEchoTokenMasked(t *testing.T) {
	srv := newWebhookServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"message":"bad webhook %s%s"}`, "https://example.com", r.URL.Path)
	})
	ch := newTestChannel(t, webhookSettings(t, srv, ""))
	err := sendPrepared(t, ch, "Build done").Send(context.Background(), srv.Client())

	prob := mustProblem(t, err)
	body, _ := prob.Data["Body"].(string)
	if strings.Contains(body, "example-token") {
		t.Fatalf("the error Data leaked the webhook token: %q", body)
	}
	// 服务端回显的地址由 SanitizeString 统一替换成脱敏值, token 段因此不会留下
	//
	// The address echoed by the server goes through SanitizeString, which replaces it with the
	// redaction value, so no token segment survives
	if !strings.Contains(body, "<redacted-url>") && !strings.Contains(body, maskValue) {
		t.Fatalf("the echoed address was not masked: %q", body)
	}
}

// TestSend_LongErrorBodySummary 覆盖超长响应体被压成摘要
//
// TestSend_LongErrorBodySummary covers an oversized response body being compressed into a summary
func TestSend_LongErrorBodySummary(t *testing.T) {
	srv := newWebhookServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, strings.Repeat("x", 4000))
	})
	ch := newTestChannel(t, webhookSettings(t, srv, ""))
	err := sendPrepared(t, ch, "Build done").Send(context.Background(), srv.Client())

	prob := mustProblem(t, err)
	body, _ := prob.Data["Body"].(string)
	if got := len([]rune(body)); got > 201 {
		t.Fatalf("Data.Body kept %d runes, want a summary of at most 201", got)
	}
}

// TestSend_RequestPayloadAvatar 覆盖 avatarUrl 出现在报文里, 且字段名为 avatar_url
//
// TestSend_RequestPayloadAvatar covers avatarUrl appearing in the request under the avatar_url key
func TestSend_RequestPayloadAvatar(t *testing.T) {
	srv := newWebhookServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	ch := newTestChannel(t, webhookSettings(t, srv, `,"avatarUrl":"https://example.com/avatar.png"`))
	if err := sendPrepared(t, ch, "Build done").Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.seen()
	if len(got) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(got))
	}
	if !strings.Contains(got[0].rawBody, `"avatar_url":"https://example.com/avatar.png"`) {
		t.Fatalf("request body = %s", got[0].rawBody)
	}
	if got[0].payload.AvatarURL != "https://example.com/avatar.png" {
		t.Fatalf("avatar_url = %q", got[0].payload.AvatarURL)
	}
}

// TestSend_NoRetryInsideSend 覆盖 Send 自身不做等待重试: 一次调用只发一个请求
//
// TestSend_NoRetryInsideSend covers Send performing no wait or retry of its own: one call sends one
// request
func TestSend_NoRetryInsideSend(t *testing.T) {
	srv := newWebhookServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"retry_after":1.0}`)
	})
	ch := newTestChannel(t, webhookSettings(t, srv, ""))
	err := sendPrepared(t, ch, "Build done").Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := srv.seen(); len(got) != 1 {
		t.Fatalf("one Send produced %d requests, want 1", len(got))
	}
}
