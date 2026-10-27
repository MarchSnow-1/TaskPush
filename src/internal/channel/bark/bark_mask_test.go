package bark

/*
bark 渠道在状态码错误上遮蔽服务端回显凭据的测试
单设备发送把 token 放在 path 里, 批量发送把 device key 放在请求体里, 两条路径都必须遮蔽
common.Check2xx 的 Body 摘要只按 scheme://... 形态打码, 裸凭据会原样留下

Tests for masking the credentials a server echoes back on a status-code error in the bark channel
A single-device send keeps the token in the path while a batch send keeps the device keys in the body,
and both paths have to mask
The Body summary of common.Check2xx masks the scheme://... form alone, which leaves a bare credential
as it is
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
)

// statusErrorBody 取 Send 返回的 Problem 里的响应体摘要, 顺带断言 ID 与 4xx 的永久性
//
// statusErrorBody returns the response-body summary of the Problem Send returned and asserts the ID
// and the permanence of a 4xx along the way
func statusErrorBody(t *testing.T, err error, wantStatus int) string {
	t.Helper()
	if err == nil {
		t.Fatal("Send returned nil error, want a status-code failure")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	if prob.ID != "common.error.http_status" {
		t.Fatalf("ID = %q, want common.error.http_status", prob.ID)
	}
	if got := prob.Data["Status"]; got != wantStatus {
		t.Fatalf("Data[Status] = %v, want %d", got, wantStatus)
	}
	if !apperr.IsPermanent(err) {
		t.Fatalf("a %d was not marked permanent", wantStatus)
	}
	if prob.Cause != nil {
		t.Fatalf("Cause = %v, want nil", prob.Cause)
	}
	body, _ := prob.Data["Body"].(string)
	return body
}

// TestSend_SingleMasksEchoedToken 覆盖单设备发送: token 在 path 里, 服务端把它回显进 400 响应体
// 遮蔽由 maskStatusError 完成, 回显的 token 不留在 Data["Body"]
//
// TestSend_SingleMasksEchoedToken covers a single-device send, where the token sits in the path and
// the server echoes it into a 400 body
// maskStatusError does the masking, so the echoed token does not stay in Data["Body"]
func TestSend_SingleMasksEchoedToken(t *testing.T) {
	const token = "example-path-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
		// 把 path 末尾的 token 原样回显, 模拟服务端把收到的凭据写进错误体
		//
		// The token at the end of the path is echoed as it arrived, which stands for a server writing
		// the credential it received into the error body
		_, _ = io.WriteString(w, "rejected token "+r.URL.Path)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"token":"`+token+`","server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	body := statusErrorBody(t, prepared.Send(context.Background(), srv.Client()), http.StatusBadRequest)
	if strings.Contains(body, token) {
		t.Fatalf("Data[Body] = %q, want the echoed token masked", body)
	}
	if !strings.Contains(body, maskValue) {
		t.Fatalf("Data[Body] = %q, want it to hold the mask", body)
	}
}

// TestSend_BatchMasksEchoedDeviceKeys 覆盖批量发送: device key 在请求体里, 服务端逐个回显
// 遮蔽由 maskStatusError 完成, 回显的 device key 不留在 Data["Body"]
//
// TestSend_BatchMasksEchoedDeviceKeys covers a batch send, where the device keys sit in the request
// body and the server echoes them one by one
// maskStatusError does the masking, so the echoed device keys do not stay in Data["Body"]
func TestSend_BatchMasksEchoedDeviceKeys(t *testing.T) {
	const keyOne = "example-device-one"
	const keyTwo = "example-device-two"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload struct {
			DeviceKeys []string `json:"device_keys"`
		}
		_ = json.Unmarshal(raw, &payload)
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "unknown devices: "+strings.Join(payload.DeviceKeys, ", "))
	}))
	defer srv.Close()

	ch := newChannel(t, `{"tokens":["`+keyOne+`","`+keyTwo+`"],"server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	body := statusErrorBody(t, prepared.Send(context.Background(), srv.Client()), http.StatusForbidden)
	for _, key := range []string{keyOne, keyTwo} {
		if strings.Contains(body, key) {
			t.Fatalf("Data[Body] = %q, want the echoed device key masked", body)
		}
	}
	if !strings.Contains(body, maskValue) {
		t.Fatalf("Data[Body] = %q, want it to hold the mask", body)
	}
}

// TestSend_StatusErrorKeepsBodySummary 验证遮蔽之后不是把摘要清空, 其余文本仍然保留
//
// TestSend_StatusErrorKeepsBodySummary checks that masking does not empty the summary, which keeps
// the rest of the text readable
func TestSend_StatusErrorKeepsBodySummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "bad request detail")
	}))
	defer srv.Close()

	ch := newChannel(t, `{"token":"example-token","server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	body := statusErrorBody(t, prepared.Send(context.Background(), srv.Client()), http.StatusBadRequest)
	if !strings.Contains(body, "bad request detail") {
		t.Fatalf("Data[Body] = %q, want the server detail kept", body)
	}
}

// TestSend_RetryableStatusStaysRetryable 验证 5xx 与 429 经重包后仍可重试
//
// TestSend_RetryableStatusStaysRetryable checks that a 5xx and a 429 stay retryable after the re-wrap
func TestSend_RetryableStatusStaysRetryable(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, "retry later")
		}))

		ch := newChannel(t, `{"token":"example-token","server":"`+srv.URL+`"}`)
		prepared, _ := prepare(t, ch, "example-title", "")
		err := prepared.Send(context.Background(), srv.Client())
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: Send returned nil error, want a failure", status)
		}
		if apperr.IsPermanent(err) {
			t.Fatalf("status %d: marked permanent, want retryable", status)
		}
		var prob *apperr.Problem
		if !errors.As(err, &prob) || prob.ID != "common.error.http_status" {
			t.Fatalf("status %d: problem = %#v, want common.error.http_status", status, prob)
		}
	}
}
