package webhook

/*
webhook 渠道的静态校验, 模板渲染与请求构造测试
全部用例只使用标准库与本地数据, 不发网络请求; 网络行为由 webhook_send_test.go 用 httptest 覆盖

Tests for the static validation, template rendering and request construction of the webhook channel
Every case uses the standard library and local data only, without network requests; network behaviour
is covered by webhook_send_test.go with httptest
*/

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// createWebhook 通过工厂创建渠道, 测试失败时直接结束当前用例
//
// createWebhook creates a channel through the factory and fails the current case when creation
// does not succeed
func createWebhook(t *testing.T, raw string) channel.Channel {
	t.Helper()
	ch, err := (factory{}).Create("example", json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Create(%s) error: %v", raw, err)
	}
	return ch
}

// prepareWebhook 用给定变量与模式准备渠道, 返回 Prepared 与警告
//
// prepareWebhook prepares the channel with the given variables and mode, returning the Prepared and
// the warnings
func prepareWebhook(t *testing.T, raw string, vars *template.Vars, mode channel.RenderMode) (channel.Prepared, []apperr.Problem) {
	t.Helper()
	ch := createWebhook(t, raw)
	prepared, warnings, err := ch.Prepare(context.Background(), channel.Message{
		Title: "Example title",
		Body:  "Example body",
		Vars:  vars,
		Mode:  mode,
	})
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	if prepared == nil {
		t.Fatal("Prepare returned a nil Prepared without an error")
	}
	return prepared, warnings
}

// prepareProblem 准备渠道并断言失败, 返回 Problem 与其 Permanent 标记
//
// prepareProblem prepares the channel expecting a failure and returns the Problem with its
// permanent marker
func prepareProblem(t *testing.T, raw string, vars *template.Vars, mode channel.RenderMode) (*apperr.Problem, bool) {
	t.Helper()
	ch := createWebhook(t, raw)
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{Vars: vars, Mode: mode})
	if err == nil {
		t.Fatal("Prepare returned nil error, want a failure")
	}
	if prepared != nil {
		t.Fatal("Prepare returned a non-nil Prepared together with an error")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("Prepare error = %T (%v), want *apperr.Problem", err, err)
	}
	return prob, apperr.IsPermanent(err)
}

// createProblem 创建渠道并断言失败, 返回 Problem 与其 Permanent 标记
//
// createProblem creates the channel expecting a failure and returns the Problem with its permanent
// marker
func createProblem(t *testing.T, raw string) (*apperr.Problem, bool) {
	t.Helper()
	ch, err := (factory{}).Create("example", json.RawMessage(raw))
	if err == nil {
		t.Fatal("Create returned nil error, want a failure")
	}
	if ch != nil {
		t.Fatal("Create returned a non-nil channel together with an error")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("Create error = %T (%v), want *apperr.Problem", err, err)
	}
	return prob, apperr.IsPermanent(err)
}

// rawProblem 把错误链上的 *apperr.Problem 取出, 取出失败时结束用例
//
// rawProblem extracts the *apperr.Problem from an error chain and stops the case when absent
func rawProblem(t *testing.T, err error) *apperr.Problem {
	t.Helper()
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error = %T (%v), want *apperr.Problem", err, err)
	}
	return prob
}

// TestFactory_Fields 覆盖 type, 描述键与 Fields 的完整元数据
//
// TestFactory_Fields covers the type, the description key and the complete Fields metadata
func TestFactory_Fields(t *testing.T) {
	f := factory{}
	if f.Type() != "webhook" {
		t.Fatalf("Type() = %q, want webhook", f.Type())
	}
	if f.Describe() != keyDesc {
		t.Fatalf("Describe() = %q, want %q", f.Describe(), keyDesc)
	}

	want := map[string]struct {
		typ      string
		required bool
		def      string
		desc     string
	}{
		"url":              {typ: "string", required: true, desc: keyFieldURL},
		"method":           {typ: "string", def: defaultMethod, desc: keyFieldMethod},
		"headers":          {typ: "object", desc: keyFieldHeaders},
		"body":             {typ: "object", desc: keyFieldBody},
		"body.format":      {typ: "string", desc: keyFieldBodyFormat},
		"body.json":        {typ: "object", desc: keyFieldBodyJSON},
		"body.text":        {typ: "string", desc: keyFieldBodyText},
		"body.fields":      {typ: "object", desc: keyFieldBodyFields},
		"body.contentType": {typ: "string", desc: keyFieldBodyContentType},
		"maskSecrets":      {typ: "stringlist", desc: keyFieldMaskSecrets},
	}
	fields := f.Fields()
	if len(fields) != len(want) {
		t.Fatalf("Fields() has %d entries, want %d", len(fields), len(want))
	}
	seen := map[string]bool{}
	for _, field := range fields {
		expected, ok := want[field.Name]
		if !ok {
			t.Fatalf("Fields() returned an unexpected field %q", field.Name)
		}
		if seen[field.Name] {
			t.Fatalf("Fields() returned %q twice", field.Name)
		}
		seen[field.Name] = true
		if field.Type != expected.typ || field.Required != expected.required ||
			field.Default != expected.def || field.DescriptionKey != expected.desc {
			t.Fatalf("field %q = %+v, want type=%q required=%v default=%q desc=%q",
				field.Name, field, expected.typ, expected.required, expected.def, expected.desc)
		}
	}
}

// TestCreate_Strict 覆盖未知键, 非对象 settings, body 未知键与 url 缺失或类型错误
//
// TestCreate_Strict covers an unknown key, non-object settings, an unknown body key and a missing
// or wrongly typed url
func TestCreate_Strict(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantID    string
		wantPerm  bool
		checkData func(*testing.T, *apperr.Problem)
	}{
		{
			name:     "unknown top-level key",
			raw:      `{"url":"https://example.com","unknown":true}`,
			wantID:   "channel.error.invalid_settings",
			wantPerm: true,
			checkData: func(t *testing.T, p *apperr.Problem) {
				if p.Data["Field"] != "unknown" {
					t.Fatalf("Data[Field] = %v, want unknown", p.Data["Field"])
				}
			},
		},
		{
			name:     "settings not an object",
			raw:      `[]`,
			wantID:   "channel.error.invalid_settings",
			wantPerm: true,
		},
		{
			name:     "body unknown key",
			raw:      `{"url":"https://example.com","body":{"format":"text","text":"x","extra":1}}`,
			wantID:   "channel.error.invalid_settings",
			wantPerm: true,
		},
		{
			name:     "url missing",
			raw:      `{"method":"POST"}`,
			wantID:   keyErrorMissingURL,
			wantPerm: true,
		},
		{
			name:     "url wrong type",
			raw:      `{"url":1}`,
			wantID:   "channel.error.invalid_settings",
			wantPerm: true,
		},
		{
			name:     "headers wrong type",
			raw:      `{"url":"https://example.com","headers":["a"]}`,
			wantID:   "channel.error.invalid_settings",
			wantPerm: true,
		},
		{
			name:     "body wrong type",
			raw:      `{"url":"https://example.com","body":1}`,
			wantID:   "channel.error.invalid_settings",
			wantPerm: true,
		},
		{
			name:     "empty object body",
			raw:      `{"url":"https://example.com","body":{}}`,
			wantID:   keyErrorBodyEmpty,
			wantPerm: true,
		},
		{
			name:     "contentType only body",
			raw:      `{"url":"https://example.com","body":{"contentType":"text/plain"}}`,
			wantID:   keyErrorBodyEmpty,
			wantPerm: true,
		},
		{
			name:     "format without content",
			raw:      `{"url":"https://example.com","body":{"format":"json"}}`,
			wantID:   keyErrorBodyJSONMissing,
			wantPerm: true,
		},
		{
			name:     "json format without json",
			raw:      `{"url":"https://example.com","body":{"format":"json"}}`,
			wantID:   keyErrorBodyJSONMissing,
			wantPerm: true,
		},
		{
			name:     "text format without text",
			raw:      `{"url":"https://example.com","body":{"format":"text"}}`,
			wantID:   keyErrorBodyEmpty,
			wantPerm: true,
		},
		{
			name:     "form format without fields",
			raw:      `{"url":"https://example.com","body":{"format":"form"}}`,
			wantID:   keyErrorBodyEmpty,
			wantPerm: true,
		},
		{
			name:     "text without format",
			raw:      `{"url":"https://example.com","body":{"text":"x"}}`,
			wantID:   keyErrorBodyFormat,
			wantPerm: true,
			checkData: func(t *testing.T, p *apperr.Problem) {
				if p.Data["Format"] != formatMissing || p.Data["Allowed"] != allowedFormats {
					t.Fatalf("Data = %v, want Format=%q Allowed=%q", p.Data, formatMissing, allowedFormats)
				}
			},
		},
		{
			name:     "unknown format",
			raw:      `{"url":"https://example.com","body":{"format":"xml","text":"x"}}`,
			wantID:   keyErrorBodyFormat,
			wantPerm: true,
			checkData: func(t *testing.T, p *apperr.Problem) {
				if p.Data["Format"] != "xml" || p.Data["Allowed"] == "" {
					t.Fatalf("Data = %v, want Format and Allowed", p.Data)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problem, permanent := createProblem(t, tc.raw)
			if problem.ID != tc.wantID {
				t.Fatalf("Problem.ID = %q, want %q", problem.ID, tc.wantID)
			}
			if permanent != tc.wantPerm {
				t.Fatalf("permanent = %v, want %v", permanent, tc.wantPerm)
			}
			if tc.checkData != nil {
				tc.checkData(t, problem)
			}
		})
	}
}

// TestCreate_AllowedBodyShapes 覆盖字符串正文与允许空值的 text/form 正文
//
// TestCreate_AllowedBodyShapes covers a string body and the text and form bodies that allow an
// empty value
func TestCreate_AllowedBodyShapes(t *testing.T) {
	cases := []string{
		`{"url":"https://example.com","body":""}`,
		`{"url":"https://example.com","body":"hello"}`,
		`{"url":"https://example.com","body":null}`,
		`{"url":"https://example.com","body":{"format":"text","text":""}}`,
		`{"url":"https://example.com","body":{"format":"form","fields":{}}}`,
		`{"url":"https://example.com","body":{"format":"json","json":{}}}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			createWebhook(t, raw)
		})
	}
}

// TestPrepare_BodyForms 覆盖字符串与 json/text/form 三种对象正文, contentType 覆盖与缺省 body
//
// TestPrepare_BodyForms covers the string body, the json, text and form object bodies, the
// contentType override and an absent body
func TestPrepare_BodyForms(t *testing.T) {
	vars := template.NewVars()
	vars.Set("title", template.Literal("Example title"))
	vars.Set("body", template.Literal("Example body"))
	vars.Set("name", "world")

	cases := []struct {
		name       string
		raw        string
		wantFormat string
		wantCT     string
		wantBody   string
		bodyNoBody bool
	}{
		{
			name:       "string body",
			raw:        `{"url":"https://example.com/hook","body":"hello {{name}}"}`,
			wantFormat: formatText,
			wantCT:     contentTypeText,
			wantBody:   "hello world",
		},
		{
			name:       "text body",
			raw:        `{"url":"https://example.com/hook","body":{"format":"text","text":"hello {{name}}"}}`,
			wantFormat: formatText,
			wantCT:     contentTypeText,
			wantBody:   "hello world",
		},
		{
			name:       "json body",
			raw:        `{"url":"https://example.com/hook","body":{"format":"json","json":{"title":"{{title}}","name":"{{name}}"}}}`,
			wantFormat: formatJSON,
			wantCT:     contentTypeJSON,
			wantBody:   `{"name":"world","title":"Example title"}`,
		},
		{
			name:       "form body",
			raw:        `{"url":"https://example.com/hook","body":{"format":"form","fields":{"b":"hello world","a":"1"}}}`,
			wantFormat: formatForm,
			wantCT:     contentTypeForm,
			wantBody:   "a=1&b=hello+world",
		},
		{
			name:       "contentType override",
			raw:        `{"url":"https://example.com/hook","body":{"format":"text","text":"x","contentType":"text/markdown"}}`,
			wantFormat: formatText,
			wantCT:     "text/markdown",
			wantBody:   "x",
		},
		{
			name:       "no body",
			raw:        `{"url":"https://example.com/hook"}`,
			wantFormat: formatNone,
			bodyNoBody: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prepared, warnings := prepareWebhook(t, tc.raw, vars, channel.RenderStrict)
			if len(warnings) != 0 {
				t.Fatalf("warnings = %v, want none", warnings)
			}
			p := prepared.(*preparedChannel)
			if p.format != tc.wantFormat {
				t.Fatalf("format = %q, want %q", p.format, tc.wantFormat)
			}
			if got := p.req.Header.Get("Content-Type"); got != tc.wantCT {
				t.Fatalf("Content-Type = %q, want %q", got, tc.wantCT)
			}
			if tc.bodyNoBody {
				if p.req.Body != nil || p.bytes != 0 {
					t.Fatalf("absent body produced body=%v bytes=%d", p.req.Body, p.bytes)
				}
				return
			}
			if p.bytes != len(tc.wantBody) {
				t.Fatalf("bytes = %d, want %d", p.bytes, len(tc.wantBody))
			}
			if got := string(p.body); got != tc.wantBody {
				t.Fatalf("body = %q, want %q", got, tc.wantBody)
			}
		})
	}
}

// TestPrepare_UnknownFormat 覆盖绕过 Create 直接进入 Prepare 时未知 format 仍被拒绝
//
// TestPrepare_UnknownFormat covers an unknown format still being rejected when Prepare is reached
// without Create
func TestPrepare_UnknownFormat(t *testing.T) {
	c := &webhookChannel{
		id: "example",
		settings: settings{
			URL: "https://example.com",
			Body: &bodySettings{
				Format:   "xml",
				Text:     "x",
				isObject: true,
				hasText:  true,
			},
		},
	}
	_, _, err := c.Prepare(context.Background(), channel.Message{})
	problem := rawProblem(t, err)
	if problem.ID != keyErrorBodyFormat {
		t.Fatalf("Problem.ID = %q, want %q", problem.ID, keyErrorBodyFormat)
	}
}

// TestPrepare_URL 覆盖 https, http 回环, 裸域名, 空 host, 非法 scheme 与模板 URL
//
// TestPrepare_URL covers https, http loopback, a bare domain, an empty host, an invalid scheme and
// a templated URL
func TestPrepare_URL(t *testing.T) {
	t.Run("https", func(t *testing.T) {
		prepared, _ := prepareWebhook(t, `{"url":"https://example.com/hook?x=1"}`, nil, channel.RenderStrict)
		if got := prepared.(*preparedChannel).target; got != "https://example.com/hook?x=1" {
			t.Fatalf("target = %q", got)
		}
	})
	t.Run("loopback http", func(t *testing.T) {
		prepared, _ := prepareWebhook(t, `{"url":"127.0.0.1:8080/hook"}`, nil, channel.RenderStrict)
		if got := prepared.(*preparedChannel).target; got != "http://127.0.0.1:8080/hook" {
			t.Fatalf("target = %q", got)
		}
	})
	t.Run("bare domain gets https", func(t *testing.T) {
		prepared, _ := prepareWebhook(t, `{"url":"example.com/hook"}`, nil, channel.RenderStrict)
		if got := prepared.(*preparedChannel).target; got != "https://example.com/hook" {
			t.Fatalf("target = %q", got)
		}
	})
	t.Run("empty host", func(t *testing.T) {
		problem, permanent := prepareProblem(t, `{"url":"https:///hook"}`, nil, channel.RenderStrict)
		if problem.ID != keyErrorInvalidURL || !permanent {
			t.Fatalf("got ID=%q permanent=%v", problem.ID, permanent)
		}
	})
	t.Run("invalid scheme", func(t *testing.T) {
		problem, permanent := prepareProblem(t, `{"url":"ftp://example.com/hook"}`, nil, channel.RenderStrict)
		if problem.ID != keyErrorInvalidURL || !permanent {
			t.Fatalf("got ID=%q permanent=%v", problem.ID, permanent)
		}
		if _, ok := problem.Data["URL"]; !ok {
			t.Fatalf("Data has no URL key: %v", problem.Data)
		}
	})
	t.Run("template", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("id", "hook")
		prepared, _ := prepareWebhook(t, `{"url":"https://example.com/{{id}}"}`, vars, channel.RenderStrict)
		if got := prepared.(*preparedChannel).target; got != "https://example.com/hook" {
			t.Fatalf("target = %q", got)
		}
	})
	t.Run("template renders empty", func(t *testing.T) {
		problem, _ := prepareProblem(t, `{"url":"{{missing}}"}`, nil, channel.RenderStrict)
		if problem.ID != "template.error.unresolved" {
			t.Fatalf("Problem.ID = %q, want the template error before the empty check", problem.ID)
		}
	})
}

// TestPrepare_Headers 覆盖大小写重复, 渲染同名, CRLF, Host, Content-Length 与 Transfer-Encoding
//
// TestPrepare_Headers covers case-variant duplicates, two keys rendering into one name, CRLF, Host,
// Content-Length and Transfer-Encoding
func TestPrepare_Headers(t *testing.T) {
	t.Run("rendered names and values", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("header", "X-Example")
		vars.Set("value", "example")
		prepared, _ := prepareWebhook(t,
			`{"url":"https://example.com","headers":{"{{header}}":"{{value}}"}}`,
			vars, channel.RenderStrict)
		if got := prepared.(*preparedChannel).req.Header.Get("X-Example"); got != "example" {
			t.Fatalf("X-Example = %q, want example", got)
		}
	})
	t.Run("case variant duplicate", func(t *testing.T) {
		problem, permanent := prepareProblem(t,
			`{"url":"https://example.com","headers":{"X-Token":"a","x-token":"b"}}`,
			nil, channel.RenderStrict)
		if problem.ID != keyErrorDuplicateHeader || !permanent {
			t.Fatalf("got ID=%q permanent=%v", problem.ID, permanent)
		}
	})
	t.Run("two keys render into one name", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("part", "b")
		problem, _ := prepareProblem(t,
			`{"url":"https://example.com","headers":{"X-{{part}}":"a","X-b":"c"}}`,
			vars, channel.RenderStrict)
		if problem.ID != keyErrorDuplicateHeader {
			t.Fatalf("Problem.ID = %q, want %q", problem.ID, keyErrorDuplicateHeader)
		}
	})
	t.Run("crlf value", func(t *testing.T) {
		problem, _ := prepareProblem(t,
			`{"url":"https://example.com","headers":{"X-Token":"a\r\nb"}}`,
			nil, channel.RenderStrict)
		if problem.ID != keyErrorInvalidHeaderVal {
			t.Fatalf("Problem.ID = %q, want %q", problem.ID, keyErrorInvalidHeaderVal)
		}
	})
	t.Run("invalid name", func(t *testing.T) {
		problem, _ := prepareProblem(t,
			`{"url":"https://example.com","headers":{"X-Token:":"a"}}`,
			nil, channel.RenderStrict)
		if problem.ID != keyErrorInvalidHeaderName {
			t.Fatalf("Problem.ID = %q, want %q", problem.ID, keyErrorInvalidHeaderName)
		}
	})
	t.Run("host", func(t *testing.T) {
		prepared, _ := prepareWebhook(t,
			`{"url":"https://example.com","headers":{"Host":"internal.example.com"}}`,
			nil, channel.RenderStrict)
		if got := prepared.(*preparedChannel).req.Host; got != "internal.example.com" {
			t.Fatalf("req.Host = %q", got)
		}
	})
	t.Run("invalid host", func(t *testing.T) {
		problem, _ := prepareProblem(t,
			`{"url":"https://example.com","headers":{"Host":"bad host"}}`,
			nil, channel.RenderStrict)
		if problem.ID != keyErrorSpecialHeader {
			t.Fatalf("Problem.ID = %q, want %q", problem.ID, keyErrorSpecialHeader)
		}
	})
	t.Run("content length matching", func(t *testing.T) {
		prepared, _ := prepareWebhook(t,
			`{"url":"https://example.com","headers":{"Content-Length":"3"},"body":"abc"}`,
			nil, channel.RenderStrict)
		if got := prepared.(*preparedChannel).req.ContentLength; got != 3 {
			t.Fatalf("ContentLength = %d, want 3", got)
		}
	})
	t.Run("content length mismatch", func(t *testing.T) {
		problem, _ := prepareProblem(t,
			`{"url":"https://example.com","headers":{"Content-Length":"4"},"body":"abc"}`,
			nil, channel.RenderStrict)
		if problem.ID != keyErrorContentLength {
			t.Fatalf("Problem.ID = %q, want %q", problem.ID, keyErrorContentLength)
		}
		if problem.Data["Value"] != "4" || problem.Data["Actual"] != 3 {
			t.Fatalf("Data = %v, want Value=4 Actual=3", problem.Data)
		}
	})
	t.Run("transfer encoding", func(t *testing.T) {
		problem, _ := prepareProblem(t,
			`{"url":"https://example.com","headers":{"Transfer-Encoding":"chunked"}}`,
			nil, channel.RenderStrict)
		if problem.ID != keyErrorTransferEncoding {
			t.Fatalf("Problem.ID = %q, want %q", problem.ID, keyErrorTransferEncoding)
		}
	})
	t.Run("headers content type wins", func(t *testing.T) {
		prepared, _ := prepareWebhook(t,
			`{"url":"https://example.com","headers":{"Content-Type":"text/markdown"},"body":{"format":"text","text":"x","contentType":"text/plain"}}`,
			nil, channel.RenderStrict)
		if got := prepared.(*preparedChannel).req.Header.Get("Content-Type"); got != "text/markdown" {
			t.Fatalf("Content-Type = %q, want text/markdown", got)
		}
	})
}

// TestPrepare_JSON 覆盖合法, 非法, HTML 不转义, 数字精度与单变量类型保真
//
// TestPrepare_JSON covers valid and invalid JSON, unescaped HTML, number precision and single
// variable type fidelity
func TestPrepare_JSON(t *testing.T) {
	t.Run("html is not escaped", func(t *testing.T) {
		prepared, _ := prepareWebhook(t,
			`{"url":"https://example.com","body":{"format":"json","json":{"text":"<tag>&"}}}`,
			nil, channel.RenderStrict)
		got := string(prepared.(*preparedChannel).body)
		if !strings.Contains(got, "<tag>&") || strings.Contains(got, `\u003c`) {
			t.Fatalf("body = %q, want unescaped HTML", got)
		}
	})
	t.Run("number precision", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("count", json.Number("9007199254740993"))
		prepared, _ := prepareWebhook(t,
			`{"url":"https://example.com","body":{"format":"json","json":{"count":"{{count}}"}}}`,
			vars, channel.RenderStrict)
		got := string(prepared.(*preparedChannel).body)
		if got != `{"count":9007199254740993}` {
			t.Fatalf("body = %q", got)
		}
	})
	t.Run("single variable type fidelity", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("number", 5)
		vars.Set("truth", true)
		vars.Set("nothing", nil)
		vars.Set("list", []any{1, "two"})
		prepared, _ := prepareWebhook(t,
			`{"url":"https://example.com","body":{"format":"json","json":{"n":"{{number}}","b":"{{truth}}","z":"{{nothing}}","a":"{{list}}"}}}`,
			vars, channel.RenderStrict)
		got := string(prepared.(*preparedChannel).body)
		want := `{"a":[1,"two"],"b":true,"n":5,"z":null}`
		if got != want {
			t.Fatalf("body = %q, want %q", got, want)
		}
	})
	t.Run("invalid json through content type override", func(t *testing.T) {
		problem, permanent := prepareProblem(t,
			`{"url":"https://example.com","headers":{"Content-Type":"application/json"},"body":"not json"}`,
			nil, channel.RenderStrict)
		if problem.ID != keyErrorInvalidJSONBody || !permanent {
			t.Fatalf("got ID=%q permanent=%v", problem.ID, permanent)
		}
	})
	t.Run("empty body as json", func(t *testing.T) {
		problem, _ := prepareProblem(t,
			`{"url":"https://example.com","body":{"format":"text","text":"","contentType":"application/json"}}`,
			nil, channel.RenderStrict)
		if problem.ID != keyErrorInvalidJSONBody {
			t.Fatalf("Problem.ID = %q, want %q", problem.ID, keyErrorInvalidJSONBody)
		}
	})
}

// TestPrepare_Unresolved 覆盖严格模式报错与 check 模式的 webhook 警告键
//
// TestPrepare_Unresolved covers the strict-mode error and the webhook warning key under check mode
func TestPrepare_Unresolved(t *testing.T) {
	t.Run("strict url", func(t *testing.T) {
		problem, permanent := prepareProblem(t, `{"url":"https://example.com/{{missing}}"}`, nil, channel.RenderStrict)
		if problem.ID != "template.error.unresolved" || !permanent {
			t.Fatalf("got ID=%q permanent=%v", problem.ID, permanent)
		}
		if got := problem.Data["Path"]; got != "settings.url" {
			t.Fatalf("Path = %v, want settings.url", got)
		}
	})
	t.Run("check body warning", func(t *testing.T) {
		prepared, warnings := prepareWebhook(t,
			`{"url":"https://example.com","body":{"format":"text","text":"{{missing}}"}}`,
			nil, channel.RenderCheck)
		if prepared == nil {
			t.Fatal("Prepare returned nil Prepared")
		}
		if len(warnings) != 1 {
			t.Fatalf("warnings = %v, want one", warnings)
		}
		if warnings[0].ID != keyWarnUnresolved {
			t.Fatalf("warning ID = %q, want %q", warnings[0].ID, keyWarnUnresolved)
		}
		if warnings[0].Data["Field"] != "settings.body.text" || warnings[0].Data["Snippet"] == "" {
			t.Fatalf("warning Data = %v, want Field and Snippet", warnings[0].Data)
		}
	})
}

// TestPreview_NoSecrets 覆盖预览不含 header 值, 正文与 URL 凭据
//
// TestPreview_NoSecrets covers the preview keeping header values, the body and URL credentials out
func TestPreview_NoSecrets(t *testing.T) {
	prepared, _ := prepareWebhook(t,
		`{"url":"https://user:pass@example.com/hook?token=secret-token&x=1","headers":{"X-Token":"header-secret"},"body":"body-secret"}`,
		nil, channel.RenderStrict)
	preview := prepared.Preview()
	if preview.ID != keyPreviewHTTP {
		t.Fatalf("Preview ID = %q, want %q", preview.ID, keyPreviewHTTP)
	}
	if preview.Data["Method"] != "POST" || preview.Data["Format"] != formatText ||
		preview.Data["Bytes"] != len("body-secret") || preview.Data["Targets"] != 1 {
		t.Fatalf("Preview Data = %v", preview.Data)
	}
	text := strings.Join([]string{
		preview.ID,
		preview.Data["Method"].(string),
		preview.Data["URL"].(string),
		preview.Data["Format"].(string),
	}, " ")
	for _, secret := range []string{"header-secret", "body-secret", "secret-token", "user", "pass"} {
		if strings.Contains(text, secret) {
			t.Fatalf("preview %q leaked %q", text, secret)
		}
	}
}

// TestPrepare_Method 覆盖默认 POST, 小写大写化, 空白与非法 method
//
// TestPrepare_Method covers the POST default, an uppercase transform, whitespace and an invalid
// method
func TestPrepare_Method(t *testing.T) {
	t.Run("default post", func(t *testing.T) {
		prepared, _ := prepareWebhook(t, `{"url":"https://example.com"}`, nil, channel.RenderStrict)
		if got := prepared.(*preparedChannel).method; got != http.MethodPost {
			t.Fatalf("method = %q, want POST", got)
		}
	})
	t.Run("trim and uppercase", func(t *testing.T) {
		prepared, _ := prepareWebhook(t, `{"url":"https://example.com","method":" put "}`, nil, channel.RenderStrict)
		if got := prepared.(*preparedChannel).method; got != http.MethodPut {
			t.Fatalf("method = %q, want PUT", got)
		}
	})
	t.Run("template method", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("verb", "delete")
		prepared, _ := prepareWebhook(t, `{"url":"https://example.com","method":"{{verb}}"}`, vars, channel.RenderStrict)
		if got := prepared.(*preparedChannel).method; got != http.MethodDelete {
			t.Fatalf("method = %q, want DELETE", got)
		}
	})
	t.Run("invalid method", func(t *testing.T) {
		problem, permanent := prepareProblem(t, `{"url":"https://example.com","method":"BAD METHOD"}`, nil, channel.RenderStrict)
		if problem.ID != keyErrorInvalidMethod || !permanent {
			t.Fatalf("got ID=%q permanent=%v", problem.ID, permanent)
		}
		if problem.Data["Method"] != "BAD METHOD" {
			t.Fatalf("Data = %v, want Method", problem.Data)
		}
	})
}

// TestPrepare_ContentTypeWithoutBody 覆盖只有 headers 声明 Content-Type 时该头仍保留
//
// TestPrepare_ContentTypeWithoutBody covers a Content-Type declared in headers surviving without a
// body
func TestPrepare_ContentTypeWithoutBody(t *testing.T) {
	prepared, _ := prepareWebhook(t,
		`{"url":"https://example.com","method":"GET","headers":{"Content-Type":"text/plain"}}`,
		nil, channel.RenderStrict)
	p := prepared.(*preparedChannel)
	if got := p.req.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	if p.bytes != 0 || p.format != formatNone {
		t.Fatalf("format=%q bytes=%d, want none and 0", p.format, p.bytes)
	}
}

// TestPrepare_JSONWithoutBody 覆盖最终 Content-Type 为 JSON 而正文为空时按非法 JSON 拒绝
//
// TestPrepare_JSONWithoutBody covers an empty body being rejected as invalid JSON when the final
// Content-Type is JSON
func TestPrepare_JSONWithoutBody(t *testing.T) {
	problem, permanent := prepareProblem(t,
		`{"url":"https://example.com","headers":{"Content-Type":"application/json"}}`,
		nil, channel.RenderStrict)
	if problem.ID != keyErrorInvalidJSONBody || !permanent {
		t.Fatalf("got ID=%q permanent=%v", problem.ID, permanent)
	}
}

// TestPrepare_FormEncoding 覆盖表单字段的百分号编码与字段顺序
//
// TestPrepare_FormEncoding covers the percent encoding and field order of a form body
func TestPrepare_FormEncoding(t *testing.T) {
	vars := template.NewVars()
	vars.Set("value", "a b&c")
	prepared, _ := prepareWebhook(t,
		`{"url":"https://example.com","body":{"format":"form","fields":{"z":"1","a":"{{value}}"}}}`,
		vars, channel.RenderStrict)
	got := string(prepared.(*preparedChannel).body)
	if want := "a=a+b%26c&z=1"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// TestErrors_HaveKeys 覆盖每个错误, 警告与预览键在 en/zh-CN 都存在且占位符一致
//
// TestErrors_HaveKeys covers every error, warning and preview key existing in both catalogs with
// matching placeholders
func TestErrors_HaveKeys(t *testing.T) {
	en := readCatalog(t, "../../i18n/catalog/en/channel_webhook.json")
	zh := readCatalog(t, "../../i18n/catalog/zh-CN/channel_webhook.json")

	expected := []string{
		keyDesc,
		keyFieldURL, keyFieldMethod, keyFieldHeaders, keyFieldBody,
		keyFieldBodyFormat, keyFieldBodyJSON, keyFieldBodyText, keyFieldBodyFields, keyFieldBodyContentType,
		keyFieldMaskSecrets,
		keyErrorMissingURL, keyErrorInvalidURL, keyErrorInvalidMethod,
		keyErrorInvalidHeaderName, keyErrorInvalidHeaderVal, keyErrorDuplicateHeader, keyErrorSpecialHeader,
		keyErrorBodyEmpty, keyErrorBodyFormat, keyErrorBodyJSONMissing, keyErrorInvalidJSONBody,
		keyErrorContentLength, keyErrorTransferEncoding, keyErrorMaskEmpty,
		keyWarnUnresolved, keyWarnMaskMiss, keyWarnMaskAllMiss, keyPreviewHTTP,
	}
	sort.Strings(expected)

	enKeys := sortedKeys(en)
	zhKeys := sortedKeys(zh)
	if strings.Join(enKeys, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("en keys = %v, want %v", enKeys, expected)
	}
	if strings.Join(zhKeys, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("zh-CN keys = %v, want %v", zhKeys, expected)
	}

	enPlaceholders := placeholderSet(en)
	zhPlaceholders := placeholderSet(zh)
	if strings.Join(enPlaceholders, ",") != strings.Join(zhPlaceholders, ",") {
		t.Fatalf("en placeholders = %v, zh-CN = %v", enPlaceholders, zhPlaceholders)
	}

	// 预览的占位符必须覆盖契约里列出的五个键, Data 键名与之一致
	wantPreview := []string{"Bytes", "Format", "Method", "URL"}
	for _, name := range wantPreview {
		if !contains(enPlaceholders, name) {
			t.Fatalf("preview placeholders %v lack %q", enPlaceholders, name)
		}
	}
}

// readCatalog 读取并解析一份 catalog 文件
//
// readCatalog reads and parses one catalog file
func readCatalog(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// sortedKeys 返回 map 的排序键列表
//
// sortedKeys returns the sorted key list of a map
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var placeholderPattern = regexp.MustCompile(`\{\{\.([A-Za-z0-9_]+)\}\}`)

// placeholderSet 返回 catalog 文案里的占位符集合, 排序去重
//
// placeholderSet returns the sorted deduplicated placeholder set of the catalog copy
func placeholderSet(m map[string]string) []string {
	seen := map[string]bool{}
	for _, text := range m {
		for _, match := range placeholderPattern.FindAllStringSubmatch(text, -1) {
			seen[match[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// contains 报告切片是否包含目标字符串
//
// contains reports whether the slice holds the target string
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
