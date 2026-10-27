package wecom

/*
wecom 渠道 nil HTTP client 守卫的测试
Send 直接调用 common.DoJSON, 而 DoJSON 里的 client.Do 会解引用空指针, 因此 Send 需要先挡住 nil

Tests for the nil HTTP client guard of the WeCom channel
Send calls common.DoJSON directly, and the client.Do inside DoJSON dereferences a nil pointer, so
Send has to stop nil first
*/

import (
	"context"
	"strings"
	"testing"
)

// TestSend_NilClientFailsWithoutPanic 覆盖传入 nil client 时返回错误而不是 panic
//
// TestSend_NilClientFailsWithoutPanic covers a nil client producing an error instead of a panic
func TestSend_NilClientFailsWithoutPanic(t *testing.T) {
	ch := newChannel(t, `{"url":"https://example.com/send?key=example-key"}`)
	prepared, _ := prepare(t, ch, "example-title", "example body")

	err := preparedOf(t, prepared).Send(context.Background(), nil) //nolint:staticcheck // 这里就是要传入 nil client
	if err == nil {
		t.Fatal("Send with a nil client returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Fatalf("error = %q, want it to mention the nil client", err.Error())
	}
}
