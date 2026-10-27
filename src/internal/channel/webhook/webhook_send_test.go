package webhook

/*
webhook 渠道的发送行为测试
全部用例使用 httptest 本地服务器, 不连公网; 覆盖 200/204/301/400/408/429/500/501 与网络错误

Tests for the send behaviour of the webhook channel
Every case uses a local httptest server rather than the public network, covering 200, 204, 301, 400,
408, 429, 500 and 501 plus a network error
*/

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

// sendWebhook 用给定配置与状态码起本地服务并发送一次
//
// sendWebhook starts a local server with the given settings and status code and sends once
func sendWebhook(t *testing.T, raw string, status int) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
	}))
	defer server.Close()

	prepared, _, err := mustPrepare(t, raw, server.URL)
	if err != nil {
		return err
	}
	return prepared.Send(context.Background(), server.Client())
}

// mustPrepare 用本地服务地址替换模板占位后准备渠道
// 配置里的 {{url}} 在测试变量里给出本地地址, 避免把 httptest 地址直接拼进 JSON 文本
//
// mustPrepare replaces the template placeholder with the local server address before preparing
// The {{url}} variable carries the local address in the test, which keeps the httptest address out
// of the JSON text
func mustPrepare(t *testing.T, raw, serverURL string) (channel.Prepared, []apperr.Problem, error) {
	t.Helper()
	raw = strings.ReplaceAll(raw, "{{server}}", serverURL)
	ch, err := (factory{}).Create("example", json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return ch.Prepare(context.Background(), channel.Message{})
}

// TestSend_Status 覆盖 200/204 成功与 301/400/408/429/500/501 的分类
//
// TestSend_Status covers 200 and 204 as success plus the classification of 301, 400, 408, 429, 500
// and 501
func TestSend_Status(t *testing.T) {
	cases := []struct {
		status    int
		wantErr   bool
		permanent bool
	}{
		{status: http.StatusOK},
		{status: http.StatusNoContent},
		{status: http.StatusMovedPermanently, wantErr: true, permanent: true},
		{status: http.StatusBadRequest, wantErr: true, permanent: true},
		{status: http.StatusRequestTimeout, wantErr: true, permanent: false},
		{status: http.StatusTooManyRequests, wantErr: true, permanent: false},
		{status: http.StatusInternalServerError, wantErr: true, permanent: false},
		{status: http.StatusNotImplemented, wantErr: true, permanent: true},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			err := sendWebhook(t, `{"url":"{{server}}/hook","body":"abc"}`, tc.status)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Send error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Send returned nil error, want a failure")
			}
			if got := apperr.IsPermanent(err); got != tc.permanent {
				t.Fatalf("permanent = %v, want %v (err: %v)", got, tc.permanent, err)
			}
		})
	}
}

// TestSend_RequestShape 覆盖服务端收到的 method, headers 与正文
//
// TestSend_RequestShape covers the method, headers and body the server receives
func TestSend_RequestShape(t *testing.T) {
	var gotMethod, gotHeader, gotBody, gotHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Example")
		gotHost = r.Host
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	raw := `{"url":"` + server.URL + `/hook","method":"put","headers":{"X-Example":"value","Host":"virtual.example.com"},"body":{"format":"json","json":{"text":"hello"}}}`
	ch, err := (factory{}).Create("example", json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := prepared.Send(context.Background(), server.Client()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("method = %q, want PUT", gotMethod)
	}
	if gotHeader != "value" {
		t.Fatalf("X-Example = %q", gotHeader)
	}
	if gotHost != "virtual.example.com" {
		t.Fatalf("Host = %q, want virtual.example.com", gotHost)
	}
	if gotBody != `{"text":"hello"}` {
		t.Fatalf("body = %q", gotBody)
	}
}

// TestSend_NetworkError 覆盖连接失败时的可重试错误
//
// TestSend_NetworkError covers the retryable error of a failed connection
func TestSend_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	ch, err := (factory{}).Create("example", json.RawMessage(`{"url":"`+url+`/hook","body":"abc"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = prepared.Send(context.Background(), &http.Client{})
	if err == nil {
		t.Fatal("Send returned nil error, want a connection failure")
	}
	if apperr.IsPermanent(err) {
		t.Fatal("a connection failure was marked permanent")
	}
	if !strings.Contains(err.Error(), "<redacted-url>") {
		t.Fatalf("network error %q did not redact the URL", err)
	}
}

// TestSend_ResponseBodySummary 覆盖非 2xx 响应体摘要进入 Data
//
// TestSend_ResponseBodySummary covers the non-2xx response body summary reaching Data
func TestSend_ResponseBodySummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request detail"))
	}))
	defer server.Close()

	ch, err := (factory{}).Create("example", json.RawMessage(`{"url":"`+server.URL+`/hook","body":"abc"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = prepared.Send(context.Background(), server.Client())
	if err == nil {
		t.Fatal("Send returned nil error, want a failure")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	if prob.ID != "common.error.http_status" || prob.Data["Status"] != http.StatusBadRequest {
		t.Fatalf("Problem = %+v", prob)
	}
	if !strings.Contains(prob.Data["Body"].(string), "bad request detail") {
		t.Fatalf("Data[Body] = %v", prob.Data["Body"])
	}
}

// TestSend_NoRetry 覆盖 Send 不自行重试: 一次调用只发出一个请求
//
// TestSend_NoRetry covers Send performing no retry of its own: one call produces one request
func TestSend_NoRetry(t *testing.T) {
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ch, err := (factory{}).Create("example", json.RawMessage(`{"url":"`+server.URL+`/hook","body":"abc"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := prepared.Send(context.Background(), server.Client()); err == nil {
		t.Fatal("Send returned nil error, want a failure")
	}
	if count != 1 {
		t.Fatalf("server saw %d requests, want 1", count)
	}
}
