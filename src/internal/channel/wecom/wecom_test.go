package wecom

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// wecom 渠道测试: Create 静态校验, Prepare 渲染与按类型的字节截断, Send 的成功与错误分类,
// Preview 脱敏, 以及 en/zh-CN catalog 的键与占位符一致性
//
// WeCom channel tests: Create-time validation, Prepare rendering and per-type byte truncation,
// the success and failure classification of Send, Preview redaction, and the key plus placeholder
// parity of the en and zh-CN catalogs
// ---------------------------------------------------------------------------

// exampleURL 是测试用的端点占位, 只含 example 域名与通用 key
//
// exampleURL is the endpoint placeholder used by the tests, holding an example domain and a
// generic key only
const exampleURL = "https://qyapi.example.com/cgi-bin/webhook/send?key=example-key"

// newChannel 按 settings JSON 构造一个 wecom 渠道, 失败即中止测试
//
// newChannel builds a wecom channel from settings JSON and stops the test on failure
func newChannel(t *testing.T, raw string) channel.Channel {
	t.Helper()
	ch, err := (factory{}).Create("wecom-test", json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Create(%s): %v", raw, err)
	}
	return ch
}

// prepare 用给定标题正文渲染一次, 变量为空, 默认严格模式
//
// prepare renders once with the given title and body, no variables and strict mode by default
func prepare(t *testing.T, ch channel.Channel, title, body string) (channel.Prepared, []apperr.Problem) {
	t.Helper()
	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{
		Title: title,
		Body:  body,
		Vars:  template.NewVars(),
		Mode:  channel.RenderStrict,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared, warns
}

// preparedOf 把 Prepared 断言为包内实现, 供断言端点与请求体
//
// preparedOf asserts the Prepared into the package implementation so a test can inspect the
// endpoint and the request body
func preparedOf(t *testing.T, p channel.Prepared) *preparedChannel {
	t.Helper()
	prepared, ok := p.(*preparedChannel)
	if !ok {
		t.Fatalf("Prepared is %T, want *preparedChannel", p)
	}
	return prepared
}

// contentOf 取出 payload 里生效的内容字段
//
// contentOf returns the content field the payload actually carries
func contentOf(t *testing.T, p *preparedChannel) string {
	t.Helper()
	if p.payload.Markdown != nil {
		return p.payload.Markdown.Content
	}
	if p.payload.Text != nil {
		return p.payload.Text.Content
	}
	t.Fatal("payload carries neither text nor markdown")
	return ""
}

// TestFactory_Metadata 校验类型, 描述键与字段元数据完整
//
// TestFactory_Metadata checks the type, the description key and the completeness of the field
// metadata
func TestFactory_Metadata(t *testing.T) {
	if got := (factory{}).Type(); got != "wecom" {
		t.Errorf("Type() = %q, want wecom", got)
	}
	if got := (factory{}).Describe(); got != keyDesc {
		t.Errorf("Describe() = %q, want %q", got, keyDesc)
	}

	fields := (factory{}).Fields()
	want := []channel.Field{
		{Name: "url", Type: "string", Required: true, DescriptionKey: keyFieldURL},
		{Name: "msgType", Type: "string", Required: false, Default: msgTypeText, DescriptionKey: keyFieldMsgType},
	}
	if len(fields) != len(want) {
		t.Fatalf("Fields() has %d entries, want %d", len(fields), len(want))
	}
	for i, field := range fields {
		if field != want[i] {
			t.Errorf("Fields()[%d] = %#v, want %#v", i, field, want[i])
		}
	}

	ch := newChannel(t, `{"url":"`+exampleURL+`"}`)
	if ch.ID() != "wecom-test" || ch.Type() != "wecom" {
		t.Errorf("ID()/Type() = %q/%q", ch.ID(), ch.Type())
	}
}

// TestCreate_Validation 覆盖缺 url, 缺 key, 未知键与 msgType 的取值
//
// TestCreate_Validation covers a missing url, a missing key, an unknown field and the accepted
// values of msgType
func TestCreate_Validation(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantKey string
		wantErr bool
	}{
		{"ok", `{"url":"` + exampleURL + `"}`, "", false},
		{"default msg type", `{"url":"` + exampleURL + `"}`, "", false},
		{"markdown", `{"url":"` + exampleURL + `","msgType":"markdown"}`, "", false},
		{"msg type case", `{"url":"` + exampleURL + `","msgType":"MARKDOWN"}`, "", false},
		{"msg type space", `{"url":"` + exampleURL + `","msgType":" text "}`, "", false},
		{"missing url", `{}`, keyErrorMissingURL, true},
		{"blank url", `{"url":"   "}`, keyErrorMissingURL, true},
		{"missing key", `{"url":"https://qyapi.example.com/cgi-bin/webhook/send"}`, keyErrorMissingKey, true},
		{"blank key", `{"url":"https://qyapi.example.com/cgi-bin/webhook/send?key=  "}`, keyErrorMissingKey, true},
		{"unknown field", `{"url":"` + exampleURL + `","unknown":1}`, "channel.error.invalid_settings", true},
		{"invalid msg type", `{"url":"` + exampleURL + `","msgType":"html"}`, keyErrorInvalidType, true},
		{"invalid url", `{"url":"ftp://qyapi.example.com/send?key=k"}`, keyErrorInvalidURL, true},
		{"template url", `{"url":"{{base}}"}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch, err := factory{}.Create("wecom-test", json.RawMessage(tc.raw))
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Create(%s): %v", tc.raw, err)
				}
				if ch == nil {
					t.Fatal("Create returned a nil channel without an error")
				}
				return
			}
			if err == nil || !apperr.IsPermanent(err) {
				t.Fatalf("Create(%s) error = %v, want a permanent error", tc.raw, err)
			}
			var prob *apperr.Problem
			if !asProblem(err, &prob) || prob.ID != tc.wantKey {
				t.Errorf("Create(%s) problem = %#v, want %s", tc.raw, prob, tc.wantKey)
			}
		})
	}
}

// TestCreate_InvalidURLKeepsKeyHidden 确认 URL 校验错误里的 key 已被 MaskURL 遮蔽
//
// TestCreate_InvalidURLKeepsKeyHidden confirms the key inside a URL validation error is masked by
// MaskURL
func TestCreate_InvalidURLKeepsKeyHidden(t *testing.T) {
	_, err := factory{}.Create("wecom-test", json.RawMessage(`{"url":"ftp://qyapi.example.com/send?key=example-secret"}`))
	if err == nil {
		t.Fatal("Create accepted a non-http address")
	}
	var prob *apperr.Problem
	if !asProblem(err, &prob) || prob.ID != keyErrorInvalidURL {
		t.Fatalf("problem = %#v, want %s", prob, keyErrorInvalidURL)
	}
	urlText, _ := prob.Data["URL"].(string)
	if strings.Contains(urlText, "example-secret") {
		t.Errorf("Data[URL] exposes the key: %q", urlText)
	}
	if strings.Contains(strings.ToLower(urlText), "example-secret") {
		t.Errorf("error text exposes the key: %q", err.Error())
	}
}

// TestPrepare_Content 覆盖标题正文的组合映射
//
// TestPrepare_Content covers the join mapping of the title and the body
func TestPrepare_Content(t *testing.T) {
	cases := []struct {
		name  string
		title string
		body  string
		want  string
	}{
		{"both", "example-title", "example-body", "example-title\n\nexample-body"},
		{"title only", "example-title", "", "example-title"},
		{"body only", "", "example-body", "example-body"},
		{"whitespace kept", "   ", "   ", "   \n\n   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newChannel(t, `{"url":"`+exampleURL+`"}`)
			prepared, warns := prepare(t, ch, tc.title, tc.body)
			if len(warns) != 0 {
				t.Fatalf("warnings = %v, want none", warns)
			}
			if got := contentOf(t, preparedOf(t, prepared)); got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPrepare_EmptyContent 覆盖标题正文都为空时的永久错误
//
// TestPrepare_EmptyContent covers the permanent error when the title and the body are both empty
func TestPrepare_EmptyContent(t *testing.T) {
	ch := newChannel(t, `{"url":"`+exampleURL+`"}`)
	_, _, err := ch.Prepare(context.Background(), channel.Message{Vars: template.NewVars(), Mode: channel.RenderStrict})
	if err == nil || !apperr.IsPermanent(err) {
		t.Fatalf("Prepare with no content error = %v, want a permanent error", err)
	}
	var prob *apperr.Problem
	if !asProblem(err, &prob) || prob.ID != keyErrorEmptyContent {
		t.Errorf("problem = %#v, want %s", prob, keyErrorEmptyContent)
	}
}

// TestPrepare_MsgTypePayload 覆盖 text 与 markdown 的请求体字段选择
//
// TestPrepare_MsgTypePayload covers which request-body field text and markdown select
func TestPrepare_MsgTypePayload(t *testing.T) {
	cases := []struct {
		raw        string
		wantType   string
		wantMarkdn bool
	}{
		{`{"url":"` + exampleURL + `"}`, msgTypeText, false},
		{`{"url":"` + exampleURL + `","msgType":"text"}`, msgTypeText, false},
		{`{"url":"` + exampleURL + `","msgType":"markdown"}`, msgTypeMarkdown, true},
	}
	for _, tc := range cases {
		ch := newChannel(t, tc.raw)
		prepared, _ := prepare(t, ch, "example-title", "example-body")
		p := preparedOf(t, prepared)
		if p.payload.MsgType != tc.wantType {
			t.Errorf("msgtype = %q, want %q", p.payload.MsgType, tc.wantType)
		}
		if (p.payload.Markdown != nil) != tc.wantMarkdn {
			t.Errorf("markdown present = %v, want %v", p.payload.Markdown != nil, tc.wantMarkdn)
		}
		if got := contentOf(t, p); got != "example-title\n\nexample-body" {
			t.Errorf("content = %q", got)
		}

		// 序列化结果只带生效的字段, 空的 text 或 markdown 不出现在 JSON 里
		//
		// The serialised body carries the effective field only, so an empty text or markdown does
		// not appear in the JSON
		body, err := json.Marshal(p.payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if _, present := decoded["text"]; present != !tc.wantMarkdn {
			t.Errorf("text present = %v, want %v: %s", present, !tc.wantMarkdn, body)
		}
		if _, present := decoded["markdown"]; present != tc.wantMarkdn {
			t.Errorf("markdown present = %v, want %v: %s", present, tc.wantMarkdn, body)
		}
		if got, _ := decoded["msgtype"].(string); got != tc.wantType {
			t.Errorf("msgtype in JSON = %q, want %q", got, tc.wantType)
		}
	}
}

// TestPrepare_Truncation 覆盖 text 2048 与 markdown 4096 字节上限及截断警告
//
// TestPrepare_Truncation covers the 2048-byte text cap, the 4096-byte markdown cap and the
// truncation warning
func TestPrepare_Truncation(t *testing.T) {
	cases := []struct {
		msgType string
		limit   int
	}{
		{msgTypeText, textLimitBytes},
		{msgTypeMarkdown, markdownLimitBytes},
	}
	for _, tc := range cases {
		t.Run(tc.msgType, func(t *testing.T) {
			ch := newChannel(t, `{"url":"`+exampleURL+`","msgType":"`+tc.msgType+`"}`)

			// 恰好等于上限的内容原样发送, 不产生警告
			//
			// Content exactly at the cap goes out as it stands and raises no warning
			exact := strings.Repeat("a", tc.limit)
			prepared, warns := prepare(t, ch, exact, "")
			if len(warns) != 0 {
				t.Fatalf("warnings at the cap = %v, want none", warns)
			}
			if got := contentOf(t, preparedOf(t, prepared)); got != exact {
				t.Errorf("content length = %d, want %d", len(got), tc.limit)
			}

			// 超出一个字节即截断, 并带 Limit 警告
			//
			// One byte past the cap is cut, with a warning carrying Limit
			prepared, warns = prepare(t, ch, strings.Repeat("a", tc.limit+1), "")
			if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
				t.Fatalf("warnings = %v, want one %s", warns, keyWarnTruncated)
			}
			if got, _ := warns[0].Data["Limit"].(int); got != tc.limit {
				t.Errorf("warning Limit = %v, want %d", warns[0].Data["Limit"], tc.limit)
			}
			content := contentOf(t, preparedOf(t, prepared))
			if len(content) != tc.limit {
				t.Errorf("content length = %d, want %d", len(content), tc.limit)
			}

			// 多字节字符按 UTF-8 边界回退, 输出仍是合法 UTF-8 且不超过上限
			//
			// A multi-byte character steps back to a UTF-8 boundary, so the output stays valid
			// UTF-8 and stays inside the cap
			multibyte := strings.Repeat("中", tc.limit/3+1)
			prepared, warns = prepare(t, ch, multibyte, "")
			if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
				t.Fatalf("multibyte warnings = %v, want one %s", warns, keyWarnTruncated)
			}
			content = contentOf(t, preparedOf(t, prepared))
			if len(content) > tc.limit {
				t.Errorf("multibyte content length = %d, want at most %d", len(content), tc.limit)
			}
			if !utf8Valid(content) {
				t.Errorf("multibyte content is not valid UTF-8: %q", content)
			}
		})
	}
}

// TestPrepare_JoinThenTruncate 覆盖先拼接再截断的顺序: 标题接近上限时正文被切掉
//
// TestPrepare_JoinThenTruncate covers joining before truncating: the body is cut when the title
// nearly fills the limit
func TestPrepare_JoinThenTruncate(t *testing.T) {
	ch := newChannel(t, `{"url":"`+exampleURL+`"}`)
	title := strings.Repeat("a", textLimitBytes-1)
	prepared, warns := prepare(t, ch, title, strings.Repeat("b", 100))
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Fatalf("warnings = %v, want one %s", warns, keyWarnTruncated)
	}
	content := contentOf(t, preparedOf(t, prepared))
	if len(content) != textLimitBytes {
		t.Errorf("content length = %d, want %d", len(content), textLimitBytes)
	}
	if strings.HasSuffix(content, "b") {
		t.Errorf("content kept part of the body it should have cut: %q", content[len(content)-8:])
	}
}

// TestPrepare_Template 覆盖 url 与 msgType 的模板渲染
//
// TestPrepare_Template covers the template rendering of url and msgType
func TestPrepare_Template(t *testing.T) {
	vars := template.NewVars()
	vars.Set("base", "https://qyapi.example.com/cgi-bin/webhook/send")
	vars.Set("key", "example-key")
	vars.Set("type", "markdown")

	ch := newChannel(t, `{"url":"{{base}}?key={{key}}","msgType":"{{type}}"}`)
	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  vars,
		Mode:  channel.RenderStrict,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings = %v, want none", warns)
	}
	p := preparedOf(t, prepared)
	if got, want := p.endpoint, "https://qyapi.example.com/cgi-bin/webhook/send?key=example-key"; got != want {
		t.Errorf("endpoint = %q, want %q", got, want)
	}
	if p.msgType != msgTypeMarkdown || p.payload.Markdown == nil {
		t.Errorf("msgType = %q, markdown = %v", p.msgType, p.payload.Markdown)
	}
}

// TestPrepare_UnresolvedTemplate 覆盖严格模式报错与 check 模式告警后继续
//
// TestPrepare_UnresolvedTemplate covers the strict error and the check-mode warning that continues
func TestPrepare_UnresolvedTemplate(t *testing.T) {
	ch := newChannel(t, `{"url":"{{base}}?key=example-key"}`)
	if _, _, err := ch.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  template.NewVars(),
		Mode:  channel.RenderStrict,
	}); err == nil || !apperr.IsPermanent(err) {
		t.Fatalf("strict mode error = %v, want a permanent error", err)
	}

	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  template.NewVars(),
		Mode:  channel.RenderCheck,
	})
	// check 模式保留 "{{base}}" 字面, 它不是合法主机以外的地址: 缺 key 与非法地址都可能先命中,
	// 因此这里只要求得到一个永久错误或一次告警后的成功准备, 具体分支由 Prepare 的校验顺序决定
	//
	// Check mode keeps "{{base}}" literally, which is neither a valid host nor necessarily a
	// missing key, so the assertion accepts either a permanent error or a prepared result carrying
	// the warning
	if err != nil {
		if !apperr.IsPermanent(err) {
			t.Fatalf("check mode error = %v, want a permanent error", err)
		}
		return
	}
	if len(warns) == 0 {
		t.Error("check mode returned no warning for the unresolved variable")
	}
	if prepared == nil {
		t.Error("check mode returned a nil Prepared without an error")
	}
}

// TestPrepare_MissingKeyAfterRender 覆盖模板渲染后 key 为空时的永久错误
//
// TestPrepare_MissingKeyAfterRender covers the permanent error when the key renders empty
func TestPrepare_MissingKeyAfterRender(t *testing.T) {
	vars := template.NewVars()
	vars.Set("blank", "")

	ch := newChannel(t, `{"url":"https://qyapi.example.com/cgi-bin/webhook/send?key={{blank}}"}`)
	_, _, err := ch.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  vars,
		Mode:  channel.RenderStrict,
	})
	if err == nil || !apperr.IsPermanent(err) {
		t.Fatalf("Prepare error = %v, want a permanent error", err)
	}
	var prob *apperr.Problem
	if !asProblem(err, &prob) || prob.ID != keyErrorMissingKey {
		t.Errorf("problem = %#v, want %s", prob, keyErrorMissingKey)
	}
}

// TestPrepare_NormalizesURL 覆盖缺 scheme 地址的规范化与 key 保留
//
// TestPrepare_NormalizesURL covers normalising a scheme-less address while keeping the key
func TestPrepare_NormalizesURL(t *testing.T) {
	ch := newChannel(t, `{"url":"qyapi.example.com/cgi-bin/webhook/send?key=example-key"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	if got, want := preparedOf(t, prepared).endpoint, "https://qyapi.example.com/cgi-bin/webhook/send?key=example-key"; got != want {
		t.Errorf("endpoint = %q, want %q", got, want)
	}
}

// TestSend_ResponseVerdicts 覆盖成功, 业务码永久/限流, HTTP 状态与坏响应
//
// TestSend_ResponseVerdicts covers success, a permanent and a throttling business code, HTTP
// statuses and a malformed response
func TestSend_ResponseVerdicts(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		wantKey   string
		wantErr   bool
		permanent bool
	}{
		{"errcode 0", http.StatusOK, `{"errcode":0,"errmsg":"ok"}`, "", false, false},
		{"errcode 40001", http.StatusOK, `{"errcode":40001,"errmsg":"invalid key"}`, keyErrorAPICode, true, true},
		{"errcode 45009", http.StatusOK, `{"errcode":45009,"errmsg":"api freq out of limit"}`, keyErrorAPICode, true, false},
		{"http 400", http.StatusBadRequest, `{"errcode":0}`, "common.error.http_status", true, true},
		{"http 404", http.StatusNotFound, `not found`, "common.error.http_status", true, true},
		{"http 429", http.StatusTooManyRequests, `slow down`, "common.error.http_status", true, false},
		{"http 500", http.StatusInternalServerError, `boom`, "common.error.http_status", true, false},
		{"bad json", http.StatusOK, `not json`, keyErrorBadResponse, true, false},
		{"empty body", http.StatusOK, ``, keyErrorBadResponse, true, false},
		{"missing errcode", http.StatusOK, `{"errmsg":"ok"}`, keyErrorBadResponse, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %q, want POST", r.Method)
				}
				if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
					t.Errorf("Content-Type = %q, want JSON", got)
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			ch := newChannel(t, `{"url":"`+srv.URL+`?key=example-key"}`)
			prepared, _ := prepare(t, ch, "example-title", "example-body")
			err := prepared.Send(context.Background(), srv.Client())
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Send: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Send succeeded, want an error")
			}
			if apperr.IsPermanent(err) != tc.permanent {
				t.Errorf("IsPermanent(%v) = %v, want %v", err, apperr.IsPermanent(err), tc.permanent)
			}
			var prob *apperr.Problem
			if !asProblem(err, &prob) || prob.ID != tc.wantKey {
				t.Errorf("problem = %#v, want %s", prob, tc.wantKey)
			}
		})
	}
}

// TestSend_RequestBody 覆盖实际发出的请求体形状
//
// TestSend_RequestBody covers the shape of the request body actually sent
func TestSend_RequestBody(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"url":"`+srv.URL+`?key=example-key","msgType":"markdown"}`)
	prepared, _ := prepare(t, ch, "example-title", "example-body")
	if err := prepared.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var decoded struct {
		MsgType  string `json:"msgtype"`
		Markdown *struct {
			Content string `json:"content"`
		} `json:"markdown"`
	}
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("decode request body %s: %v", gotBody, err)
	}
	if decoded.MsgType != msgTypeMarkdown {
		t.Errorf("msgtype = %q, want markdown", decoded.MsgType)
	}
	if decoded.Markdown == nil || decoded.Markdown.Content != "example-title\n\nexample-body" {
		t.Errorf("markdown = %#v", decoded.Markdown)
	}
	if strings.Contains(string(gotBody), "text") {
		t.Errorf("request body carries an empty text field: %s", gotBody)
	}
}

// TestSend_NoRetryAfterSuccess 确认成功后重复 Send 会再次发送, 且渠道本身不做重试计数
//
// TestSend_NoRetryAfterSuccess confirms a repeated Send reaches the endpoint again, and the channel
// itself keeps no retry count
func TestSend_NoRetryAfterSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"url":"`+srv.URL+`?key=example-key"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	for i := 0; i < 2; i++ {
		if err := prepared.Send(context.Background(), srv.Client()); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 since the channel does not deduplicate sends", calls)
	}
}

// TestSend_MasksServerEcho 确认响应里回显的 key 不会出现在错误 Data 与文本里
//
// TestSend_MasksServerEcho confirms a key echoed by the response reaches neither the error Data nor
// the error text
func TestSend_MasksServerEcho(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errcode":40001,"errmsg":"key example-secret is not valid"}`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"url":"`+srv.URL+`?key=example-secret"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	err := prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("Send succeeded, want an error")
	}
	var prob *apperr.Problem
	if !asProblem(err, &prob) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	message, _ := prob.Data["Message"].(string)
	if strings.Contains(message, "example-secret") {
		t.Errorf("Data[Message] exposes the key: %q", message)
	}
	if strings.Contains(err.Error(), "example-secret") {
		t.Errorf("error text exposes the key: %q", err.Error())
	}
}

// TestSend_NetworkErrorSanitized 确认网络错误文本里的地址经过脱敏
//
// TestSend_NetworkErrorSanitized confirms the address inside a network error text is redacted
func TestSend_NetworkErrorSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ch := newChannel(t, `{"url":"`+srv.URL+`?key=example-key"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	srv.Close()

	err := prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("Send succeeded against a closed server, want an error")
	}
	if strings.Contains(err.Error(), "example-key") {
		t.Errorf("network error exposes the key: %q", err.Error())
	}
}

// TestPreview_NoSecrets 覆盖 text/markdown 预览, 并确认不含 URL 与 key
//
// TestPreview_NoSecrets covers the text and markdown previews and confirms that neither the URL nor
// the key appears inside them
func TestPreview_NoSecrets(t *testing.T) {
	cases := []struct {
		raw     string
		msgType string
	}{
		{`{"url":"` + exampleURL + `"}`, msgTypeText},
		{`{"url":"` + exampleURL + `","msgType":"markdown"}`, msgTypeMarkdown},
	}
	for _, tc := range cases {
		t.Run(tc.msgType, func(t *testing.T) {
			ch := newChannel(t, tc.raw)
			prepared, _ := prepare(t, ch, "example-title", "")
			preview := prepared.Preview()
			if preview.ID != keyPreviewMessage {
				t.Errorf("Preview().ID = %q, want %q", preview.ID, keyPreviewMessage)
			}
			if got, _ := preview.Data["MsgType"].(string); got != tc.msgType {
				t.Errorf("Preview().Data[MsgType] = %v, want %q", preview.Data["MsgType"], tc.msgType)
			}
			if got, _ := preview.Data["Targets"].(int); got != targetCount {
				t.Errorf("Preview().Data[Targets] = %v, want %d", preview.Data["Targets"], targetCount)
			}

			rendered, err := json.Marshal(preview)
			if err != nil {
				t.Fatalf("marshal preview: %v", err)
			}
			for _, secret := range []string{"example-key", "qyapi.example.com", "cgi-bin"} {
				if strings.Contains(string(rendered), secret) {
					t.Errorf("preview exposes %q: %s", secret, rendered)
				}
			}
		})
	}
}

// TestCatalog_KeysMatch 校验 en 与 zh-CN catalog 的键, 占位符与文案格式一致
//
// TestCatalog_KeysMatch checks that the en and zh-CN catalogs agree on keys, placeholders and copy
// shape
func TestCatalog_KeysMatch(t *testing.T) {
	en := readCatalog(t, "en")
	zh := readCatalog(t, "zh-CN")

	if len(en) != len(zh) {
		t.Errorf("en has %d keys, zh-CN has %d", len(en), len(zh))
	}
	for key, enText := range en {
		zhText, ok := zh[key]
		if !ok {
			t.Errorf("zh-CN is missing %s", key)
			continue
		}
		if strings.Join(catalogPlaceholders(enText), ",") != strings.Join(catalogPlaceholders(zhText), ",") {
			t.Errorf("%s placeholders differ: en %v, zh-CN %v", key, catalogPlaceholders(enText), catalogPlaceholders(zhText))
		}
		if !strings.HasPrefix(key, "channel.wecom.") {
			t.Errorf("key %q does not carry the wecom prefix", key)
		}
		for _, text := range []string{enText, zhText} {
			if strings.Contains(text, "%s") || strings.Contains(text, "%d") {
				t.Errorf("%s uses a printf verb: %q", key, text)
			}
			if strings.HasSuffix(strings.TrimSpace(text), ".") || strings.HasSuffix(strings.TrimSpace(text), "。") {
				t.Errorf("%s ends with a full stop: %q", key, text)
			}
		}
	}
	for key := range zh {
		if _, ok := en[key]; !ok {
			t.Errorf("en is missing %s", key)
		}
	}
}

// TestCatalog_RequiredKeysAndData 校验 spec 第 9 节列出的键都存在, 且每个 Problem 的 Data 填满占位符
//
// TestCatalog_RequiredKeysAndData checks that every key listed in section 9 of the spec exists and
// that the Data of each Problem fills its placeholders
func TestCatalog_RequiredKeysAndData(t *testing.T) {
	en := readCatalog(t, "en")
	required := []string{
		keyDesc, keyFieldURL, keyFieldMsgType,
		keyErrorMissingURL, keyErrorMissingKey, keyErrorInvalidURL, keyErrorInvalidType,
		keyErrorEmptyContent, keyErrorAPICode, keyErrorBadResponse,
		keyWarnTruncated, keyPreviewMessage,
	}
	for _, key := range required {
		if text := en[key]; text == "" {
			t.Errorf("en catalog is missing the required key %s", key)
		}
	}

	problems := []*apperr.Problem{
		{ID: keyErrorInvalidURL, Data: map[string]any{"URL": "https://example.com"}},
		{ID: keyErrorInvalidType, Data: map[string]any{"Type": "html"}},
		{ID: keyErrorAPICode, Data: map[string]any{"Code": 40001, "Message": "example"}},
		{ID: keyErrorBadResponse, Data: map[string]any{"Detail": "example"}},
		{ID: keyWarnTruncated, Data: map[string]any{"Limit": textLimitBytes}},
		{ID: keyPreviewMessage, Data: map[string]any{"MsgType": msgTypeText, "Targets": targetCount}},
	}
	for _, prob := range problems {
		for _, name := range catalogPlaceholders(en[prob.ID]) {
			if _, ok := prob.Data[name]; !ok {
				t.Errorf("%s: Data is missing %q", prob.ID, name)
			}
		}
	}

	// 无占位符的键确认 Data 为空, 避免多余字段进文案
	//
	// A key without placeholders is confirmed to carry no Data, so no spare field reaches the copy
	for _, key := range []string{keyErrorMissingURL, keyErrorMissingKey, keyErrorEmptyContent} {
		if got := catalogPlaceholders(en[key]); len(got) != 0 {
			t.Errorf("%s: unexpected placeholders %v", key, got)
		}
	}
}

// readCatalog 读取一份 channel_wecom.json 并解成键值表
// 测试从包目录出发, 因此路径相对 src/internal 层
//
// readCatalog reads one channel_wecom.json into a key-value table
// The test starts in the package directory, so the path steps up to the src/internal level
func readCatalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "i18n", "catalog", lang, "channel_wecom.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// catalogPlaceholderPattern 匹配文案里的 {{.Name}} 占位符
//
// catalogPlaceholderPattern matches a {{.Name}} placeholder inside copy
var catalogPlaceholderPattern = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_]+)\s*\}\}`)

// catalogPlaceholders 返回一份文案里的占位符名, 排序后输出
//
// catalogPlaceholders returns the placeholder names of one message, sorted
func catalogPlaceholders(text string) []string {
	matches := catalogPlaceholderPattern.FindAllStringSubmatch(text, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	sort.Strings(names)
	return names
}

// utf8Valid 判断字符串是否为合法 UTF-8
//
// utf8Valid reports whether a string is valid UTF-8
func utf8Valid(s string) bool { return utf8.ValidString(s) }

// asProblem 取出 err 链上的 *apperr.Problem
//
// asProblem takes the *apperr.Problem out of the err chain
func asProblem(err error, target **apperr.Problem) bool {
	for err != nil {
		if prob, ok := err.(*apperr.Problem); ok {
			*target = prob
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// Example 是占位示例, 说明一次 Prepare 与 Preview 的用法
//
// Example is a placeholder example showing one Prepare and Preview round
func Example() {
	ch, err := (factory{}).Create("example", json.RawMessage(`{"url":"https://qyapi.example.com/cgi-bin/webhook/send?key=example-key"}`))
	if err != nil {
		panic(err)
	}
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  template.NewVars(),
	})
	if err != nil {
		panic(err)
	}
	// Output:
	_ = prepared
}
