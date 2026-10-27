package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
)

// ---------------------------------------------------------------------------
// telegram Send 测试: 单 chat 成功, ok=false 的 400/429/500 分类, 多 chat 部分失败与只补失败
// 目标, 坏 JSON, 以及 HTTP 状态分支
// 每个用例都起本地 httptest 服务器, 不访问公网
//
// Tests for telegram Send: single-chat success, ok=false classification for 400/429/500, partial
// failure across chats with a failures-only retry, invalid JSON, and the HTTP status branches
// Every case starts a local httptest server, so no connection in this file leaves the machine
// ---------------------------------------------------------------------------

// recordedRequest 是一次被服务器收到的请求
//
// recordedRequest is one request the server received
type recordedRequest struct {
	chatID string
	text   string
	silent bool
}

// sendServer 是测试用的 sendMessage 服务器
// 每个 chat id 走 handler 决定的响应; 记录收到的请求供断言端点与请求体
//
// sendServer is the sendMessage server used by the tests
// Each chat id takes the response the handler decides, and the received requests are recorded so
// the test can assert on the endpoint and the body
type sendServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
	hits     map[string]int
}

// newSendServer 启动服务器, handler 按 chat id 返回状态码与响应体
//
// newSendServer starts a server whose handler returns a status code and a body per chat id
func newSendServer(t *testing.T, handler func(chatID string, hits int) (int, string)) *sendServer {
	t.Helper()
	s := &sendServer{hits: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/bot") || !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Errorf("path = %q, want /bot<token>/sendMessage", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q", ct)
		}
		var payload sendPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		s.mu.Lock()
		s.hits[payload.ChatID]++
		hits := s.hits[payload.ChatID]
		s.requests = append(s.requests, recordedRequest{
			chatID: payload.ChatID,
			text:   payload.Text,
			silent: payload.DisableNotification,
		})
		s.mu.Unlock()

		status, body := handler(payload.ChatID, hits)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

// chatsSeen 返回服务器收到的 chat id 顺序
//
// chatsSeen returns the chat ids the server received, in order
func (s *sendServer) chatsSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, r := range s.requests {
		out = append(out, r.chatID)
	}
	return out
}

// newSendChannel 构造使用本地服务器地址的渠道
//
// newSendChannel builds a channel pointing at the local server address
func newSendChannel(t *testing.T, apiBase string, chatIDs ...string) channel.Channel {
	t.Helper()
	settings := fmt.Sprintf(`{"token":"EXAMPLE","chatIds":%s,"apiBase":%q}`,
		mustJSONArray(chatIDs), apiBase)
	ch, err := channel.Create("tg", "telegram", json.RawMessage(settings))
	if err != nil {
		t.Fatalf("channel.Create: %v", err)
	}
	return ch
}

// mustJSONArray 把字符串切片序列化为 JSON 数组
//
// mustJSONArray serialises a string slice into a JSON array
func mustJSONArray(items []string) string {
	data, err := json.Marshal(items)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// sendPrepared 以严格模式准备一条消息
//
// sendPrepared prepares one message under the strict mode
func sendPrepared(t *testing.T, ch channel.Channel, text string) channel.Prepared {
	t.Helper()
	prepared, _, err := prepareStrict(ch, text, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}

// TestSend_SingleChatSuccess 覆盖单 chat 成功与端点/请求体
//
// TestSend_SingleChatSuccess covers a successful single-chat send together with the endpoint and
// the request body
func TestSend_SingleChatSuccess(t *testing.T) {
	srv := newSendServer(t, func(string, int) (int, string) {
		return http.StatusOK, `{"ok":true,"result":{"message_id":1}}`
	})
	settings := fmt.Sprintf(`{"token":"EXAMPLE","chatIds":["123456789"],"apiBase":%q,"silent":true}`, srv.URL)
	ch, err := channel.Create("tg", "telegram", json.RawMessage(settings))
	if err != nil {
		t.Fatalf("channel.Create: %v", err)
	}
	prepared := sendPrepared(t, ch, "Build done")

	if err := prepared.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.requests) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(srv.requests))
	}
	got := srv.requests[0]
	if got.chatID != "123456789" || got.text != "Build done" || !got.silent {
		t.Fatalf("request = %+v", got)
	}
	// 已成功的 chat 在第二次 Send 里不再出现
	//
	// A chat that succeeded is absent from the second Send
	if err := prepared.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if len(srv.requests) != 1 {
		t.Fatalf("after the second Send the server saw %d requests, want 1", len(srv.requests))
	}
}

// TestSend_RepeatedSendSkipsSuccess 覆盖重复 Send 不重发已成功的 chat
//
// TestSend_RepeatedSendSkipsSuccess covers a repeated Send leaving an already successful chat alone
func TestSend_RepeatedSendSkipsSuccess(t *testing.T) {
	srv := newSendServer(t, func(string, int) (int, string) {
		return http.StatusOK, `{"ok":true}`
	})
	ch := newSendChannel(t, srv.URL, "123456789")
	prepared := sendPrepared(t, ch, "Build done")

	for i := 0; i < 3; i++ {
		if err := prepared.Send(context.Background(), srv.Client()); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	if got := srv.chatsSeen(); len(got) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(got))
	}
}

// TestSend_ErrorCodeClassification 覆盖 ok=false 的 400/401/403/429/500 分类
//
// TestSend_ErrorCodeClassification covers the ok=false classification for 400/401/403/429/500
func TestSend_ErrorCodeClassification(t *testing.T) {
	cases := []struct {
		code          int
		wantPermanent bool
	}{
		{400, true},
		{401, true},
		{403, true},
		{429, false},
		{500, false},
		{502, false},
	}
	for _, c := range cases {
		code := c.code
		srv := newSendServer(t, func(string, int) (int, string) {
			return http.StatusOK, fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"example rejection"}`, code)
		})
		ch := newSendChannel(t, srv.URL, "123456789")
		prepared := sendPrepared(t, ch, "Build done")

		err := prepared.Send(context.Background(), srv.Client())
		if err == nil {
			t.Errorf("error_code %d: expected an error", code)
			continue
		}
		prob := mustProblem(t, err)
		if prob.ID != keyErrorAPIError {
			t.Errorf("error_code %d: ID = %q, want %q", code, prob.ID, keyErrorAPIError)
		}
		if prob.Data["Code"] != code {
			t.Errorf("error_code %d: Data.Code = %v", code, prob.Data["Code"])
		}
		if prob.Data["Description"] != "example rejection" {
			t.Errorf("error_code %d: Data.Description = %v", code, prob.Data["Description"])
		}
		if got := apperr.IsPermanent(err); got != c.wantPermanent {
			t.Errorf("error_code %d: IsPermanent = %v, want %v", code, got, c.wantPermanent)
		}
	}
}

// TestSend_OKFalseWithoutErrorCode 覆盖缺 error_code 时可重试
//
// TestSend_OKFalseWithoutErrorCode covers a missing error_code staying retryable
func TestSend_OKFalseWithoutErrorCode(t *testing.T) {
	srv := newSendServer(t, func(string, int) (int, string) {
		return http.StatusOK, `{"ok":false,"description":"example rejection"}`
	})
	ch := newSendChannel(t, srv.URL, "123456789")
	err := sendPrepared(t, ch, "Build done").Send(context.Background(), srv.Client())
	prob := mustProblem(t, err)
	if prob.Data["Code"] != 0 {
		t.Fatalf("Data.Code = %v, want 0", prob.Data["Code"])
	}
	if apperr.IsPermanent(err) {
		t.Fatal("a missing error_code was classified as permanent")
	}
}

// TestSend_HTTPStatusClassification 覆盖 HTTP 非 2xx 的状态码分类
//
// TestSend_HTTPStatusClassification covers the status classification of a non-2xx HTTP response
func TestSend_HTTPStatusClassification(t *testing.T) {
	cases := []struct {
		status        int
		wantPermanent bool
	}{
		{http.StatusBadRequest, true},
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusTooManyRequests, false},
		{http.StatusInternalServerError, false},
		{http.StatusServiceUnavailable, false},
	}
	for _, c := range cases {
		status := c.status
		srv := newSendServer(t, func(string, int) (int, string) {
			return status, `{"ok":false,"error_code":400,"description":"example"}`
		})
		ch := newSendChannel(t, srv.URL, "123456789")
		err := sendPrepared(t, ch, "Build done").Send(context.Background(), srv.Client())
		if err == nil {
			t.Errorf("status %d: expected an error", status)
			continue
		}
		if got := apperr.IsPermanent(err); got != c.wantPermanent {
			t.Errorf("status %d: IsPermanent = %v, want %v", status, got, c.wantPermanent)
		}
	}
}

// TestSend_BadResponse 覆盖坏 JSON, 空体与缺 ok 字段, 三者都按可重试处理
//
// TestSend_BadResponse covers invalid JSON, an empty body and a missing ok field, all three read as
// retryable
func TestSend_BadResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"invalid json", `{"ok":`},
		{"empty body", ""},
		{"missing ok", `{"result":{"message_id":1}}`},
	}
	for _, c := range cases {
		body := c.body
		srv := newSendServer(t, func(string, int) (int, string) {
			return http.StatusOK, body
		})
		ch := newSendChannel(t, srv.URL, "123456789")
		err := sendPrepared(t, ch, "Build done").Send(context.Background(), srv.Client())
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		prob := mustProblem(t, err)
		if prob.ID != keyErrorBadResponse {
			t.Errorf("%s: ID = %q, want %q", c.name, prob.ID, keyErrorBadResponse)
		}
		if apperr.IsPermanent(err) {
			t.Errorf("%s: a bad response body was classified as permanent", c.name)
		}
	}
}

// TestSend_MultiChatPartialFailureAndRetry 覆盖多 chat 部分失败与只补失败目标
//
// TestSend_MultiChatPartialFailureAndRetry covers a partial failure across chats and a retry that
// reaches only the failures
func TestSend_MultiChatPartialFailureAndRetry(t *testing.T) {
	// chat 1 与 3 成功; chat 2 先返回可重试的 500, 恢复后返回成功
	//
	// Chats 1 and 3 succeed, while chat 2 answers with the retryable 500 first and succeeds once it
	// recovers
	var recovered sync.Mutex
	recoveredChat2 := false
	setRecovered := func(v bool) {
		recovered.Lock()
		defer recovered.Unlock()
		recoveredChat2 = v
	}
	srv := newSendServer(t, func(chatID string, _ int) (int, string) {
		if chatID == "222" {
			recovered.Lock()
			ok := recoveredChat2
			recovered.Unlock()
			if !ok {
				return http.StatusOK, `{"ok":false,"error_code":500,"description":"example temporary"}`
			}
		}
		return http.StatusOK, `{"ok":true}`
	})
	ch := newSendChannel(t, srv.URL, "111", "222", "333")
	prepared := sendPrepared(t, ch, "Build done")

	err := prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("expected an error for the failing chat")
	}
	if apperr.IsPermanent(err) {
		t.Fatal("a mixed or transient failure was classified as permanent")
	}
	if got := srv.chatsSeen(); strings.Join(got, ",") != "111,222,333" {
		t.Fatalf("first round saw %v", got)
	}

	// 重试只应补发 chat 2
	//
	// The retry reaches chat 2 only
	err = prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("expected an error on the retry")
	}
	if got := srv.chatsSeen(); strings.Join(got, ",") != "111,222,333,222" {
		t.Fatalf("after retry the server saw %v", got)
	}

	// chat 2 恢复后重试成功, 且不再向其它 chat 发送
	//
	// Once chat 2 recovers the next retry succeeds without sending to any other chat
	setRecovered(true)
	if err := prepared.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("recovering Send: %v", err)
	}
	if got := srv.chatsSeen(); strings.Join(got, ",") != "111,222,333,222,222" {
		t.Fatalf("after recovery the server saw %v", got)
	}
	// 全部成功后再 Send 不发任何请求
	//
	// Once everything succeeded a further Send reaches no target at all
	if err := prepared.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Send after full success: %v", err)
	}
	if got := srv.chatsSeen(); len(got) != 5 {
		t.Fatalf("a fully successful Prepared sent again: %v", got)
	}
}

// TestSend_MultiChatAllPermanent 覆盖全部失败目标都永久时整体为永久
//
// TestSend_MultiChatAllPermanent covers every failing target being permanent, which makes the whole
// error permanent
func TestSend_MultiChatAllPermanent(t *testing.T) {
	srv := newSendServer(t, func(string, int) (int, string) {
		return http.StatusOK, `{"ok":false,"error_code":400,"description":"chat not found"}`
	})
	ch := newSendChannel(t, srv.URL, "111", "222")
	prepared := sendPrepared(t, ch, "Build done")

	err := prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !apperr.IsPermanent(err) {
		t.Fatal("an all-permanent batch was not classified as permanent")
	}
	// chat id 与 token 都不进聚合错误
	//
	// Neither a chat id nor the token enters the aggregate error
	message := fmt.Sprint(err)
	if strings.Contains(message, "111") || strings.Contains(message, "222") ||
		strings.Contains(message, "EXAMPLE") || strings.Contains(message, srv.URL) {
		t.Fatalf("the aggregate error leaked a target or a credential: %s", message)
	}
}

// TestSend_MixedPermanentAndTransient 覆盖混合失败批次不因内部永久标记被判永久
//
// TestSend_MixedPermanentAndTransient covers a mixed batch staying retryable even though one inner
// failure is permanent
func TestSend_MixedPermanentAndTransient(t *testing.T) {
	srv := newSendServer(t, func(chatID string, _ int) (int, string) {
		if chatID == "111" {
			return http.StatusOK, `{"ok":false,"error_code":400,"description":"chat not found"}`
		}
		return http.StatusOK, `{"ok":false,"error_code":500,"description":"example temporary"}`
	})
	ch := newSendChannel(t, srv.URL, "111", "222")
	prepared := sendPrepared(t, ch, "Build done")

	err := prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("expected an error")
	}
	if apperr.IsPermanent(err) {
		t.Fatal("a mixed batch was classified as permanent through an inner permanent failure")
	}
}

// TestSend_NetworkFailureIsRetryable 覆盖服务器下线时的网络错误保持可重试
//
// TestSend_NetworkFailureIsRetryable covers a network failure staying retryable when the server is
// gone
func TestSend_NetworkFailureIsRetryable(t *testing.T) {
	srv := newSendServer(t, func(string, int) (int, string) {
		return http.StatusOK, `{"ok":true}`
	})
	ch := newSendChannel(t, srv.URL, "123456789")
	prepared := sendPrepared(t, ch, "Build done")
	url := srv.URL
	srv.Close()

	err := prepared.Send(context.Background(), &http.Client{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if apperr.IsPermanent(err) {
		t.Fatal("a network failure was classified as permanent")
	}
	if strings.Contains(fmt.Sprint(err), "EXAMPLE") || strings.Contains(fmt.Sprint(err), url) {
		t.Fatalf("the network error leaked a credential or the full URL: %v", err)
	}
}

// TestSend_ServerTextTokenMasked 覆盖服务端回显 token 时在错误 Data 里被遮蔽
//
// TestSend_ServerTextTokenMasked covers a token echoed by the server being masked inside the error
// Data
func TestSend_ServerTextTokenMasked(t *testing.T) {
	const token = "123456:EXAMPLE-SECRET"
	srv := newSendServer(t, func(string, int) (int, string) {
		return http.StatusOK, fmt.Sprintf(`{"ok":false,"error_code":400,"description":"bad token %s"}`, token)
	})
	settings := fmt.Sprintf(`{"token":%q,"chatIds":["123456789"],"apiBase":%q}`, token, srv.URL)
	ch, err := channel.Create("tg", "telegram", json.RawMessage(settings))
	if err != nil {
		t.Fatalf("channel.Create: %v", err)
	}
	prepared := sendPrepared(t, ch, "Build done")

	sendErr := prepared.Send(context.Background(), srv.Client())
	prob := mustProblem(t, sendErr)
	if description, _ := prob.Data["Description"].(string); strings.Contains(description, token) {
		t.Fatalf("the error Data leaked the token: %q", description)
	}
	if !strings.Contains(prob.Data["Description"].(string), maskValue) {
		t.Fatalf("the echoed token was not masked: %q", prob.Data["Description"])
	}
}

// TestPreview_TargetsAndNoSecrets 覆盖预览的目标数与脱敏
//
// TestPreview_TargetsAndNoSecrets covers the preview target count and its redaction
func TestPreview_TargetsAndNoSecrets(t *testing.T) {
	ch := newTestChannel(t, `{"token":"EXAMPLE-SECRET","chatIds":["111","-100222"],"silent":true}`)
	prepared := sendPrepared(t, ch, "Build done with a very long body")

	preview := prepared.Preview()
	if preview.ID != keyPreviewMessage {
		t.Fatalf("preview ID = %q, want %q", preview.ID, keyPreviewMessage)
	}
	if preview.Data["Targets"] != 2 {
		t.Fatalf("preview Targets = %v, want 2", preview.Data["Targets"])
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	for _, secret := range []string{"EXAMPLE-SECRET", "111", "-100222", "Build done"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the preview leaked %q: %s", secret, encoded)
		}
	}
}
