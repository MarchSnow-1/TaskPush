package channel

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// exampleSettings 是严格解码测试的目标结构, 覆盖字符串与嵌套对象
//
// exampleSettings is the target struct of the strict-decoding tests, covering a string and a
// nested object
type exampleSettings struct {
	URL    string `json:"url"`
	Silent *bool  `json:"silent"`
}

// assertInvalidSettings 断言错误是 ID 为 channel.error.invalid_settings 的 *Problem
// 并返回它, 让调用方继续检查 Field 与 Detail
//
// assertInvalidSettings asserts the error is an *apperr.Problem with the ID
// channel.error.invalid_settings and returns it for the caller to keep checking Field and Detail
func assertInvalidSettings(t *testing.T, err error) *apperr.Problem {
	t.Helper()
	if err == nil {
		t.Fatal("DecodeStrict returned nil, want a failure")
	}
	var problem *apperr.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("error = %T (%v), want *apperr.Problem", err, err)
	}
	if problem.ID != "channel.error.invalid_settings" {
		t.Fatalf("Problem.ID = %q, want channel.error.invalid_settings", problem.ID)
	}
	if _, ok := problem.Data["Field"]; !ok {
		t.Fatalf("Problem.Data has no Field key: %v", problem.Data)
	}
	if _, ok := problem.Data["Detail"]; !ok {
		t.Fatalf("Problem.Data has no Detail key: %v", problem.Data)
	}
	return problem
}

// TestDecodeStrict_Valid 覆盖正常对象与未知字段的默认拒绝
//
// TestDecodeStrict_Valid covers a valid object and the default rejection of an unknown field
func TestDecodeStrict_Valid(t *testing.T) {
	var s exampleSettings
	if err := DecodeStrict(json.RawMessage(`{"url":"https://example.com","silent":true}`), &s); err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	if s.URL != "https://example.com" {
		t.Fatalf("URL = %q", s.URL)
	}
	if s.Silent == nil || !*s.Silent {
		t.Fatalf("Silent = %v, want true", s.Silent)
	}
}

// TestDecodeStrict_EmptyRaw 覆盖空 raw 与仅空白 raw 按空对象处理
//
// TestDecodeStrict_EmptyRaw covers an empty raw and a whitespace-only one being treated as an
// empty object
func TestDecodeStrict_EmptyRaw(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "nil raw", raw: nil},
		{name: "empty raw", raw: json.RawMessage("")},
		{name: "whitespace raw", raw: json.RawMessage("  \n\t")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := exampleSettings{URL: "example-before"}
			if err := DecodeStrict(tc.raw, &s); err != nil {
				t.Fatalf("DecodeStrict(empty) = %v, want nil", err)
			}
			// 空对象不会写入任何字段, 目标结构保留原值
			// An empty object writes no field, so the target keeps what it held
			if s.URL != "example-before" || s.Silent != nil {
				t.Fatalf("empty raw decoded into %+v", s)
			}
		})
	}
}

// TestDecodeStrict_UnknownField 覆盖未知字段被拒绝且 Field 给出该键
//
// TestDecodeStrict_UnknownField covers an unknown field being rejected with Field naming it
func TestDecodeStrict_UnknownField(t *testing.T) {
	var s exampleSettings
	problem := assertInvalidSettings(t, DecodeStrict(json.RawMessage(`{"url":"https://example.com","unknown":"x"}`), &s))
	if problem.Data["Field"] != "unknown" {
		t.Fatalf("Data[Field] = %v, want unknown", problem.Data["Field"])
	}
	if problem.Cause == nil || !strings.Contains(problem.Cause.Error(), "unknown field") {
		t.Fatalf("Cause = %v, want the encoding/json detail", problem.Cause)
	}
}

// TestDecodeStrict_TypeMismatch 覆盖字段类型不匹配的失败与 Field 的兜底空值
//
// TestDecodeStrict_TypeMismatch covers a field type mismatch failing with Field falling back
// to empty
func TestDecodeStrict_TypeMismatch(t *testing.T) {
	var s exampleSettings
	problem := assertInvalidSettings(t, DecodeStrict(json.RawMessage(`{"silent":"example"}`), &s))
	if problem.Data["Field"] != "" {
		t.Fatalf("Data[Field] = %v, want empty for a non-unknown-field failure", problem.Data["Field"])
	}
	if detail, _ := problem.Data["Detail"].(string); !strings.Contains(detail, "bool") {
		t.Fatalf("Data[Detail] = %v, want the type detail", problem.Data["Detail"])
	}
}

// TestDecodeStrict_TopLevelShape 覆盖顶层数组、标量与尾随内容的拒绝
//
// TestDecodeStrict_TopLevelShape covers rejecting a top-level array, a scalar and trailing content
func TestDecodeStrict_TopLevelShape(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "array", raw: `[]`},
		{name: "string scalar", raw: `"example"`},
		{name: "number scalar", raw: `1`},
		{name: "null", raw: `null`},
		{name: "trailing object", raw: `{"url":"https://example.com"}{"url":"https://example.org"}`},
		{name: "trailing token", raw: `{"url":"https://example.com"} true`},
		{name: "truncated", raw: `{"url":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// target 用 any 承载, 让数组与标量也能进入后续的形状检查
			// The target is an any so an array or a scalar still reaches the shape check
			var target any
			assertInvalidSettings(t, DecodeStrict(json.RawMessage(tc.raw), &target))
		})
	}
}

// TestDecodeStrict_ObjectTarget 覆盖顶层对象传入结构体指针的正常路径与空对象
//
// TestDecodeStrict_ObjectTarget covers the normal path of a top-level object into a struct
// pointer, along with an empty object
func TestDecodeStrict_ObjectTarget(t *testing.T) {
	var s exampleSettings
	if err := DecodeStrict(json.RawMessage(`{}`), &s); err != nil {
		t.Fatalf("DecodeStrict({}) = %v, want nil", err)
	}
	if s.URL != "" || s.Silent != nil {
		t.Fatalf("empty object decoded into %+v", s)
	}
	if err := DecodeStrict(json.RawMessage(`{"url":""}`), &s); err != nil {
		t.Fatalf("DecodeStrict with an empty string field = %v, want nil", err)
	}
	if s.URL != "" {
		t.Fatalf("URL = %q, want the decoded empty string", s.URL)
	}
}

// TestDecodeStrict_WrappedProblemKeepsCause 覆盖工厂再包一层键时 Cause 仍然可穿透
//
// TestDecodeStrict_WrappedProblemKeepsCause covers the Cause staying reachable when a factory
// wraps the error under its own key
func TestDecodeStrict_WrappedProblemKeepsCause(t *testing.T) {
	var s exampleSettings
	inner := DecodeStrict(json.RawMessage(`{"unknown":"x"}`), &s)
	outer := apperr.Permanent(&apperr.Problem{
		ID:    "channel.example.error.invalid_settings",
		Data:  map[string]any{"Field": "unknown"},
		Cause: inner,
	})

	if !apperr.IsPermanent(outer) {
		t.Fatal("the wrapper lost the permanent marker")
	}
	var problem *apperr.Problem
	if !errors.As(outer, &problem) {
		t.Fatal("errors.As did not reach the outer *Problem")
	}
	if problem.ID != "channel.example.error.invalid_settings" {
		t.Fatalf("outer ID = %q", problem.ID)
	}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is did not reach the inner decode Problem through Cause")
	}
}
