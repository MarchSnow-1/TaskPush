package gotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// gotify 单元测试: Create 的严格解码与必填, Prepare 的端点拼接/内容映射/截断/priority/token,
// Send 的状态码分类, Preview 的脱敏与 catalog 键的一致性
// 网络用例一律指向本地 httptest 服务器, 不访问公网
//
// gotify unit tests: the strict decoding and required fields of Create, the endpoint join, content
// mapping, truncation, priority and token handling of Prepare, the status classification of Send, the
// redaction of Preview, and the consistency of the catalog keys
// Every network case points at a local httptest server, so no request leaves the machine
// ---------------------------------------------------------------------------

// exampleURL 是普通用例里的目标地址, 用通用占位域名
//
// exampleURL is the target address of the ordinary cases, using the generic example domain
const exampleURL = "https://gotify.example.com"

// newTestChannel 通过注册表创建一个 gotify 渠道, 顺带覆盖 Create 的注册路径
//
// newTestChannel builds one gotify channel through the registry, which also covers the registration
// path of Create
func newTestChannel(t *testing.T, settingsJSON string) channel.Channel {
	t.Helper()
	ch, err := channel.Create("gotify-main", "gotify", json.RawMessage(settingsJSON))
	if err != nil {
		t.Fatalf("channel.Create: %v", err)
	}
	return ch
}

// newChannel 直接调用工厂创建渠道, 用于断言 Create 返回的具体类型
//
// newChannel calls the factory directly, which lets a test assert the concrete type Create returns
func newChannel(t *testing.T, settingsJSON string) *gotifyChannel {
	t.Helper()
	ch, err := factory{}.Create("gotify-main", json.RawMessage(settingsJSON))
	if err != nil {
		t.Fatalf("factory.Create: %v", err)
	}
	concrete, ok := ch.(*gotifyChannel)
	if !ok {
		t.Fatalf("Create returned %T, want *gotifyChannel", ch)
	}
	return concrete
}

// prepareWith 以给定变量与模式执行 Prepare
//
// prepareWith runs Prepare with the supplied variables and render mode
func prepareWith(t *testing.T, ch channel.Channel, title, body string, vars *template.Vars, mode channel.RenderMode) (channel.Prepared, []apperr.Problem) {
	t.Helper()
	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{
		Title: title,
		Body:  body,
		Vars:  vars,
		Mode:  mode,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared, warns
}

// prepareStrict 以严格模式与空变量执行 Prepare
//
// prepareStrict runs Prepare under the strict mode with empty variables
func prepareStrict(t *testing.T, ch channel.Channel, title, body string) (channel.Prepared, []apperr.Problem) {
	t.Helper()
	return prepareWith(t, ch, title, body, template.NewVars(), channel.RenderStrict)
}

// prepareProblem 断言 Prepare 失败并返回错误链上的 Problem
//
// prepareProblem asserts that Prepare fails and returns the Problem on the error chain
func prepareProblem(t *testing.T, ch channel.Channel, title, body string, vars *template.Vars, mode channel.RenderMode) *apperr.Problem {
	t.Helper()
	if vars == nil {
		vars = template.NewVars()
	}
	_, _, err := ch.Prepare(context.Background(), channel.Message{
		Title: title,
		Body:  body,
		Vars:  vars,
		Mode:  mode,
	})
	return mustProblem(t, err)
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

// preparedOf 把 Prepared 断言为包内实现, 供断言端点与请求体
//
// preparedOf asserts the concrete type of a Prepared result, which lets a test inspect the endpoint
// and the request body
func preparedOf(t *testing.T, prepared channel.Prepared) *preparedChannel {
	t.Helper()
	concrete, ok := prepared.(*preparedChannel)
	if !ok {
		t.Fatalf("Prepare returned %T, want *preparedChannel", prepared)
	}
	return concrete
}

// decodePayload 把已准备的请求体解回 sendPayload
//
// decodePayload decodes a prepared request body back into sendPayload
func decodePayload(t *testing.T, p *preparedChannel) sendPayload {
	t.Helper()
	var payload sendPayload
	if err := json.Unmarshal(p.body, &payload); err != nil {
		t.Fatalf("unmarshal body %q: %v", p.body, err)
	}
	return payload
}

// strconvQuote 把字符串写成 JSON 字符串字面量
//
// strconvQuote writes a string as a JSON string literal
func strconvQuote(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// settingsJSON 拼出 url 与 token 两个必填字段的 settings
//
// settingsJSON builds settings from the two required fields, url and token
func settingsJSON(url, token, extra string) string {
	return fmt.Sprintf(`{"url":%s,"token":%s%s}`, strconvQuote(url), strconvQuote(token), extra)
}

// TestCreate_RequiredFields 覆盖 url 与 token 各自的缺失
//
// TestCreate_RequiredFields covers a missing url and a missing token in turn
func TestCreate_RequiredFields(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		wantKey  string
	}{
		{"missing url", `{"token":"example-token"}`, keyErrorMissingURL},
		{"blank url", `{"url":"   ","token":"example-token"}`, keyErrorMissingURL},
		{"missing token", `{"url":"https://gotify.example.com"}`, keyErrorMissingToken},
		{"blank token", `{"url":"https://gotify.example.com","token":"  "}`, keyErrorMissingToken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := factory{}.Create("gotify-main", json.RawMessage(c.settings))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !apperr.IsPermanent(err) {
				t.Fatalf("error %v is not permanent", err)
			}
			if got := mustProblem(t, err).ID; got != c.wantKey {
				t.Errorf("Problem.ID = %q, want %q", got, c.wantKey)
			}
		})
	}
}

// TestCreate_UnknownFieldRejected 覆盖未知键被严格解码拒绝
//
// TestCreate_UnknownFieldRejected covers an unknown key being rejected by strict decoding
func TestCreate_UnknownFieldRejected(t *testing.T) {
	_, err := factory{}.Create("gotify-main",
		json.RawMessage(`{"url":"https://gotify.example.com","token":"example-token","ur1":"typo"}`))
	if err == nil || !apperr.IsPermanent(err) {
		t.Fatalf("Create with an unknown key error = %v, want a permanent error", err)
	}
	prob := mustProblem(t, err)
	if prob.ID != "channel.error.invalid_settings" {
		t.Fatalf("Problem.ID = %q, want channel.error.invalid_settings", prob.ID)
	}
}

// TestCreate_InvalidURL 覆盖不含模板的非法地址在 Create 阶段就被拒绝
//
// TestCreate_InvalidURL covers an address without a template being refused during Create
func TestCreate_InvalidURL(t *testing.T) {
	_, err := factory{}.Create("gotify-main", json.RawMessage(settingsJSON("ftp://gotify.example.com", "example-token", "")))
	if err == nil || !apperr.IsPermanent(err) {
		t.Fatalf("Create error = %v, want a permanent error", err)
	}
	prob := mustProblem(t, err)
	if prob.ID != keyErrorInvalidURL {
		t.Fatalf("Problem.ID = %q, want %q", prob.ID, keyErrorInvalidURL)
	}
	if _, ok := prob.Data["URL"]; !ok {
		t.Fatalf("Problem.Data = %v, want a URL entry", prob.Data)
	}
}

// TestCreate_Priority 覆盖 priority 的数字, 十进制字符串, 模板串与非法写法
//
// TestCreate_Priority covers the number, decimal string, template string and invalid spellings of
// priority
func TestCreate_Priority(t *testing.T) {
	cases := []struct {
		name         string
		extra        string
		wantPriority int
		wantHas      bool
		wantTemplate string
	}{
		{"absent", "", 0, false, ""},
		{"null", `,"priority":null`, 0, false, ""},
		{"number", `,"priority":5`, 5, true, ""},
		{"number zero", `,"priority":0`, 0, true, ""},
		{"decimal string", `,"priority":"7"`, 7, true, ""},
		{"template string", `,"priority":"{{level}}"`, 0, false, "{{level}}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := newChannel(t, settingsJSON(exampleURL, "example-token", c.extra))
			if ch.s.Priority != c.wantPriority {
				t.Errorf("Priority = %d, want %d", ch.s.Priority, c.wantPriority)
			}
			if ch.s.HasPriority != c.wantHas {
				t.Errorf("HasPriority = %v, want %v", ch.s.HasPriority, c.wantHas)
			}
			if ch.s.PriorityTemplate != c.wantTemplate {
				t.Errorf("PriorityTemplate = %q, want %q", ch.s.PriorityTemplate, c.wantTemplate)
			}
		})
	}
}

// TestCreate_InvalidPriorityType 覆盖 priority 的类型与取值错误
//
// TestCreate_InvalidPriorityType covers the bad type and bad value of priority
func TestCreate_InvalidPriorityType(t *testing.T) {
	cases := []struct {
		name  string
		extra string
	}{
		{"boolean", `,"priority":true`},
		{"object", `,"priority":{"value":5}`},
		{"array", `,"priority":[5]`},
		{"bad decimal string", `,"priority":"high"`},
		{"float", `,"priority":5.5`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := factory{}.Create("gotify-main", json.RawMessage(settingsJSON(exampleURL, "example-token", c.extra)))
			if err == nil || !apperr.IsPermanent(err) {
				t.Fatalf("Create error = %v, want a permanent error", err)
			}
			prob := mustProblem(t, err)
			if prob.ID != keyErrorInvalidPrior {
				t.Fatalf("Problem.ID = %q, want %q", prob.ID, keyErrorInvalidPrior)
			}
			if _, ok := prob.Data["Value"]; !ok {
				t.Fatalf("Problem.Data = %v, want a Value entry", prob.Data)
			}
		})
	}
}

// TestFields 覆盖字段元数据的名称, 类型, 必填与描述键
//
// TestFields covers the name, type, required flag and description key of every field
func TestFields(t *testing.T) {
	fields := factory{}.Fields()
	want := []channel.Field{
		{Name: "url", Type: "string", Required: true, DescriptionKey: keyFieldURL},
		{Name: "token", Type: "string", Required: true, DescriptionKey: keyFieldToken},
		{Name: "priority", Type: "int", Required: false, DescriptionKey: keyFieldPriority},
	}
	if len(fields) != len(want) {
		t.Fatalf("Fields() returned %d entries, want %d", len(fields), len(want))
	}
	for i, w := range want {
		if fields[i] != w {
			t.Errorf("Fields()[%d] = %+v, want %+v", i, fields[i], w)
		}
	}
	if got := (factory{}).Describe(); got != keyDesc {
		t.Errorf("Describe() = %q, want %q", got, keyDesc)
	}
	if got := (factory{}).Type(); got != "gotify" {
		t.Errorf("Type() = %q, want gotify", got)
	}
}

// TestPrepare_EndpointJoin 覆盖 /message 拼接的四种形态
// 根路径追加, 已含 /message 保持, 带 query 时 query 保留, 尾部斜杠只影响 path
//
// TestPrepare_EndpointJoin covers the four shapes of the /message join
// A root path gets the segment appended, a path already ending in /message stays as it is, a query is
// preserved, and trailing slashes only affect the path
func TestPrepare_EndpointJoin(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"root path", "https://gotify.example.com", "https://gotify.example.com/message"},
		{"already message", "https://gotify.example.com/message", "https://gotify.example.com/message"},
		{"with query", "https://gotify.example.com?app=example", "https://gotify.example.com/message?app=example"},
		{"trailing slash", "https://gotify.example.com/", "https://gotify.example.com/message"},
		{"message with trailing slash", "https://gotify.example.com/message/", "https://gotify.example.com/message"},
		{"sub path", "https://gotify.example.com/gotify", "https://gotify.example.com/gotify/message"},
		{"message with query", "https://gotify.example.com/message?app=example", "https://gotify.example.com/message?app=example"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := newTestChannel(t, settingsJSON(c.url, "example-token", ""))
			prepared, warns := prepareStrict(t, ch, "Build done", "commit abc123")
			if len(warns) != 0 {
				t.Fatalf("warnings = %v, want none", warns)
			}
			if got := preparedOf(t, prepared).endpoint; got != c.want {
				t.Errorf("endpoint = %q, want %q", got, c.want)
			}
		})
	}
}

// TestPrepare_ContentMapping 覆盖正文与标题的内容映射
// 正文优先, 正文为空时用标题, 标题非空才进 payload, 两者都空报错
//
// TestPrepare_ContentMapping covers how the body and the title map onto the payload
// The body wins, an empty body falls back to the title, only a non-empty title reaches the payload,
// and an empty body with an empty title is an error
func TestPrepare_ContentMapping(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		body      string
		wantMsg   string
		wantTitle string
	}{
		{"both", "Build done", "commit abc123", "commit abc123", "Build done"},
		{"body only", "", "commit abc123", "commit abc123", ""},
		{"title falls back", "Build done", "", "Build done", "Build done"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", ""))
			prepared, warns := prepareStrict(t, ch, c.title, c.body)
			if len(warns) != 0 {
				t.Fatalf("warnings = %v, want none", warns)
			}
			payload := decodePayload(t, preparedOf(t, prepared))
			if payload.Message != c.wantMsg {
				t.Errorf("message = %q, want %q", payload.Message, c.wantMsg)
			}
			if payload.Title != c.wantTitle {
				t.Errorf("title = %q, want %q", payload.Title, c.wantTitle)
			}
		})
	}
}

// TestPrepare_EmptyContent 覆盖标题与正文都为空时报 empty_content
// 只含空白的文本按原样送出, 不算空内容
//
// TestPrepare_EmptyContent covers empty_content when both the title and the body are empty
// Whitespace-only text goes out as it came in and does not count as empty content
func TestPrepare_EmptyContent(t *testing.T) {
	ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", ""))
	prob := prepareProblem(t, ch, "", "", nil, channel.RenderStrict)
	if prob.ID != keyErrorEmptyContent {
		t.Fatalf("Problem.ID = %q, want %q", prob.ID, keyErrorEmptyContent)
	}

	prepared, _ := prepareStrict(t, ch, "   ", "")
	payload := decodePayload(t, preparedOf(t, prepared))
	if payload.Message != "   " {
		t.Fatalf("message = %q, want the whitespace-only body unchanged", payload.Message)
	}
}

// TestPrepare_MessageTruncation 覆盖 32767 rune 的边界: 恰好等于上限不截断, 超出一个 rune 截断并告警
// 中文与 emoji 各按一个 rune 计数
//
// TestPrepare_MessageTruncation covers the 32767-rune boundary: exactly at the cap is untouched, one
// rune past it is cut with a warning
// A Chinese character and an emoji each count as one rune
func TestPrepare_MessageTruncation(t *testing.T) {
	ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", ""))

	exact := strings.Repeat("中", messageLimitRunes)
	prepared, warns := prepareStrict(t, ch, "", exact)
	if len(warns) != 0 {
		t.Fatalf("warnings at the cap = %v, want none", warns)
	}
	if got := decodePayload(t, preparedOf(t, prepared)).Message; got != exact {
		t.Errorf("message rune count = %d, want %d", len([]rune(got)), messageLimitRunes)
	}

	over := exact + "中"
	prepared, warns = prepareStrict(t, ch, "", over)
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Fatalf("warnings = %v, want one %s", warns, keyWarnTruncated)
	}
	if got, _ := warns[0].Data["Limit"].(int); got != messageLimitRunes {
		t.Errorf("warning Limit = %v, want %d", warns[0].Data["Limit"], messageLimitRunes)
	}
	message := decodePayload(t, preparedOf(t, prepared)).Message
	if len([]rune(message)) != messageLimitRunes {
		t.Errorf("message rune count = %d, want %d", len([]rune(message)), messageLimitRunes)
	}
	if strings.Contains(message, "�") {
		t.Error("the cut left an invalid UTF-8 rune behind")
	}

	// emoji 在 rune 口径下同样算一个, 短语料因此不触发截断
	emoji := strings.Repeat("🙂", 10)
	prepared, warns = prepareStrict(t, ch, "", emoji)
	if len(warns) != 0 {
		t.Fatalf("emoji warnings = %v, want none", warns)
	}
	if got := decodePayload(t, preparedOf(t, prepared)).Message; got != emoji {
		t.Errorf("message = %q, want the emoji text unchanged", got)
	}
}

// TestPrepare_Priority 覆盖 priority 的 omission, 数字, 模板与 0-10 范围
//
// TestPrepare_Priority covers the omission, number, template and 0-10 range of priority
func TestPrepare_Priority(t *testing.T) {
	t.Run("omitted stays out of the payload", func(t *testing.T) {
		ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", ""))
		prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")
		p := preparedOf(t, prepared)
		if strings.Contains(string(p.body), "priority") {
			t.Fatalf("request body carries priority although it was omitted: %s", p.body)
		}
	})

	t.Run("number reaches the payload", func(t *testing.T) {
		ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", `,"priority":5`))
		prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")
		payload := decodePayload(t, preparedOf(t, prepared))
		if payload.Priority == nil || *payload.Priority != 5 {
			t.Fatalf("priority = %v, want 5", payload.Priority)
		}
	})

	t.Run("zero stays in the payload", func(t *testing.T) {
		ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", `,"priority":0`))
		prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")
		payload := decodePayload(t, preparedOf(t, prepared))
		if payload.Priority == nil || *payload.Priority != 0 {
			t.Fatalf("priority = %v, want an explicit 0", payload.Priority)
		}
	})

	t.Run("template renders a number", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("level", 7)
		ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", `,"priority":"{{level}}"`))
		prepared, warns := prepareWith(t, ch, "Build done", "commit abc123", vars, channel.RenderStrict)
		if len(warns) != 0 {
			t.Fatalf("warnings = %v, want none", warns)
		}
		payload := decodePayload(t, preparedOf(t, prepared))
		if payload.Priority == nil || *payload.Priority != 7 {
			t.Fatalf("priority = %v, want 7", payload.Priority)
		}
	})

	t.Run("out of range", func(t *testing.T) {
		for _, extra := range []string{`,"priority":-1`, `,"priority":11`, `,"priority":99`} {
			ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", extra))
			prob := prepareProblem(t, ch, "Build done", "commit abc123", nil, channel.RenderStrict)
			if prob.ID != keyErrorInvalidPrior {
				t.Errorf("%s: Problem.ID = %q, want %q", extra, prob.ID, keyErrorInvalidPrior)
			}
		}
	})

	t.Run("template out of range", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("level", 11)
		ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", `,"priority":"{{level}}"`))
		prob := prepareProblem(t, ch, "Build done", "commit abc123", vars, channel.RenderStrict)
		if prob.ID != keyErrorInvalidPrior {
			t.Fatalf("Problem.ID = %q, want %q", prob.ID, keyErrorInvalidPrior)
		}
	})

	t.Run("template empty means omitted", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("level", "")
		ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", `,"priority":"{{level}}"`))
		prepared, _ := prepareWith(t, ch, "Build done", "commit abc123", vars, channel.RenderCheck)
		if payload := decodePayload(t, preparedOf(t, prepared)); payload.Priority != nil {
			t.Fatalf("priority = %v, want it omitted", *payload.Priority)
		}
	})
}

// TestPrepare_Token 覆盖 token 的渲染, 缺失与控制字符
//
// TestPrepare_Token covers the rendering, the absence and the control characters of token
func TestPrepare_Token(t *testing.T) {
	t.Run("template renders", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("appToken", "example-token")
		ch := newTestChannel(t, settingsJSON(exampleURL, "{{appToken}}", ""))
		prepared, warns := prepareWith(t, ch, "Build done", "commit abc123", vars, channel.RenderStrict)
		if len(warns) != 0 {
			t.Fatalf("warnings = %v, want none", warns)
		}
		if got := preparedOf(t, prepared).token; got != "example-token" {
			t.Errorf("token = %q, want the rendered value", got)
		}
	})

	t.Run("empty render", func(t *testing.T) {
		vars := template.NewVars()
		vars.Set("appToken", "")
		ch := newTestChannel(t, settingsJSON(exampleURL, "{{appToken}}", ""))
		prob := prepareProblem(t, ch, "Build done", "commit abc123", vars, channel.RenderStrict)
		if prob.ID != keyErrorMissingToken {
			t.Fatalf("Problem.ID = %q, want %q", prob.ID, keyErrorMissingToken)
		}
	})

	t.Run("control characters", func(t *testing.T) {
		cases := []struct {
			name  string
			token string
		}{
			{"newline", "example\ntoken"},
			{"carriage return", "example\rtoken"},
			{"null", "example\x00token"},
			{"delete", "example\x7ftoken"},
			{"tab is allowed", "example\ttoken"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				ch := newTestChannel(t, settingsJSON(exampleURL, c.token, ""))
				_, _, err := ch.Prepare(context.Background(), channel.Message{
					Title: "Build done",
					Body:  "commit abc123",
					Vars:  template.NewVars(),
					Mode:  channel.RenderStrict,
				})
				if strings.Contains(c.name, "allowed") {
					if err != nil {
						t.Fatalf("Prepare with a tab in the token: %v", err)
					}
					return
				}
				if err == nil || !apperr.IsPermanent(err) {
					t.Fatalf("Prepare error = %v, want a permanent error", err)
				}
				if got := mustProblem(t, err).ID; got != keyErrorInvalidToken {
					t.Errorf("Problem.ID = %q, want %q", got, keyErrorInvalidToken)
				}
			})
		}
	})
}

// TestPrepare_NoNetwork 覆盖 Prepare 不发网络请求
// 指向一个已关闭的地址仍能成功准备, 因为准备阶段只构造请求
//
// TestPrepare_NoNetwork covers Prepare performing no network request
// Pointing at a closed address still prepares successfully, since the preparation stage only builds
// the request
func TestPrepare_NoNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("Prepare reached the network")
	}))
	address := srv.URL
	srv.Close()

	ch := newTestChannel(t, settingsJSON(address, "example-token", ""))
	if _, warns := prepareStrict(t, ch, "Build done", "commit abc123"); len(warns) != 0 {
		t.Fatalf("warnings = %v, want none", warns)
	}
}

// recordedRequest 是一次被服务器收到的请求
//
// recordedRequest is one request the server received
type recordedRequest struct {
	method  string
	path    string
	query   string
	header  string
	payload sendPayload
	rawBody string
}

// gotifyServer 是测试用的 Gotify 服务器
//
// gotifyServer is the Gotify server used by the tests
type gotifyServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

// newGotifyServer 启动服务器, handler 决定每次请求的响应
// 收到的请求被记录下来, 供断言方法, 路径, 鉴权头与请求体
//
// newGotifyServer starts a server whose handler decides the response of each request
// The received requests are recorded so a test can assert the method, the path, the auth header and
// the request body
func newGotifyServer(t *testing.T, handler http.HandlerFunc) *gotifyServer {
	t.Helper()
	s := &gotifyServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var payload sendPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Errorf("unmarshal request body %q: %v", raw, err)
		}
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{
			method:  r.Method,
			path:    r.URL.Path,
			query:   r.URL.RawQuery,
			header:  r.Header.Get(headerKey),
			payload: payload,
			rawBody: string(raw),
		})
		s.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// seen 返回服务器收到的请求副本
//
// seen returns a copy of the requests the server received
func (s *gotifyServer) seen() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// TestSend_Success 覆盖 200 与 204 都算成功, 含空 body 与坏 JSON
// 同时断言方法, 路径, 鉴权头与请求体
//
// TestSend_Success covers both 200 and 204 counting as a success, including an empty body and invalid
// JSON, and asserts the method, the path, the auth header and the request body along the way
func TestSend_Success(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"200 with body", http.StatusOK, `{"id":1}`},
		{"200 empty body", http.StatusOK, ""},
		{"200 invalid json", http.StatusOK, `{"id":`},
		{"204 no content", http.StatusNoContent, ""},
	}
	for _, c := range cases {
		status, body := c.status, c.body
		srv := newGotifyServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			fmt.Fprint(w, body)
		})
		ch := newTestChannel(t, settingsJSON(srv.URL, "example-token", `,"priority":5`))
		prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")

		if err := prepared.Send(context.Background(), srv.Client()); err != nil {
			t.Errorf("%s: Send: %v", c.name, err)
		}
		got := srv.seen()
		if len(got) != 1 {
			t.Errorf("%s: server saw %d requests, want 1", c.name, len(got))
			continue
		}
		if got[0].method != http.MethodPost {
			t.Errorf("%s: method = %q, want POST", c.name, got[0].method)
		}
		if got[0].path != messagePath {
			t.Errorf("%s: path = %q, want %q", c.name, got[0].path, messagePath)
		}
		if got[0].header != "example-token" {
			t.Errorf("%s: %s = %q", c.name, headerKey, got[0].header)
		}
		if got[0].payload.Message != "commit abc123" || got[0].payload.Title != "Build done" {
			t.Errorf("%s: payload = %+v", c.name, got[0].payload)
		}
		if got[0].payload.Priority == nil || *got[0].payload.Priority != 5 {
			t.Errorf("%s: priority = %v, want 5", c.name, got[0].payload.Priority)
		}
	}
}

// TestSend_StatusClassification 覆盖非 2xx 的状态码分类
// 401 与 404 永久; 429 与常规 5xx 可重试
//
// TestSend_StatusClassification covers the classification of the non-2xx statuses
// 401 and 404 are permanent, while 429 and an ordinary 5xx stay retryable
func TestSend_StatusClassification(t *testing.T) {
	cases := []struct {
		status        int
		wantPermanent bool
	}{
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusNotFound, true},
		{http.StatusTooManyRequests, false},
		{http.StatusInternalServerError, false},
		{http.StatusServiceUnavailable, false},
	}
	for _, c := range cases {
		status := c.status
		srv := newGotifyServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":"example rejection","errorCode":401}`)
		})
		ch := newTestChannel(t, settingsJSON(srv.URL, "example-token", ""))
		prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")
		err := prepared.Send(context.Background(), srv.Client())
		if err == nil {
			t.Errorf("status %d: expected an error", status)
			continue
		}
		prob := mustProblem(t, err)
		if prob.ID != keyErrorHTTPStatus {
			t.Errorf("status %d: ID = %q, want %q", status, prob.ID, keyErrorHTTPStatus)
		}
		if prob.Data["Status"] != status {
			t.Errorf("status %d: Data.Status = %v", status, prob.Data["Status"])
		}
		if body, _ := prob.Data["Body"].(string); !strings.Contains(body, "example rejection") {
			t.Errorf("status %d: Data.Body = %q, want the response summary", status, body)
		}
		if got := apperr.IsPermanent(err); got != c.wantPermanent {
			t.Errorf("status %d: IsPermanent = %v, want %v", status, got, c.wantPermanent)
		}
	}
}

// TestSend_RetryRebuildsBody 覆盖可重试失败后的下一次 Send 仍带完整请求体
//
// TestSend_RetryRebuildsBody covers a retryable failure letting the next Send carry the complete body
// again
func TestSend_RetryRebuildsBody(t *testing.T) {
	srv := newGotifyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "example outage")
	})
	ch := newTestChannel(t, settingsJSON(srv.URL, "example-token", ""))
	prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")

	for i := 0; i < 2; i++ {
		if err := prepared.Send(context.Background(), srv.Client()); err == nil {
			t.Fatalf("attempt %d: expected an error", i+1)
		}
	}
	got := srv.seen()
	if len(got) != 2 {
		t.Fatalf("the endpoint saw %d requests, want 2", len(got))
	}
	for _, request := range got {
		if request.payload.Message != "commit abc123" {
			t.Fatalf("attempt payload = %+v, want the message on every attempt", request.payload)
		}
	}
}

// TestSend_NoRetryInsideSend 覆盖 Send 自身不做等待重试: 一次调用只发一个请求
//
// TestSend_NoRetryInsideSend covers Send performing no wait or retry of its own: one call sends one
// request
func TestSend_NoRetryInsideSend(t *testing.T) {
	srv := newGotifyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "slow down")
	})
	ch := newTestChannel(t, settingsJSON(srv.URL, "example-token", ""))
	prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")

	if err := prepared.Send(context.Background(), srv.Client()); err == nil {
		t.Fatal("expected an error")
	}
	if got := srv.seen(); len(got) != 1 {
		t.Fatalf("one Send produced %d requests, want 1", len(got))
	}
}

// TestSend_ServerEchoTokenMasked 覆盖服务端在响应体里回显 token 时的遮蔽
//
// TestSend_ServerEchoTokenMasked covers masking a token the server echoes in the response body
func TestSend_ServerEchoTokenMasked(t *testing.T) {
	srv := newGotifyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"bad key example-token at "+"https://gotify.example.com/message"}`)
	})
	ch := newTestChannel(t, settingsJSON(srv.URL, "example-token", ""))
	prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")
	err := prepared.Send(context.Background(), srv.Client())

	prob := mustProblem(t, err)
	body, _ := prob.Data["Body"].(string)
	if strings.Contains(body, "example-token") {
		t.Fatalf("the error Data leaked the token: %q", body)
	}
	if !strings.Contains(body, maskValue) {
		t.Fatalf("the echoed token was not masked: %q", body)
	}
}

// TestSend_NetworkFailureIsRetryable 覆盖服务器下线时的网络错误保持可重试且不含完整地址
//
// TestSend_NetworkFailureIsRetryable covers a network failure staying retryable when the server is
// gone, without the full address in the error text
func TestSend_NetworkFailureIsRetryable(t *testing.T) {
	srv := newGotifyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	ch := newTestChannel(t, settingsJSON(srv.URL, "example-token", ""))
	prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")
	address := srv.URL
	srv.Close()

	err := prepared.Send(context.Background(), &http.Client{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if apperr.IsPermanent(err) {
		t.Fatal("a network failure was classified as permanent")
	}
	if message := err.Error(); strings.Contains(message, address) {
		t.Fatalf("the network error leaked the address: %v", message)
	}
}

// TestSend_NilClient 覆盖传入 nil client 时返回错误而不是 panic
//
// TestSend_NilClient covers a nil client returning an error instead of panicking
func TestSend_NilClient(t *testing.T) {
	ch := newTestChannel(t, settingsJSON(exampleURL, "example-token", ""))
	prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")
	if err := prepared.Send(context.Background(), nil); err == nil {
		t.Fatal("expected an error for a nil client")
	}
}

// TestPreview 覆盖预览只带主机名与字节数, 且不含 token 与完整路径
//
// TestPreview covers the preview carrying the host name and the byte count alone, without the token
// or the full path
func TestPreview(t *testing.T) {
	ch := newTestChannel(t, settingsJSON(exampleURL+"/gotify", "example-token", ""))
	prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")
	p := preparedOf(t, prepared)

	prob := prepared.Preview()
	if prob.ID != keyPreviewMessage {
		t.Fatalf("Preview ID = %q, want %q", prob.ID, keyPreviewMessage)
	}
	if got := prob.Data["Host"]; got != "gotify.example.com" {
		t.Errorf("Data.Host = %v, want gotify.example.com", got)
	}
	if got, _ := prob.Data["Bytes"].(int); got != len(p.body) {
		t.Errorf("Data.Bytes = %v, want %d", prob.Data["Bytes"], len(p.body))
	}
	rendered := fmt.Sprintf("%+v", prob)
	for _, secret := range []string{"example-token", "/gotify/message", exampleURL} {
		if strings.Contains(rendered, secret) {
			t.Errorf("Preview leaked %q: %s", secret, rendered)
		}
	}
}

// TestPreview_NoCredentialsInHost 覆盖地址带 userinfo 时 host 只取主机名
//
// TestPreview_NoCredentialsInHost covers a userinfo in the address, with host carrying the host name
// alone
func TestPreview_NoCredentialsInHost(t *testing.T) {
	ch := newTestChannel(t, settingsJSON("https://example-user:example-pass@gotify.example.com", "example-token", ""))
	prepared, _ := prepareStrict(t, ch, "Build done", "commit abc123")

	prob := prepared.Preview()
	if got := prob.Data["Host"]; got != "gotify.example.com" {
		t.Fatalf("Data.Host = %v, want gotify.example.com", got)
	}
	if rendered := fmt.Sprintf("%+v", prob); strings.Contains(rendered, "example-pass") {
		t.Fatalf("Preview leaked the userinfo: %s", rendered)
	}
}
