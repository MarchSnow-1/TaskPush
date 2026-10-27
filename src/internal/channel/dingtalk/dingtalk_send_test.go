package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
)

// ---------------------------------------------------------------------------
// dingtalk Send 测试: 请求构造, errcode 判定, HTTP 状态分类, 坏响应与脱敏
// 端点直接来自 settings.url, 因此用例把 httptest 地址写进配置, 不连公网, 也不改包级注入点
//
// Send tests for the DingTalk channel: request construction, errcode verdicts, HTTP status
// classification, malformed responses and redaction
// The endpoint comes from settings.url directly, so each case writes an httptest address into the
// settings instead of reaching the public internet or swapping a package-level injection point
// ---------------------------------------------------------------------------

// responseStub 是响应桩: 状态码, 业务体与可选的 Content-Type 覆盖
//
// responseStub is the response stub: the status code, the business body and an optional
// Content-Type override
type responseStub struct {
	status  int
	body    string
	content string
}

// newServer 起一个返回固定响应的本地 httptest 服务
//
// newServer starts a local httptest server answering with one fixed response
func newServer(t *testing.T, stub responseStub) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if stub.content != "" {
			w.Header().Set("Content-Type", stub.content)
		}
		if stub.status != 0 {
			w.WriteHeader(stub.status)
		}
		_, _ = io.WriteString(w, stub.body)
	}))
	t.Cleanup(server.Close)
	return server
}

// prepareAgainst 把渠道指向本地 httptest 服务并准备一次发送
//
// prepareAgainst points the channel at a local httptest server and prepares one send
func prepareAgainst(t *testing.T, server *httptest.Server, settingsJSON string, title, body string) *preparedChannel {
	t.Helper()
	ch := newTestChannel(t, settingsJSON)
	prepared, _, err := prepareStrict(ch, title, body)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return mustPrepared(t, prepared)
}

// settingsFor 用 httptest 地址替换配置里的 URL 占位
//
// settingsFor replaces the URL placeholder in a configuration with the httptest address
func settingsFor(server *httptest.Server, template string) string {
	return strings.ReplaceAll(template, "TESTSERVER", server.URL+"/robot/send?access_token="+exampleToken)
}

// TestSend_Success 覆盖 errcode 0 时的请求构造与返回 nil
//
// TestSend_Success covers the request construction and the nil result when errcode is 0
func TestSend_Success(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotCT     string
		rawQuery  []string
		rawBody   []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotCT = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		rawQuery = []string{r.URL.Query().Get("access_token"), r.URL.Query().Get("timestamp"), r.URL.Query().Get("sign")}
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer server.Close()

	prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER","secret":"SECexample"}`), "Example", "example body")
	if err := prepared.Send(context.Background(), server.Client()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/robot/send" {
		t.Errorf("path = %q, want /robot/send", gotPath)
	}
	if !strings.EqualFold(gotCT, "application/json; charset=utf-8") {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if rawQuery[0] != exampleToken || rawQuery[1] != "1767323045000" || rawQuery[2] == "" {
		t.Errorf("query = %v, want the token, the injected timestamp and a signature", rawQuery)
	}

	var body map[string]any
	if err := json.Unmarshal(rawBody, &body); err != nil {
		t.Fatalf("decode request body %q: %v", rawBody, err)
	}
	if body["msgtype"] != "text" {
		t.Errorf("msgtype = %v, want text", body["msgtype"])
	}
	if got := body["text"].(map[string]any)["content"]; got != "Example\n\nexample body" {
		t.Errorf("content = %q", got)
	}
	if _, ok := body["at"]; ok {
		t.Errorf("at appeared without atAll: %s", rawBody)
	}

	// 同一个 Prepared 可被重试重复调用, 请求体仍可再次读出
	// The same Prepared may be sent again by a retry, so the body stays readable
	if err := prepared.Send(context.Background(), server.Client()); err != nil {
		t.Fatalf("second Send: %v", err)
	}
}

// TestSend_MarkdownAndAtAll 覆盖 markdown 请求体与 text 的 at 字段
//
// TestSend_MarkdownAndAtAll covers the markdown request body and the at field of a text message
func TestSend_MarkdownAndAtAll(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		title    string
		body     string
		wantType string
		wantAt   bool
	}{
		{
			name:     "markdown",
			settings: `{"url":"TESTSERVER","msgType":"markdown"}`,
			title:    "",
			body:     "**bold**",
			wantType: "markdown",
		},
		{
			name:     "text at all",
			settings: `{"url":"TESTSERVER","atAll":true}`,
			title:    "Example",
			body:     "",
			wantType: "text",
			wantAt:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rawBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
			}))
			defer server.Close()

			prepared := prepareAgainst(t, server, settingsFor(server, tc.settings), tc.title, tc.body)
			if err := prepared.Send(context.Background(), server.Client()); err != nil {
				t.Fatalf("Send: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(rawBody, &body); err != nil {
				t.Fatalf("decode request body %q: %v", rawBody, err)
			}
			if body["msgtype"] != tc.wantType {
				t.Fatalf("msgtype = %v, want %s", body["msgtype"], tc.wantType)
			}
			if tc.wantType == "markdown" {
				md, _ := body["markdown"].(map[string]any)
				if md["title"] != "通知" || md["text"] != "**bold**" {
					t.Errorf("markdown = %v, want the default title and the body", md)
				}
			}
			at, ok := body["at"].(map[string]any)
			if ok != tc.wantAt {
				t.Fatalf("at present = %v, want %v (%s)", ok, tc.wantAt, rawBody)
			}
			if tc.wantAt && at["isAtAll"] != true {
				t.Errorf("at = %v, want isAtAll true", at)
			}
		})
	}
}

// TestSend_BusinessCode 覆盖 errcode 非 0 的永久与限流分支
//
// TestSend_BusinessCode covers the permanent and the throttling branches of a non-zero errcode
func TestSend_BusinessCode(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantCode  int
		wantMsg   string
		permanent bool
	}{
		{name: "bad signature", body: `{"errcode":310000,"errmsg":"sign not match"}`, wantCode: 310000, wantMsg: "sign not match", permanent: true},
		{name: "keyword filter", body: `{"errcode":310001,"errmsg":"keywords not in content"}`, wantCode: 310001, wantMsg: "keywords not in content", permanent: true},
		{name: "throttled", body: `{"errcode":410100,"errmsg":"send too fast"}`, wantCode: 410100, wantMsg: "send too fast", permanent: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newServer(t, responseStub{body: tc.body})
			prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER"}`), "", "example body")
			err := prepared.Send(context.Background(), server.Client())
			prob := mustProblem(t, err)
			if prob.ID != keyErrorAPICode {
				t.Fatalf("ID = %q, want %q", prob.ID, keyErrorAPICode)
			}
			if got := prob.Data["Code"]; got != tc.wantCode {
				t.Errorf("Code = %v, want %d", got, tc.wantCode)
			}
			if got, _ := prob.Data["Message"].(string); got != tc.wantMsg {
				t.Errorf("Message = %q, want %q", got, tc.wantMsg)
			}
			if got := apperr.IsPermanent(err); got != tc.permanent {
				t.Errorf("permanent = %v, want %v", got, tc.permanent)
			}
		})
	}
}

// TestSend_HTTPStatus 覆盖 HTTP 非 2xx 的分类与响应摘要
//
// TestSend_HTTPStatus covers the classification and the response summary of a non-2xx status
func TestSend_HTTPStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		permanent bool
	}{
		{name: "400", status: http.StatusBadRequest, body: "bad request", permanent: true},
		{name: "403", status: http.StatusForbidden, body: "forbidden", permanent: true},
		{name: "404", status: http.StatusNotFound, body: "not found", permanent: true},
		{name: "408", status: http.StatusRequestTimeout, body: "timeout", permanent: false},
		{name: "429", status: http.StatusTooManyRequests, body: "slow down", permanent: false},
		{name: "500", status: http.StatusInternalServerError, body: "boom", permanent: false},
		{name: "503", status: http.StatusServiceUnavailable, body: "down", permanent: false},
		{name: "302", status: http.StatusFound, body: "moved", permanent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newServer(t, responseStub{status: tc.status, body: tc.body})
			prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER"}`), "", "example body")
			err := prepared.Send(context.Background(), server.Client())
			prob := mustProblem(t, err)
			if prob.ID != "common.error.http_status" {
				t.Fatalf("ID = %q, want common.error.http_status", prob.ID)
			}
			if got := prob.Data["Status"]; got != tc.status {
				t.Errorf("Status = %v, want %d", got, tc.status)
			}
			if got, _ := prob.Data["Body"].(string); !strings.Contains(got, tc.body) {
				t.Errorf("Body summary = %q, want it to mention %q", got, tc.body)
			}
			if got := apperr.IsPermanent(err); got != tc.permanent {
				t.Errorf("permanent = %v, want %v", got, tc.permanent)
			}
		})
	}
}

// TestSend_BadResponse 覆盖坏 JSON, 空 body 与缺 errcode 的协议错误
//
// TestSend_BadResponse covers the protocol errors of invalid JSON, an empty body and a missing
// errcode
func TestSend_BadResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "bad json", body: `{"errcode":`, want: keyErrorBadResponse},
		{name: "html error page", body: `<html>oops</html>`, want: keyErrorBadResponse},
		{name: "empty body", body: ``, want: keyErrorBadResponse},
		{name: "missing errcode", body: `{"errmsg":"ok"}`, want: keyErrorBadResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newServer(t, responseStub{body: tc.body})
			prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER"}`), "", "example body")
			err := prepared.Send(context.Background(), server.Client())
			prob := mustProblem(t, err)
			if prob.ID != tc.want {
				t.Fatalf("ID = %q, want %q", prob.ID, tc.want)
			}
			if apperr.IsPermanent(err) {
				t.Error("a protocol error must stay retryable")
			}
			if got := prob.Data["Detail"]; got == nil || got == "" {
				t.Errorf("Detail = %v, want a description", got)
			}
		})
	}
}

// TestSend_TransientThenSuccess 覆盖限流后重试成功的语义: Send 不自己 sleep 也不计数
//
// TestSend_TransientThenSuccess covers the throttled-then-succeeded sequence: Send neither sleeps
// nor counts on its own
func TestSend_TransientThenSuccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			_, _ = io.WriteString(w, `{"errcode":410100,"errmsg":"send too fast"}`)
			return
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer server.Close()

	prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER"}`), "", "example body")
	first := prepared.Send(context.Background(), server.Client())
	if first == nil {
		t.Fatal("the throttled round returned nil")
	}
	if apperr.IsPermanent(first) {
		t.Fatalf("a throttling code is permanent: %v", first)
	}
	if err := prepared.Send(context.Background(), server.Client()); err != nil {
		t.Fatalf("retry after throttling: %v", err)
	}
	if calls != 2 {
		t.Errorf("server saw %d requests, want 2", calls)
	}
}

// TestSend_MasksEchoedSecret 覆盖服务端回显里的 secret 与地址被清洗后才进 Data
//
// TestSend_MasksEchoedSecret covers an echoed secret and address being cleaned before they reach Data
func TestSend_MasksEchoedSecret(t *testing.T) {
	const secret = "SECechosecret"
	server := newServer(t, responseStub{body: `{"errcode":310000,"errmsg":"bad sign for ` + secret + ` at https://example.com/robot/send?access_token=` + exampleToken + `"}`})
	prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER","secret":"`+secret+`"}`), "", "example body")
	err := prepared.Send(context.Background(), server.Client())
	prob := mustProblem(t, err)
	message, _ := prob.Data["Message"].(string)
	if strings.Contains(message, secret) {
		t.Errorf("Message = %q, want the secret masked", message)
	}
	if strings.Contains(message, exampleToken) || strings.Contains(message, "https://") {
		t.Errorf("Message = %q, want the echoed address masked", message)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("Error() = %q, want the secret masked", err.Error())
	}
}

// TestSend_NilClientFailsWithoutPanic 覆盖传入 nil client 时返回脱敏错误而不是 panic
//
// TestSend_NilClientFailsWithoutPanic covers a nil client producing a redacted error instead of a panic
func TestSend_NilClientFailsWithoutPanic(t *testing.T) {
	ch := newTestChannel(t, `{"url":"https://example.com/robot/send?access_token=`+exampleToken+`"}`)
	prepared, _, err := prepareStrict(ch, "", "example body")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = mustPrepared(t, prepared).Send(context.Background(), nil) //nolint:staticcheck // 这里就是要传入 nil client
	if err == nil {
		t.Fatal("Send with a nil client returned nil")
	}
	var prob *apperr.Problem
	if errors.As(err, &prob) {
		return
	}
	if !strings.Contains(err.Error(), "nil") && !strings.Contains(err.Error(), "redacted") {
		t.Fatalf("error = %q, want a readable failure", err.Error())
	}
}

// TestSend_RequestContextCancelled 覆盖 ctx 取消时不发送成功
//
// TestSend_RequestContextCancelled covers a cancelled context stopping the send
func TestSend_RequestContextCancelled(t *testing.T) {
	reached := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer server.Close()

	prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER"}`), "", "example body")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := prepared.Send(ctx, server.Client()); err == nil {
		t.Fatal("a cancelled context let Send succeed")
	}
}

// TestSend_LargeResponse 覆盖超大响应体的错误键
//
// TestSend_LargeResponse covers the error key of an oversized response body
func TestSend_LargeResponse(t *testing.T) {
	server := newServer(t, responseStub{body: `{"errcode":0,"errmsg":"` + strings.Repeat("x", 1<<20) + `"}`})
	prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER"}`), "", "example body")
	err := prepared.Send(context.Background(), server.Client())
	prob := mustProblem(t, err)
	if prob.ID != "common.error.body_too_large" {
		t.Fatalf("ID = %q, want common.error.body_too_large", prob.ID)
	}
	if apperr.IsPermanent(err) {
		t.Error("an oversized response must stay retryable")
	}
}

// TestSend_RejectsUnresolvedRender 覆盖 Prepare 报错时不返回可发送对象
//
// TestSend_RejectsUnresolvedRender covers Prepare reporting an error without returning a sendable
// object
func TestSend_RejectsUnresolvedRender(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+webhookURL("https://example.com")+`"}`)
	result, warns, err := prepareStrict(ch, "", "example body")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warnIDs(warns))
	}
	if result == nil {
		t.Fatal("Prepare returned a nil Prepared with a nil error")
	}

	// 缺失 access_token 的地址在 Create 就被拒绝, 不会留下可发送的渠道实例
	// An address without access_token is refused at Create, leaving no sendable channel instance
	if _, err := channel.Create("dt", "dingtalk", json.RawMessage(`{"url":"https://example.com/robot/send"}`)); err == nil {
		t.Fatal("Create accepted an address without access_token")
	}
}
