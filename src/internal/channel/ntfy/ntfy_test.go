package ntfy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// ntfy 渠道测试: Create 的严格解码与 priority/tags 类型, Fields 完整性, Prepare 的 topic 判定,
// 正文与标题组合, 4096 字节截断, priority 枚举与范围, tags 归一, header 注入与 token header,
// Send 的状态码判定与永久性, Preview 的脱敏
// 网络用例全部走本地 httptest 服务器, 域名一律用 example 占位, 不连公网
//
// Tests for the ntfy channel: strict decoding and the priority and tags types in Create, the
// completeness of Fields, and in Prepare the topic verdict, the body and title combinations, the
// 4096 byte cut, the priority enum and range, tag normalisation, a header injection and the token
// header; plus the status verdicts and permanence in Send and the redaction of Preview
// Every network case runs against a local httptest server, hosts are example placeholders and the
// public internet is never reached
// ---------------------------------------------------------------------------

// topicURL 是形态合法的示例 topic 地址, 域名为占位值
//
// topicURL is a syntactically valid sample topic address whose host is a placeholder
const topicURL = "https://ntfy.example.com/taskpush"

// exampleToken 是进入 Authorization 的占位 token, 用于检查它不会出现在错误与预览里
//
// exampleToken is the placeholder token going into Authorization, used to check it never shows up in
// an error or a preview
const exampleToken = "secret-token-example"

// record 是一次 httptest 交互的观测结果
//
// record is what one httptest exchange was observed to carry
type record struct {
	method     string
	path       string
	body       string
	headers    http.Header
	authHeader string
}

// newRecordServer 起一个记录请求细节的 httptest 服务器, 由 status 决定响应码与响应体
// 调用方用 t.Cleanup 关闭, 这里不额外注册以免遗漏
//
// newRecordServer starts an httptest server that records the request details, with status deciding
// the status code and the response body
// The caller registers the close through t.Cleanup
func newRecordServer(t *testing.T, status int, respBody string) (*httptest.Server, *record) {
	t.Helper()
	rec := &record{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.body = string(body)
		rec.headers = r.Header.Clone()
		rec.authHeader = r.Header.Get("Authorization")
		if respBody != "" {
			w.Header().Set("Content-Type", "text/plain")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// newTestChannel 构造使用指定 settings 的渠道实例
//
// newTestChannel builds a channel instance with the given settings
func newTestChannel(t *testing.T, settingsJSON string) channel.Channel {
	t.Helper()
	ch, err := channel.Create("nt", "ntfy", json.RawMessage(settingsJSON))
	if err != nil {
		t.Fatalf("channel.Create: %v", err)
	}
	return ch
}

// prepare 以严格模式渲染一条消息
//
// prepare renders one message under the strict mode
func prepare(ch channel.Channel, title, body string) (channel.Prepared, []apperr.Problem, error) {
	return prepareVars(ch, title, body, nil, channel.RenderStrict)
}

// prepareVars 以指定模式与变量渲染一条消息
//
// prepareVars renders one message with the given variables and mode
func prepareVars(ch channel.Channel, title, body string, vars *template.Vars, mode channel.RenderMode) (channel.Prepared, []apperr.Problem, error) {
	if vars == nil {
		vars = template.NewVars()
	}
	return ch.Prepare(context.Background(), channel.Message{
		Title: title,
		Body:  body,
		Vars:  vars,
		Mode:  mode,
	})
}

// mustProblem 取出 err 链上的 *apperr.Problem
//
// mustProblem extracts the *apperr.Problem from the err chain
func mustProblem(t *testing.T, err error) *apperr.Problem {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error %T (%v) is not *apperr.Problem", err, err)
	}
	return prob
}

// preparedOf 断言 Prepare 结果的具体类型
//
// preparedOf asserts the concrete type of a Prepare result
func preparedOf(t *testing.T, prepared channel.Prepared) *preparedChannel {
	t.Helper()
	p, ok := prepared.(*preparedChannel)
	if !ok {
		t.Fatalf("Prepare returned %T, want *preparedChannel", prepared)
	}
	return p
}

// TestFactoryRegistration 覆盖 init 注册与工厂元数据
//
// TestFactoryRegistration covers the init registration and the factory metadata
func TestFactoryRegistration(t *testing.T) {
	if !channel.IsRegistered("ntfy") {
		t.Fatal("ntfy is not registered")
	}
	var f channel.Factory = factory{}
	if f.Type() != "ntfy" {
		t.Fatalf("Type() = %q, want ntfy", f.Type())
	}
	if f.Describe() != keyDesc {
		t.Fatalf("Describe() = %q, want %q", f.Describe(), keyDesc)
	}
}

// TestFactory_Fields 覆盖字段元数据的完整性与稳定性
//
// TestFactory_Fields covers the completeness and the stability of the field metadata
func TestFactory_Fields(t *testing.T) {
	fields := factory{}.Fields()
	want := []struct {
		name, typ string
		required  bool
	}{
		{"url", "string", true},
		{"token", "string", false},
		{"priority", "int", false},
		{"tags", "list<string>", false},
	}
	if len(fields) != len(want) {
		t.Fatalf("Fields() returned %d fields, want %d", len(fields), len(want))
	}
	for i, w := range want {
		got := fields[i]
		if got.Name != w.name || got.Type != w.typ || got.Required != w.required {
			t.Errorf("field %d = {%s %s required=%v}, want {%s %s required=%v}",
				i, got.Name, got.Type, got.Required, w.name, w.typ, w.required)
		}
		if got.Default != "" {
			t.Errorf("field %s: Default = %q, want empty", got.Name, got.Default)
		}
		if got.DescriptionKey == "" || !strings.HasPrefix(got.DescriptionKey, "channel.ntfy.field.") {
			t.Errorf("field %s: unexpected DescriptionKey %q", got.Name, got.DescriptionKey)
		}
	}
}

// TestCreate_MissingURL 覆盖 url 缺失与只有空白的 url
//
// TestCreate_MissingURL covers a missing url and a url that is blank space only
func TestCreate_MissingURL(t *testing.T) {
	for _, raw := range []string{`{}`, `{"token":"example"}`, `{"url":"   "}`} {
		_, err := factory{}.Create("nt", json.RawMessage(raw))
		if err == nil {
			t.Fatalf("%s: expected an error, got nil", raw)
		}
		if !apperr.IsPermanent(err) {
			t.Errorf("%s: error is not permanent: %v", raw, err)
		}
		if prob := mustProblem(t, err); prob.ID != keyErrorMissingURL {
			t.Errorf("%s: problem ID = %q, want %q", raw, prob.ID, keyErrorMissingURL)
		}
	}
}

// TestCreate_Strict 覆盖未知字段, 字段类型错误与非对象 settings
// priority 的布尔与数组写法属于类型错误, tags 的数字写法由 settings.StringList 拒绝
//
// TestCreate_Strict covers an unknown field, wrong field types and non-object settings
// The boolean and array forms of priority are type errors, and a number for tags is refused by
// settings.StringList
func TestCreate_Strict(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"unknown key", `{"url":"` + topicURL + `","nope":true}`},
		{"priority bool", `{"url":"` + topicURL + `","priority":true}`},
		{"priority array", `{"url":"` + topicURL + `","priority":["high"]}`},
		{"priority object", `{"url":"` + topicURL + `","priority":{"v":1}}`},
		{"priority float", `{"url":"` + topicURL + `","priority":4.5}`},
		{"tags number", `{"url":"` + topicURL + `","tags":3}`},
		{"tags element number", `{"url":"` + topicURL + `","tags":["ci",2]}`},
		{"settings array", `[{"url":"` + topicURL + `"}]`},
		{"trailing content", `{"url":"` + topicURL + `"} {"url":"` + topicURL + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := factory{}.Create("nt", json.RawMessage(tc.raw))
			if err == nil {
				t.Fatalf("settings %s accepted, want an error", tc.raw)
			}
			if !apperr.IsPermanent(err) {
				t.Errorf("error is not permanent: %v", err)
			}
			prob := mustProblem(t, err)
			// priority 的类型错误落在自己的键上, 其余落在 channel.error.invalid_settings
			//
			// A priority type error lands on its own key while the rest land on
			// channel.error.invalid_settings
			want := "channel.error.invalid_settings"
			if strings.Contains(tc.name, "priority ") {
				want = keyErrorInvalidPriority
			}
			if prob.ID != want {
				t.Errorf("problem ID = %q, want %q", prob.ID, want)
			}
		})
	}
}

// TestCreate_PriorityForms 覆盖 priority 的 JSON 数字, 数字字符串, 枚举名与模板四种写法
// Create 只归一类型与写法, 区间与枚举判定留到 Prepare, 使静态写法与模板写法走同一条校验路径
//
// TestCreate_PriorityForms covers the four priority spellings: a JSON number, a numeric string, an
// enum name and a template
// Create only normalises the type and the spelling, since the range and enum verdicts wait for
// Prepare, which keeps a static spelling and a template on one validation path
func TestCreate_PriorityForms(t *testing.T) {
	cases := []struct {
		raw  string
		want prioritySetting
	}{
		{`{"url":"` + topicURL + `","priority":4}`, prioritySetting{Given: true, Text: "4"}},
		{`{"url":"` + topicURL + `","priority":"3"}`, prioritySetting{Given: true, Text: "3"}},
		{`{"url":"` + topicURL + `","priority":"high"}`, prioritySetting{Given: true, Text: "high"}},
		{`{"url":"` + topicURL + `","priority":"{{prio}}"}`, prioritySetting{Given: true, Text: "{{prio}}"}},
		{`{"url":"` + topicURL + `","priority":" 3 "}`, prioritySetting{Given: true, Text: "3"}},
		{`{"url":"` + topicURL + `","priority":null}`, prioritySetting{}},
		{`{"url":"` + topicURL + `"}`, prioritySetting{}},
	}
	for _, tc := range cases {
		ch, err := factory{}.Create("nt", json.RawMessage(tc.raw))
		if err != nil {
			t.Fatalf("%s: Create: %v", tc.raw, err)
		}
		c := ch.(*ntfyChannel)
		if c.s.Priority != tc.want {
			t.Errorf("%s: priority = %+v, want %+v", tc.raw, c.s.Priority, tc.want)
		}
	}
}

// TestCreate_TagsForms 覆盖 tags 的数组与逗号字符串两种写法, 以及全角分隔符
//
// TestCreate_TagsForms covers the array and delimited-string forms of tags, including the
// full-width separators
func TestCreate_TagsForms(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{`{"url":"` + topicURL + `","tags":["ci","build"]}`, []string{"ci", "build"}},
		{`{"url":"` + topicURL + `","tags":"ci,build"}`, []string{"ci", "build"}},
		{`{"url":"` + topicURL + `","tags":"ci，build、done "}`, []string{"ci", "build", "done"}},
		{`{"url":"` + topicURL + `","tags":["ci","","  "]}`, []string{"ci"}},
	}
	for _, tc := range cases {
		ch, err := factory{}.Create("nt", json.RawMessage(tc.raw))
		if err != nil {
			t.Fatalf("%s: Create: %v", tc.raw, err)
		}
		got := ch.(*ntfyChannel).s.Tags.Strings()
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: tags = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestPrepare_Topic 覆盖 topic 判定: 只有主机没有 path, 只有斜杠, 多级 path 与带 query 的地址
//
// TestPrepare_Topic covers the topic verdict: a host with no path, separators only, a multi-level
// path and an address carrying a query
func TestPrepare_Topic(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr string
		topic   string
	}{
		{"no path", "https://ntfy.example.com", keyErrorMissingTopic, ""},
		{"slash only", "https://ntfy.example.com/", keyErrorMissingTopic, ""},
		{"nested path", "https://ntfy.example.com/team/alerts", "", "alerts"},
		{"trailing slash", "https://ntfy.example.com/alerts/", "", "alerts"},
		{"with query", "https://ntfy.example.com/alerts?priority=high", "", "alerts"},
		{"missing scheme", "ntfy.example.com/alerts", "", "alerts"},
		{"loopback http", "localhost:8080/alerts", "", "alerts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newTestChannel(t, `{"url":"`+tc.url+`"}`)
			prepared, _, err := prepare(ch, "Build done", "commit abc123")
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected %s, got nil", tc.wantErr)
				}
				if !apperr.IsPermanent(err) {
					t.Errorf("error is not permanent: %v", err)
				}
				if prob := mustProblem(t, err); prob.ID != tc.wantErr {
					t.Errorf("problem ID = %q, want %q", prob.ID, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got := preparedOf(t, prepared).topic; got != tc.topic {
				t.Errorf("topic = %q, want %q", got, tc.topic)
			}
		})
	}
}

// TestPrepare_InvalidURL 覆盖非法地址报 invalid_url, 且 Data.URL 已打码, 不出现原样地址
// 非法 scheme 由 common.NormalizeURL 拒绝, 因此错误发生在 topic 判定之前
//
// TestPrepare_InvalidURL covers an invalid address reporting invalid_url with Data.URL masked so the
// address as written cannot appear
// An illegal scheme is refused by common.NormalizeURL, so the error lands before the topic verdict
func TestPrepare_InvalidURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		peek string
	}{
		{"bad scheme", "ftp://ntfy.example.com/taskpush", "ftp://"},
		{"no host", "https:///taskpush", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newTestChannel(t, `{"url":"`+tc.url+`"}`)
			_, _, err := prepare(ch, "Build done", "commit abc123")
			prob := mustProblem(t, err)
			if prob.ID != keyErrorInvalidURL {
				t.Fatalf("problem ID = %q, want %q", prob.ID, keyErrorInvalidURL)
			}
			if !apperr.IsPermanent(err) {
				t.Error("error is not permanent")
			}
			if got, ok := prob.Data["URL"].(string); !ok || got == "" {
				t.Errorf("Data.URL = %v, want a masked address", prob.Data["URL"])
			}
			if tc.peek != "" && strings.Contains(prob.Error(), tc.peek) {
				t.Errorf("error text leaks the address %q: %v", tc.peek, prob.Error())
			}
		})
	}
}

// TestPrepare_Content 覆盖正文与标题的组合: 正文优先, 正文空时用标题, 两者都空报错
// 判定按渲染结果原样进行, 因此只有空白字符的正文视为非空并按原样发送
//
// TestPrepare_Content covers the content combinations: the body wins, the title stands in when the
// body is empty, and both empty is an error
// The test runs on the text as rendered, so a whitespace-only body counts as content and is sent
// as written
func TestPrepare_Content(t *testing.T) {
	t.Run("body wins", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
		prepared, _, err := prepare(ch, "Build done", "commit abc123")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := string(preparedOf(t, prepared).body); got != "commit abc123" {
			t.Errorf("body = %q, want the body text", got)
		}
	})

	t.Run("title stands in", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
		prepared, _, err := prepare(ch, "Build done", "")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		p := preparedOf(t, prepared)
		if got := string(p.body); got != "Build done" {
			t.Errorf("body = %q, want the title text", got)
		}
		// 标题同时进入 header, 但不拼进正文
		//
		// The title goes into the header as well while staying out of the body
		if got := p.req.Header.Get("Title"); got != "Build done" {
			t.Errorf("Title header = %q, want the title", got)
		}
	})

	t.Run("whitespace body kept", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
		prepared, _, err := prepare(ch, "Build done", " \n ")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := string(preparedOf(t, prepared).body); got != " \n " {
			t.Errorf("body = %q, want the whitespace as written", got)
		}
	})

	t.Run("both empty", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
		_, _, err := prepare(ch, "", "")
		prob := mustProblem(t, err)
		if prob.ID != keyErrorEmptyContent {
			t.Errorf("problem ID = %q, want %q", prob.ID, keyErrorEmptyContent)
		}
		if !apperr.IsPermanent(err) {
			t.Error("error is not permanent")
		}
	})

	t.Run("no title no header", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
		prepared, _, err := prepare(ch, "", "commit abc123")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := preparedOf(t, prepared).req.Header.Get("Title"); got != "" {
			t.Errorf("Title header = %q, want it absent", got)
		}
	})
}

// TestPrepare_Truncate 覆盖 4096 字节截断与警告, 以及切点回退到 UTF-8 字符边界
//
// TestPrepare_Truncate covers the 4096 byte cut with its warning and the cut point stepping back to
// a UTF-8 character boundary
func TestPrepare_Truncate(t *testing.T) {
	t.Run("ascii overshoot", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
		body := strings.Repeat("a", bodyLimitBytes+900)
		prepared, warns, err := prepare(ch, "Build done", body)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		p := preparedOf(t, prepared)
		if len(p.body) != bodyLimitBytes {
			t.Fatalf("body length = %d, want %d", len(p.body), bodyLimitBytes)
		}
		if got := p.req.ContentLength; got != int64(bodyLimitBytes) {
			t.Errorf("ContentLength = %d, want %d", got, bodyLimitBytes)
		}
		if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
			t.Fatalf("warnings = %+v, want one %s", warns, keyWarnTruncated)
		}
		if warns[0].Data["Limit"] != bodyLimitBytes {
			t.Errorf("warning Limit = %v, want %d", warns[0].Data["Limit"], bodyLimitBytes)
		}
	})

	t.Run("multibyte boundary", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
		body := strings.Repeat("啊", 2000) // 6000 字节 / 6000 bytes
		prepared, warns, err := prepare(ch, "", body)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		p := preparedOf(t, prepared)
		if len(p.body) >= len(body) {
			t.Fatalf("body was not cut: %d bytes", len(p.body))
		}
		if !utf8.Valid(p.body) {
			t.Errorf("truncated body is not valid UTF-8: %d bytes", len(p.body))
		}
		// 每个字符 3 字节, 截断后必须仍是完整字符的整数倍
		//
		// Every character takes 3 bytes, so the cut body has to hold a whole number of them
		if len(p.body)%3 != 0 {
			t.Errorf("body length = %d, want a whole number of 3-byte runes", len(p.body))
		}
		if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
			t.Fatalf("warnings = %+v, want the truncation warning", warns)
		}
	})

	t.Run("at the limit", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
		body := strings.Repeat("a", bodyLimitBytes)
		prepared, warns, err := prepare(ch, "", body)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if len(preparedOf(t, prepared).body) != bodyLimitBytes {
			t.Error("a body at the limit must stay whole")
		}
		for _, w := range warns {
			if w.ID == keyWarnTruncated {
				t.Error("a body at the limit must not warn about truncation")
			}
		}
	})
}

// TestPrepare_Priority 覆盖 priority 的枚举名, 数字区间与非法取值
// 枚举名比较大小写不敏感, 数字只接受 1-5, 未配置时不发 Priority header
//
// TestPrepare_Priority covers the priority enum names, the numeric range and an invalid value
// An enum name is compared case-insensitively, a number has to sit inside 1-5, and an unset field
// sends no Priority header
func TestPrepare_Priority(t *testing.T) {
	cases := []struct {
		name     string
		priority string
		want     string
		wantErr  bool
	}{
		{name: "min", priority: `"min"`, want: "min"},
		{name: "low", priority: `"low"`, want: "low"},
		{name: "default", priority: `"default"`, want: "default"},
		{name: "high", priority: `"high"`, want: "high"},
		{name: "max", priority: `"max"`, want: "max"},
		{name: "upper case", priority: `"HIGH"`, want: "HIGH"},
		{name: "number 1", priority: `1`, want: "1"},
		{name: "number 5", priority: `5`, want: "5"},
		{name: "numeric string", priority: `" 3 "`, want: "3"},
		{name: "zero", priority: `0`, wantErr: true},
		{name: "six", priority: `6`, wantErr: true},
		{name: "negative", priority: `-1`, wantErr: true},
		{name: "unknown enum", priority: `"urgent"`, wantErr: true},
		{name: "not a number", priority: `"42x"`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"url":"` + topicURL + `","priority":` + tc.priority + `}`
			ch := newTestChannel(t, raw)
			prepared, _, err := prepare(ch, "Build done", "commit abc123")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("priority %s accepted, want an error", tc.priority)
				}
				prob := mustProblem(t, err)
				if prob.ID != keyErrorInvalidPriority {
					t.Errorf("problem ID = %q, want %q", prob.ID, keyErrorInvalidPriority)
				}
				if !apperr.IsPermanent(err) {
					t.Error("error is not permanent")
				}
				if got, ok := prob.Data["Value"].(string); !ok || got == "" {
					t.Errorf("Data.Value = %v, want the rejected value", prob.Data["Value"])
				}
				return
			}
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got := preparedOf(t, prepared).req.Header.Get("Priority"); got != tc.want {
				t.Errorf("Priority header = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("unset sends nothing", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
		prepared, _, err := prepare(ch, "Build done", "commit abc123")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := preparedOf(t, prepared).req.Header.Get("Priority"); got != "" {
			t.Errorf("Priority header = %q, want it absent", got)
		}
	})

	t.Run("renders empty", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("prio", "")
		ch := newTestChannel(t, `{"url":"`+topicURL+`","priority":"{{prio}}"}`)
		prepared, _, err := prepareVars(ch, "Build done", "commit abc123", vars, channel.RenderStrict)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := preparedOf(t, prepared).req.Header.Get("Priority"); got != "" {
			t.Errorf("Priority header = %q, want it absent", got)
		}
	})

	t.Run("from a variable", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("prio", "high")
		ch := newTestChannel(t, `{"url":"`+topicURL+`","priority":"{{prio}}"}`)
		prepared, _, err := prepareVars(ch, "Build done", "commit abc123", vars, channel.RenderStrict)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := preparedOf(t, prepared).req.Header.Get("Priority"); got != "high" {
			t.Errorf("Priority header = %q, want high", got)
		}
	})

	t.Run("out of range from a variable", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("prio", "9")
		ch := newTestChannel(t, `{"url":"`+topicURL+`","priority":"{{prio}}"}`)
		_, _, err := prepareVars(ch, "Build done", "commit abc123", vars, channel.RenderStrict)
		prob := mustProblem(t, err)
		if prob.ID != keyErrorInvalidPriority {
			t.Errorf("problem ID = %q, want %q", prob.ID, keyErrorInvalidPriority)
		}
		if prob.Data["Value"] != "9" {
			t.Errorf("Data.Value = %v, want 9", prob.Data["Value"])
		}
	})
}

// TestPrepare_Tags 覆盖 tags 的归一: 逐项渲染, 按逗号类分隔符切分, 去空白与空项, 半角逗号连接
// 数组与字符串写法归一后一致, 变量整体提供列表时同样被切开
//
// TestPrepare_Tags covers tag normalisation: rendering item by item, cutting on the comma-like
// separators, dropping whitespace and empty items and joining with an ASCII comma
// An array and a delimited string normalise alike, and a variable supplying the whole list is cut
// the same way
func TestPrepare_Tags(t *testing.T) {
	t.Run("array", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`","tags":["ci","build"]}`)
		prepared, _, err := prepare(ch, "Build done", "commit abc123")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := preparedOf(t, prepared).req.Header.Get("Tags"); got != "ci,build" {
			t.Errorf("Tags header = %q, want ci,build", got)
		}
	})

	t.Run("delimiter mix", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`","tags":[" ci , build ","done，late、"]}`)
		prepared, _, err := prepare(ch, "Build done", "commit abc123")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := preparedOf(t, prepared).req.Header.Get("Tags"); got != "ci,build,done,late" {
			t.Errorf("Tags header = %q, want ci,build,done,late", got)
		}
	})

	t.Run("from a variable list", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("tags", "ci,build")
		ch := newTestChannel(t, `{"url":"`+topicURL+`","tags":["{{tags}}"]}`)
		prepared, _, err := prepareVars(ch, "Build done", "commit abc123", vars, channel.RenderStrict)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := preparedOf(t, prepared).req.Header.Get("Tags"); got != "ci,build" {
			t.Errorf("Tags header = %q, want ci,build", got)
		}
	})

	t.Run("all empty", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`","tags":["   "]}`)
		prepared, _, err := prepare(ch, "Build done", "commit abc123")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := preparedOf(t, prepared).req.Header.Get("Tags"); got != "" {
			t.Errorf("Tags header = %q, want it absent", got)
		}
	})
}

// TestPrepare_Headers 覆盖 header 注入与 token header
// 含 CR/LF 的标题, token 与标签都必须被拒, 因为那会让 net/http 写出额外 header 或分裂请求
// token 非空时写 Bearer, 前后空白由 TrimSpace 去掉
//
// TestPrepare_Headers covers a header injection and the token header
// A title, token or tag holding CR or LF has to be refused, since net/http would write an extra
// header or split the request
// A non-empty token is sent as Bearer with its surrounding space trimmed
func TestPrepare_Headers(t *testing.T) {
	t.Run("token header", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`","token":"  secret-token-example  "}`)
		prepared, _, err := prepare(ch, "Build done", "commit abc123")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		p := preparedOf(t, prepared)
		if got := p.req.Header.Get("Authorization"); got != "Bearer secret-token-example" {
			t.Errorf("Authorization = %q, want the bearer form", got)
		}
		if p.token != "secret-token-example" {
			t.Errorf("recorded token = %q, want the trimmed form", p.token)
		}
	})

	t.Run("empty token", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`","token":"   "}`)
		prepared, _, err := prepare(ch, "Build done", "commit abc123")
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := preparedOf(t, prepared).req.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want it absent", got)
		}
	})

	injections := []struct {
		name   string
		raw    string
		title  string
		expect string
	}{
		{
			name:   "title with CR",
			raw:    `{"url":"` + topicURL + `"}`,
			title:  "Build\rDone",
			expect: headerTitle,
		},
		{
			name:   "title with LF",
			raw:    `{"url":"` + topicURL + `"}`,
			title:  "Build\nDone",
			expect: headerTitle,
		},
		{
			name:   "title with CRLF and an injected header",
			raw:    `{"url":"` + topicURL + `"}`,
			title:  "Build done\r\nX-Evil: yes",
			expect: headerTitle,
		},
		{
			name:   "token with CRLF and an injected header",
			raw:    `{"url":"` + topicURL + `","token":"` + exampleToken + `\r\nX-Evil: yes"}`,
			title:  "Build done",
			expect: headerAuthorization,
		},
		{
			name:   "tag with CR",
			raw:    `{"url":"` + topicURL + `","tags":["ci\u000dbuild"]}`,
			title:  "Build done",
			expect: headerTags,
		},
		{
			name:   "tag with NUL",
			raw:    `{"url":"` + topicURL + `","tags":["ci\u0000build"]}`,
			title:  "Build done",
			expect: headerTags,
		},
	}
	for _, tc := range injections {
		t.Run(tc.name, func(t *testing.T) {
			ch := newTestChannel(t, tc.raw)
			_, _, err := prepare(ch, tc.title, "commit abc123")
			if err == nil {
				t.Fatal("expected a header error, got nil")
			}
			if !apperr.IsPermanent(err) {
				t.Error("error is not permanent")
			}
			prob := mustProblem(t, err)
			if prob.ID != keyErrorInvalidHeader {
				t.Fatalf("problem ID = %q, want %q", prob.ID, keyErrorInvalidHeader)
			}
			if prob.Data["Name"] != tc.expect {
				t.Errorf("Data.Name = %v, want %q", prob.Data["Name"], tc.expect)
			}
			// Data 只放 header 名, 不放被拒的值
			//
			// Data names the header only, never the rejected value
			if strings.Contains(prob.Error(), "\n") {
				t.Errorf("error text carries a raw newline: %q", prob.Error())
			}
		})
	}
}

// TestPrepare_RenderMode 覆盖两种渲染模式下的未解析变量处理
// Strict 报错, Check 追加警告并保留字面文本继续构造请求
//
// TestPrepare_RenderMode covers both render modes for an unresolved variable
// Strict reports an error while Check appends a warning and keeps the literal text going into the
// request
func TestPrepare_RenderMode(t *testing.T) {
	t.Run("strict", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"{{url}}"}`)
		_, _, err := prepareVars(ch, "Build done", "commit abc123", template.NewVars(), channel.RenderStrict)
		if err == nil {
			t.Fatal("expected an unresolved-variable error, got nil")
		}
		if !apperr.IsPermanent(err) {
			t.Error("error is not permanent")
		}
		prob := mustProblem(t, err)
		if prob.Data["Path"] != settingsPathURL {
			t.Errorf("Data.Path = %v, want %q", prob.Data["Path"], settingsPathURL)
		}
	})

	t.Run("check", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`","priority":"{{prio}}"}`)
		_, warns, err := prepareVars(ch, "Build done", "commit abc123", template.NewVars(), channel.RenderCheck)
		// 未解析的 priority 字面文本 "{{prio}}" 不是合法枚举, 因此报 invalid_priority, 而告警仍带回字段路径
		//
		// The unresolved literal "{{prio}}" is not a legal enum name, so invalid_priority is reported
		// while the warning still carries the field path
		prob := mustProblem(t, err)
		if prob.ID != keyErrorInvalidPriority {
			t.Errorf("problem ID = %q, want %q", prob.ID, keyErrorInvalidPriority)
		}
		var found bool
		for _, w := range warns {
			if w.Data["Path"] == settingsPathPriority {
				found = true
			}
		}
		if !found {
			t.Errorf("no warning carries Path %q: %+v", settingsPathPriority, warns)
		}
	})

	t.Run("check keeps the literal in the url", func(t *testing.T) {
		ch := newTestChannel(t, `{"url":"`+topicURL+`","tags":["{{tag}}"]}`)
		prepared, warns, err := prepareVars(ch, "Build done", "commit abc123", template.NewVars(), channel.RenderCheck)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		var found bool
		for _, w := range warns {
			if w.Data["Path"] == settingsPathTags+".0" {
				found = true
			}
		}
		if !found {
			t.Errorf("no warning carries Path %q: %+v", settingsPathTags+".0", warns)
		}
		// Check 模式保留字面文本, 因此 Tags header 里出现的是未解析的原文
		//
		// Check keeps the literal text, so the unresolved spelling reaches the Tags header
		if got := preparedOf(t, prepared).req.Header.Get("Tags"); got != "{{tag}}" {
			t.Errorf("Tags header = %q, want the literal template text", got)
		}
	})
}

// TestPrepare_RequestShape 覆盖请求构造: POST, 地址, Content-Type 与标题 header
// Prepare 不发网络, 因此这里只检查已构造好的请求
//
// TestPrepare_RequestShape covers request construction: the method, the address, Content-Type and
// the title header
// Prepare sends nothing, so only the constructed request is inspected here
func TestPrepare_RequestShape(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+topicURL+`","token":"secret-token-example","priority":"high","tags":["ci","build"]}`)
	prepared, _, err := prepare(ch, "Build done", "commit abc123")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := preparedOf(t, prepared)
	req := p.req
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != topicURL {
		t.Errorf("url = %q, want %q", req.URL.String(), topicURL)
	}
	if got := req.Header.Get("Content-Type"); got != contentTypeText {
		t.Errorf("Content-Type = %q, want %q", got, contentTypeText)
	}
	for header, want := range map[string]string{
		"Title":         "Build done",
		"Priority":      "high",
		"Tags":          "ci,build",
		"Authorization": "Bearer secret-token-example",
	} {
		if got := req.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// TestSend 覆盖 200, 204 与空 body 成功, 400 永久, 429 与 500 可重试, 且响应摘要与永久性分类
// 都落在渠道自己的错误键上
//
// TestSend covers a 200, a 204 and an empty body as successes, a 400 as permanent, and 429 and 500
// as retryable, with the response summary and the permanence class landing on the channel key
func TestSend(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		respBody  string
		wantErr   bool
		permanent bool
	}{
		{name: "200 empty", status: http.StatusOK},
		{name: "200 json", status: http.StatusOK, respBody: `{"id":"example"}`},
		{name: "204 no content", status: http.StatusNoContent},
		{name: "400 bad request", status: http.StatusBadRequest, respBody: "topic name invalid", permanent: true},
		{name: "429 throttled", status: http.StatusTooManyRequests, respBody: "slow down", wantErr: true},
		{name: "500 server error", status: http.StatusInternalServerError, respBody: "boom", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := newRecordServer(t, tc.status, tc.respBody)
			ch := newTestChannel(t, `{"url":"`+srv.URL+`/taskpush","token":"secret-token-example"}`)
			prepared, _, err := prepare(ch, "Build done", "commit abc123")
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			sendErr := preparedOf(t, prepared).Send(context.Background(), srv.Client())

			if !tc.wantErr && !tc.permanent && sendErr == nil {
				// 成功路径继续检查请求内容
				//
				// The success path keeps checking what was sent
			} else if sendErr == nil {
				t.Fatalf("status %d accepted, want an error", tc.status)
			} else if tc.permanent != apperr.IsPermanent(sendErr) {
				t.Errorf("permanent = %v, want %v (%v)", apperr.IsPermanent(sendErr), tc.permanent, sendErr)
			}
			if tc.wantErr || tc.permanent {
				prob := mustProblem(t, sendErr)
				if prob.ID != keyErrorHTTPStatus {
					t.Errorf("problem ID = %q, want %q", prob.ID, keyErrorHTTPStatus)
				}
				if prob.Data["Status"] != tc.status {
					t.Errorf("Data.Status = %v, want %d", prob.Data["Status"], tc.status)
				}
				if tc.respBody != "" && !strings.Contains(prob.Data["Body"].(string), tc.respBody) {
					t.Errorf("Data.Body = %q, want it to carry %q", prob.Data["Body"], tc.respBody)
				}
				return
			}
			if sendErr != nil {
				t.Fatalf("Send: %v", sendErr)
			}

			if rec.method != http.MethodPost {
				t.Errorf("method sent = %q, want POST", rec.method)
			}
			if rec.path != "/taskpush" {
				t.Errorf("path sent = %q, want /taskpush", rec.path)
			}
			if rec.body != "commit abc123" {
				t.Errorf("body sent = %q, want the message text", rec.body)
			}
			if rec.headers.Get("Content-Type") != contentTypeText {
				t.Errorf("Content-Type sent = %q, want %q", rec.headers.Get("Content-Type"), contentTypeText)
			}
			if rec.authHeader != "Bearer secret-token-example" {
				t.Errorf("Authorization sent = %q, want the bearer form", rec.authHeader)
			}
			if rec.headers.Get("Title") != "Build done" {
				t.Errorf("Title sent = %q, want the message title", rec.headers.Get("Title"))
			}
		})
	}
}

// TestSend_RetryRebuildsBody 覆盖同一 Prepared 重复 Send 时正文仍可读出
// 第一次失败后调度层重试, 第二次必须带着同样的正文到达
//
// TestSend_RetryRebuildsBody covers a repeated Send on one Prepared still carrying its content
// After a first failure the scheduling layer retries, and the second attempt has to arrive with the
// same body
func TestSend_RetryRebuildsBody(t *testing.T) {
	var (
		calls  int
		bodies []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls++
		bodies = append(bodies, string(body))
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ch := newTestChannel(t, `{"url":"`+srv.URL+`/taskpush"}`)
	prepared, _, err := prepare(ch, "Build done", "commit abc123")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := preparedOf(t, prepared)
	if err := p.Send(context.Background(), srv.Client()); err == nil {
		t.Fatal("the failing first attempt returned no error")
	}
	if err := p.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	// 成功后重复 Send 不再产生第三个请求
	//
	// After the success a repeated Send reaches no third request
	if err := p.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("third Send: %v", err)
	}
	if calls != 2 {
		t.Fatalf("server saw %d requests, want 2", calls)
	}
	for i, b := range bodies {
		if b != "commit abc123" {
			t.Errorf("attempt %d body = %q, want the message text", i+1, b)
		}
	}
}

// TestSend_NilClient 覆盖 client 为 nil 时报错而不是 panic
//
// TestSend_NilClient covers a nil client reporting an error instead of panicking
func TestSend_NilClient(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+topicURL+`"}`)
	prepared, _, err := prepare(ch, "Build done", "commit abc123")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := preparedOf(t, prepared).Send(context.Background(), nil); err == nil {
		t.Fatal("expected an error for a nil client, got nil")
	}
}

// TestSend_TruncatedBody 覆盖截断后的正文与警告一起生效: 服务端收到的字节数等于上限
//
// TestSend_TruncatedBody covers a cut body reaching the server at exactly the limit together with
// its warning
func TestSend_TruncatedBody(t *testing.T) {
	srv, rec := newRecordServer(t, http.StatusOK, "")
	ch := newTestChannel(t, `{"url":"`+srv.URL+`/taskpush"}`)
	prepared, warns, err := prepare(ch, "Build done", strings.Repeat("a", bodyLimitBytes+10))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %+v, want the truncation warning", warns)
	}
	if err := preparedOf(t, prepared).Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(rec.body) != bodyLimitBytes {
		t.Errorf("server received %d bytes, want %d", len(rec.body), bodyLimitBytes)
	}
}

// TestSend_ErrorRedaction 覆盖错误文本不含完整地址与 token
// 服务端回显的文案里带上 token 时, 摘要仍要遮蔽它
//
// TestSend_ErrorRedaction covers an error text holding neither the full address nor the token
// When server-echoed copy carries the token, the summary still has to hide it
func TestSend_ErrorRedaction(t *testing.T) {
	const token = "secret-token-example"
	srv, _ := newRecordServer(t, http.StatusBadRequest, "bad "+token+" at "+srvHostPlaceholder())
	ch := newTestChannel(t, `{"url":"`+srv.URL+`/taskpush","token":"`+token+`"}`)
	prepared, _, err := prepare(ch, "Build done", "commit abc123")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	sendErr := preparedOf(t, prepared).Send(context.Background(), srv.Client())
	if sendErr == nil {
		t.Fatal("expected a 400 error, got nil")
	}
	text := sendErr.Error()
	if strings.Contains(text, token) {
		t.Errorf("error text leaks the token: %q", text)
	}
	prob := mustProblem(t, sendErr)
	if body, _ := prob.Data["Body"].(string); strings.Contains(body, token) {
		t.Errorf("Data.Body leaks the token: %q", body)
	}
}

// srvHostPlaceholder 是响应文案里引用的示例地址前缀, 域名为占位值
//
// srvHostPlaceholder is the sample address prefix quoted in the response copy, with a placeholder
// host
func srvHostPlaceholder() string {
	return "https://ntfy.example.com"
}

// TestPreview 覆盖预览只带 topic 与字节数, 不含 token 与完整地址
//
// TestPreview covers the preview carrying the topic and the byte count alone, with neither the token
// nor the full address
func TestPreview(t *testing.T) {
	const token = "secret-token-example"
	ch := newTestChannel(t, `{"url":"`+topicURL+`?cache=0","token":"`+token+`","tags":["ci"]}`)
	prepared, _, err := prepare(ch, "Build done", "commit abc123")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	prob := prepared.Preview()
	if prob.ID != keyPreviewMessage {
		t.Errorf("preview ID = %q, want %q", prob.ID, keyPreviewMessage)
	}
	if prob.Data["Topic"] != "taskpush" {
		t.Errorf("Data.Topic = %v, want taskpush", prob.Data["Topic"])
	}
	if prob.Data["Bytes"] != len("commit abc123") {
		t.Errorf("Data.Bytes = %v, want %d", prob.Data["Bytes"], len("commit abc123"))
	}
	for _, leak := range []string{token, "ntfy.example.com", topicURL, "Bearer", "ci"} {
		if strings.Contains(prob.Error(), leak) {
			t.Errorf("preview leaks %q: %q", leak, prob.Error())
		}
	}
	for key := range prob.Data {
		if key != "Topic" && key != "Bytes" {
			t.Errorf("unexpected preview key %q", key)
		}
	}

	// 正文为空时用标题, 字节数跟着变成标题长度
	//
	// The title stands in for an empty body, so the byte count follows it
	ch2 := newTestChannel(t, `{"url":"https://ntfy.example.com/team/alerts"}`)
	prepared2, _, err := prepare(ch2, "Build done", "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	prob2 := prepared2.Preview()
	if prob2.Data["Topic"] != "alerts" {
		t.Errorf("Data.Topic = %v, want alerts", prob2.Data["Topic"])
	}
	if prob2.Data["Bytes"] != len("Build done") {
		t.Errorf("Data.Bytes = %v, want %d", prob2.Data["Bytes"], len("Build done"))
	}
}

// TestPreview_IndependentOfSend 覆盖 Preview 无副作用: 调用后仍能 Send
//
// TestPreview_IndependentOfSend covers Preview having no side effect, so a Send still follows it
func TestPreview_IndependentOfSend(t *testing.T) {
	srv, _ := newRecordServer(t, http.StatusOK, "")
	ch := newTestChannel(t, `{"url":"`+srv.URL+`/taskpush"}`)
	prepared, _, err := prepare(ch, "Build done", "commit abc123")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := preparedOf(t, prepared)
	_ = p.Preview()
	if err := p.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Send after Preview: %v", err)
	}
}

// TestChannel_IDAndType 覆盖实例标识与类型名
//
// TestChannel_IDAndType covers the instance identifier and the type name
func TestChannel_IDAndType(t *testing.T) {
	ch, err := channel.Create("alerts", "ntfy", json.RawMessage(`{"url":"`+topicURL+`"}`))
	if err != nil {
		t.Fatalf("channel.Create: %v", err)
	}
	if ch.ID() != "alerts" {
		t.Errorf("ID() = %q, want alerts", ch.ID())
	}
	if ch.Type() != "ntfy" {
		t.Errorf("Type() = %q, want ntfy", ch.Type())
	}
}
