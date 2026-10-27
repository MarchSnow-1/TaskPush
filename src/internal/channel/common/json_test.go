package common

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// JSON helper 测试: MarshalNoHTMLEscape 不转义 < > &, DecodeRawJSON 保持大数精度并拒绝尾随内容
//
// JSON helper tests: MarshalNoHTMLEscape leaves < > & alone, while DecodeRawJSON keeps large
// numbers precise and refuses trailing content
// ---------------------------------------------------------------------------

func TestMarshalNoHTMLEscape(t *testing.T) {
	got, err := MarshalNoHTMLEscape(map[string]string{"body": "<b>a & b</b>"})
	if err != nil {
		t.Fatalf("MarshalNoHTMLEscape: %v", err)
	}
	want := `{"body":"<b>a & b</b>"}`
	if string(got) != want {
		t.Errorf("MarshalNoHTMLEscape = %s, want %s", got, want)
	}
	if strings.HasSuffix(string(got), "\n") {
		t.Errorf("MarshalNoHTMLEscape kept the trailing newline: %q", got)
	}

	// 默认入口仍然转义, 用来说明上面的差异来自 SetEscapeHTML(false)
	// The default entry still escapes, which shows the difference comes from SetEscapeHTML(false)
	defaultOut, err := json.Marshal(map[string]string{"body": "<b>a & b</b>"})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(defaultOut) == want {
		t.Error("json.Marshal produced the same output, so the escape difference is untested")
	}
}

func TestMarshalNoHTMLEscapeTypesAndErrors(t *testing.T) {
	got, err := MarshalNoHTMLEscape(map[string]any{
		"n":      json.Number("12345678901234567890"),
		"b":      true,
		"nil":    nil,
		"values": []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("MarshalNoHTMLEscape: %v", err)
	}
	if !strings.Contains(string(got), "12345678901234567890") {
		t.Errorf("output %s lost the json.Number precision", got)
	}

	// 不可序列化的值原样返回错误, 不 panic
	// A value that cannot be serialised returns its error rather than panicking
	if _, err := MarshalNoHTMLEscape(make(chan int)); err == nil {
		t.Error("MarshalNoHTMLEscape(channel) error = nil, want an unsupported-type error")
	}
}

// TestDecodeRawJSONPreservesNumberPrecision 确认大整数与小数按原文保留, 不经过 float64 舍入
//
// TestDecodeRawJSONPreservesNumberPrecision confirms a large integer and a decimal keep the digits
// they were written with instead of being rounded through float64
func TestDecodeRawJSONPreservesNumberPrecision(t *testing.T) {
	raw := json.RawMessage(`{"big":123456789012345678901234567890,"pi":3.14159265358979323846}`)
	got, err := DecodeRawJSON(raw)
	if err != nil {
		t.Fatalf("DecodeRawJSON: %v", err)
	}
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("DecodeRawJSON type = %T, want map[string]any", got)
	}
	big, ok := obj["big"].(json.Number)
	if !ok {
		t.Fatalf("big type = %T, want json.Number", obj["big"])
	}
	if big.String() != "123456789012345678901234567890" {
		t.Errorf("big = %s, want the digits as written", big)
	}
	if f, err := big.Float64(); err == nil && f != 1.2345678901234568e29 {
		// 这里只说明原文本未经 float64 往返, 因此可以拿到比 float64 更多的位数
		// This only shows the original text never went through float64, so more digits survive
		t.Logf("float64 projection of big is %v", f)
	}
	pi, ok := obj["pi"].(json.Number)
	if !ok {
		t.Fatalf("pi type = %T, want json.Number", obj["pi"])
	}
	if pi.String() != "3.14159265358979323846" {
		t.Errorf("pi = %s, want the decimal as written", pi)
	}
}

func TestDecodeRawJSONTopLevelTypes(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		check func(t *testing.T, got any)
	}{
		{"object", `{"a":1}`, func(t *testing.T, got any) {
			obj, ok := got.(map[string]any)
			if !ok || len(obj) != 1 {
				t.Fatalf("got %#v, want a one-key object", got)
			}
		}},
		{"array", `[1,"two",true]`, func(t *testing.T, got any) {
			arr, ok := got.([]any)
			if !ok || len(arr) != 3 {
				t.Fatalf("got %#v, want a three-element array", got)
			}
		}},
		{"string", `"example"`, func(t *testing.T, got any) {
			if got != "example" {
				t.Fatalf("got %#v, want \"example\"", got)
			}
		}},
		{"true", `true`, func(t *testing.T, got any) {
			if got != true {
				t.Fatalf("got %#v, want true", got)
			}
		}},
		{"null", `null`, func(t *testing.T, got any) {
			if got != nil {
				t.Fatalf("got %#v, want nil", got)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DecodeRawJSON(json.RawMessage(c.raw))
			if err != nil {
				t.Fatalf("DecodeRawJSON(%s): %v", c.raw, err)
			}
			c.check(t, got)
		})
	}
}

// TestDecodeRawJSONEmptyIsObject 覆盖缺失/空白 raw 按 {} 处理
//
// TestDecodeRawJSONEmptyIsObject covers a missing or whitespace-only raw being treated as {}
func TestDecodeRawJSONEmptyIsObject(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage(""), json.RawMessage("  \n\t ")} {
		got, err := DecodeRawJSON(raw)
		if err != nil {
			t.Fatalf("DecodeRawJSON(%q) error: %v", raw, err)
		}
		obj, ok := got.(map[string]any)
		if !ok || len(obj) != 0 {
			t.Errorf("DecodeRawJSON(%q) = %#v, want an empty object", raw, got)
		}
	}
}

func TestDecodeRawJSONRejectsTrailingContent(t *testing.T) {
	cases := []string{
		`1 2`,
		`{} {}`,
		`{"a":1}x`,
		`[1] [2]`,
		`"a" "b"`,
	}
	for _, raw := range cases {
		got, err := DecodeRawJSON(json.RawMessage(raw))
		if err == nil {
			t.Fatalf("DecodeRawJSON(%s) = %#v, want a trailing-content error", raw, got)
		}
		var prob *apperr.Problem
		if !errors.As(err, &prob) {
			t.Fatalf("DecodeRawJSON(%s) error type = %T, want *apperr.Problem", raw, err)
		}
		if prob.ID != keyErrorBadJSON {
			t.Errorf("DecodeRawJSON(%s) ID = %q, want %q", raw, prob.ID, keyErrorBadJSON)
		}
		if prob.Data["Field"] != "body" {
			t.Errorf("DecodeRawJSON(%s) Data.Field = %v, want %q", raw, prob.Data["Field"], "body")
		}
		if prob.Data["Detail"] == "" {
			t.Errorf("DecodeRawJSON(%s) Data.Detail is empty", raw)
		}
	}
}

func TestDecodeRawJSONRejectsMalformed(t *testing.T) {
	cases := []string{
		`{`,
		`{"a":}`,
		`[1,]`,
		`"unterminated`,
		`nul`,
	}
	for _, raw := range cases {
		_, err := DecodeRawJSON(json.RawMessage(raw))
		if err == nil {
			t.Fatalf("DecodeRawJSON(%s) error = nil, want a parse error", raw)
		}
		var prob *apperr.Problem
		if !errors.As(err, &prob) {
			t.Fatalf("DecodeRawJSON(%s) error type = %T, want *apperr.Problem", raw, err)
		}
		if prob.ID != keyErrorBadJSON {
			t.Errorf("DecodeRawJSON(%s) ID = %q, want %q", raw, prob.ID, keyErrorBadJSON)
		}
		if prob.Cause == nil {
			t.Errorf("DecodeRawJSON(%s) Cause = nil, want the decoder error", raw)
		}
	}
}

// TestDecodeRawJSONDetailRedacted 确认 Detail 里的地址被打码, 免得上游把地址回显进 Data
//
// TestDecodeRawJSONDetailRedacted confirms an address inside Detail is masked, so upstream text
// cannot echo it into Data
func TestDecodeRawJSONDetailRedacted(t *testing.T) {
	raw := json.RawMessage(`{"url":"https://example.com/push?token=example-token",}`)
	_, err := DecodeRawJSON(raw)
	if err == nil {
		t.Fatal("a trailing comma should fail")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error type = %T, want *apperr.Problem", err)
	}
	detail, _ := prob.Data["Detail"].(string)
	if strings.Contains(detail, "example-token") {
		t.Errorf("Detail = %q, want the token masked", detail)
	}
}
