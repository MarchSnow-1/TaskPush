package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/i18n"
)

// ---------------------------------------------------------------------------
// 渲染用例: 直接驱动 printer 的文案渲染, 不经过 Run
// 这类断言需要一个可控的 Problem 组合, 走 Run 会绕远且需要构造渠道
//
// Rendering cases: the copy rendering of the printer driven directly rather than through Run
// These assertions need a controlled Problem combination, and going through Run would be indirect
// and would need a channel fixture
// ---------------------------------------------------------------------------

// newTestPrinter 构造一个写到 strings.Builder 的输出器
//
// newTestPrinter builds a printer that writes into string builders
func newTestPrinter(t *testing.T, lang string) (*printer, *strings.Builder) {
	t.Helper()
	translator, err := i18n.New(lang)
	if err != nil {
		t.Fatalf("translator %s: %v", lang, err)
	}
	var stdout strings.Builder
	return newPrinter(translator, &stdout, &strings.Builder{}), &stdout
}

// TestDeferredLabel_PrefersChannelID 覆盖 ID 列优先渠道 ID, 没有 ID 时回退到 1 基配置路径
// 回退分支是防御路径: 配置层的 validateChannelID 会先拦下缺 id 或写法非法的配置,
// 因此这里直接用构造出来的 Problem 覆盖它
//
// TestDeferredLabel_PrefersChannelID covers the ID column preferring the channel ID and falling back
// to the one-based configuration path without one
// The fallback is a defensive path: validateChannelID in the configuration layer refuses a missing
// or malformed ID first, so this case covers it with a constructed Problem
func TestDeferredLabel_PrefersChannelID(t *testing.T) {
	cases := []struct {
		name    string
		problem apperr.Problem
		wantID  string
		wantTyp string
	}{
		{
			name: "channel identity wins",
			problem: apperr.Problem{
				ID:   "config.error.env_unset",
				Data: map[string]any{"ChannelID": "phone", "ChannelType": "webhook", "Path": "channels[2].timeout", "Source": "file:/example/taskpush.json"},
			},
			wantID:  "phone",
			wantTyp: "webhook",
		},
		{
			name: "path without an id",
			problem: apperr.Problem{
				ID:   "config.error.env_unset",
				Data: map[string]any{"Path": "channels[2].timeout", "Source": "file:/example/taskpush.json"},
			},
			wantID:  "channels[2].timeout",
			wantTyp: "",
		},
		{
			name: "source without a path",
			problem: apperr.Problem{
				ID:   "config.error.env_unset",
				Data: map[string]any{"Source": "stdin"},
			},
			wantID:  "stdin",
			wantTyp: "",
		},
		{
			name:    "empty data",
			problem: apperr.Problem{ID: "config.error.env_unset"},
			wantID:  "",
			wantTyp: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deferredLabel(&tc.problem); got != tc.wantID {
				t.Fatalf("deferredLabel = %q, want %q", got, tc.wantID)
			}
			if got := deferredType(&tc.problem); got != tc.wantTyp {
				t.Fatalf("deferredType = %q, want %q", got, tc.wantTyp)
			}
		})
	}

	// nil 的 Problem 返回空串而不是崩溃
	// A nil Problem yields empty strings instead of panicking
	if got := deferredLabel(nil); got != "" {
		t.Fatalf("deferredLabel(nil) = %q, want empty", got)
	}
	if got := deferredType(nil); got != "" {
		t.Fatalf("deferredType(nil) = %q, want empty", got)
	}
}

// TestRenderProblem_DropsAnInnerProblemCause 覆盖内层 Problem 作为 Cause 时不追加
// 内层 Problem 的 Error() 是 "ID: cause", 追加会把内部消息键带进用户文案
//
// TestRenderProblem_DropsAnInnerProblemCause covers an inner Problem used as the Cause being left
// out
// The Error() of an inner Problem reads as "ID: cause", so appending it would carry internal message
// keys into user copy
func TestRenderProblem_DropsAnInnerProblemCause(t *testing.T) {
	p, _ := newTestPrinter(t, "en")
	inner := &apperr.Problem{ID: "common.error.http_status", Data: map[string]any{"Status": 500, "Body": "example"}}
	outer := &apperr.Problem{
		ID:    "channel.gotify.error.http_status",
		Data:  map[string]any{"Status": 500, "Body": "example"},
		Cause: inner,
	}

	got := p.renderProblem(outer)
	if strings.Contains(got, "common.error.http_status") {
		t.Fatalf("renderProblem = %q, want no internal message key", got)
	}
	if !strings.Contains(got, "500") {
		t.Fatalf("renderProblem = %q, want the visible status kept", got)
	}
}

// TestRenderProblem_KeepsAPlainCause 覆盖普通错误作为 Cause 时仍然追加并脱敏
//
// TestRenderProblem_KeepsAPlainCause covers a plain error used as the Cause still being appended
// with redaction applied
func TestRenderProblem_KeepsAPlainCause(t *testing.T) {
	p, _ := newTestPrinter(t, "en")
	problem := &apperr.Problem{
		ID:    "config.error.duration",
		Data:  map[string]any{"Path": "timeout", "Value": "${VAR}"},
		Cause: errors.New("dial https://user:example-secret@example.com/hook failed"),
	}

	got := p.renderProblem(problem)
	if !strings.Contains(got, "dial") {
		t.Fatalf("renderProblem = %q, want the technical detail kept", got)
	}
	if strings.Contains(got, "example-secret") {
		t.Fatalf("renderProblem = %q leaked the credential", got)
	}
}

// TestRenderError_UsesRequestFailedCopy 覆盖非 Problem 错误落到请求失败文案
//
// TestRenderError_UsesRequestFailedCopy covers a non-Problem error rendering through the
// request-failed copy
func TestRenderError_UsesRequestFailedCopy(t *testing.T) {
	p, _ := newTestPrinter(t, "en")

	got := p.renderError(errors.New(`Post "https://example.com/hook": connection refused`))
	if !strings.Contains(got, "the request failed") {
		t.Fatalf("renderError = %q, want the request-failed copy", got)
	}
	if strings.Contains(got, "is missing or malformed") {
		t.Fatalf("renderError = %q still uses the bad-response copy", got)
	}
	if strings.Contains(got, "example.com") {
		t.Fatalf("renderError = %q leaked the address", got)
	}
}
