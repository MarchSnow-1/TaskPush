package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// dingtalk 渠道测试: init 注册与字段元数据, Create 严格解码与静态校验, 加签算法与时钟注入,
// Prepare 的内容映射, 截断与 atAll 规则, Preview 脱敏
// 加签向量固定 Message.Now, 因此签名不依赖机器时间; 网络用例见 dingtalk_send_test.go
//
// Tests for the DingTalk channel: the init registration and field metadata, Create strict decoding
// and static validation, the signing algorithm and its injected clock, and the Prepare content
// mapping, truncation and atAll rules plus Preview redaction
// The signing vectors fix Message.Now, so a signature does not depend on the machine clock; the
// network cases live in dingtalk_send_test.go
// ---------------------------------------------------------------------------

// exampleToken 是 access_token 的通用占位值
//
// exampleToken is the generic placeholder value of access_token
const exampleToken = "exampletoken"

// signedNow 是加签用例的固定时钟: 2026-01-02T03:04:05Z, 对应毫秒 1767323045000
//
// signedNow is the fixed signing clock: 2026-01-02T03:04:05Z, which is millisecond 1767323045000
var signedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// webhookURL 返回带 access_token 的机器人地址
//
// webhookURL returns a robot address carrying access_token
func webhookURL(host string) string {
	return host + "/robot/send?access_token=" + exampleToken
}

// newTestChannel 用给定 settings 构造渠道实例
//
// newTestChannel builds a channel instance from the given settings
func newTestChannel(t *testing.T, settingsJSON string) channel.Channel {
	t.Helper()
	ch, err := channel.Create("dt", "dingtalk", json.RawMessage(settingsJSON))
	if err != nil {
		t.Fatalf("channel.Create: %v", err)
	}
	return ch
}

// prepareWith 以指定消息与模式执行 Prepare
//
// prepareWith runs Prepare with one message and the given mode
func prepareWith(ch channel.Channel, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	if msg.Vars == nil {
		msg.Vars = template.NewVars()
	}
	if msg.Now.IsZero() {
		msg.Now = signedNow
	}
	return ch.Prepare(context.Background(), msg)
}

// prepareStrict 以严格模式渲染标题与正文
//
// prepareStrict renders a title and a body under the strict mode
func prepareStrict(ch channel.Channel, title, body string) (channel.Prepared, []apperr.Problem, error) {
	return prepareWith(ch, channel.Message{Title: title, Body: body, Mode: channel.RenderStrict})
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

// mustPrepared 断言 Prepared 是本包实现并返回它
//
// mustPrepared asserts that Prepared is this package's implementation and returns it
func mustPrepared(t *testing.T, prepared channel.Prepared) *preparedChannel {
	t.Helper()
	if prepared == nil {
		t.Fatal("Prepare returned a nil Prepared with a nil error")
	}
	p, ok := prepared.(*preparedChannel)
	if !ok {
		t.Fatalf("Prepared is %T, want *preparedChannel", prepared)
	}
	return p
}

// decodePayload 解析 Prepare 构造的请求体
//
// decodePayload parses the request body Prepare built
func decodePayload(t *testing.T, p *preparedChannel) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(p.payload, &body); err != nil {
		t.Fatalf("decode payload %q: %v", p.payload, err)
	}
	return body
}

// warnIDs 收集警告的消息键
//
// warnIDs collects the message keys of the warnings
func warnIDs(warns []apperr.Problem) []string {
	out := make([]string, 0, len(warns))
	for _, w := range warns {
		out = append(out, w.ID)
	}
	return out
}

// TestFactoryRegistration 覆盖 init 注册与工厂元数据
//
// TestFactoryRegistration covers the init registration and the factory metadata
func TestFactoryRegistration(t *testing.T) {
	if !channel.IsRegistered("dingtalk") {
		t.Fatal("dingtalk is not registered")
	}
	var f channel.Factory = factory{}
	if f.Type() != "dingtalk" {
		t.Fatalf("Type() = %q, want dingtalk", f.Type())
	}
	if f.Describe() != keyDesc {
		t.Fatalf("Describe() = %q, want %q", f.Describe(), keyDesc)
	}
}

// TestFactory_Fields 覆盖四个 settings 字段的元数据与顺序
//
// TestFactory_Fields covers the metadata and the order of the four settings fields
func TestFactory_Fields(t *testing.T) {
	fields := factory{}.Fields()
	want := []struct {
		name, typ, def string
		required       bool
	}{
		{"url", "string", "", true},
		{"secret", "string", "", false},
		{"msgType", "string", "text", false},
		{"atAll", "bool", "false", false},
	}
	if len(fields) != len(want) {
		t.Fatalf("Fields() returned %d fields, want %d", len(fields), len(want))
	}
	for i, w := range want {
		got := fields[i]
		if got.Name != w.name || got.Type != w.typ || got.Required != w.required || got.Default != w.def {
			t.Errorf("field %d = {%s %s required=%v default=%q}, want {%s %s required=%v default=%q}",
				i, got.Name, got.Type, got.Required, got.Default, w.name, w.typ, w.required, w.def)
		}
		if got.DescriptionKey != "channel.dingtalk.field."+w.name+".desc" {
			t.Errorf("field %s: DescriptionKey = %q", got.Name, got.DescriptionKey)
		}
	}
}

// TestCreate_RequiredAndStatic 覆盖 url 必填, access_token, msgType 枚举与 atAll 类型
//
// TestCreate_RequiredAndStatic covers the required url, access_token, the msgType enumeration and
// the atAll type
func TestCreate_RequiredAndStatic(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		wantID string
	}{
		{"missing url", `{}`, keyErrorMissingURL},
		{"blank url", `{"url":"   "}`, keyErrorMissingURL},
		{"url without access_token", `{"url":"https://example.com/robot/send"}`, keyErrorMissingToken},
		{"empty access_token value", `{"url":"https://example.com/robot/send?access_token="}`, keyErrorMissingToken},
		{"other query only", `{"url":"https://example.com/robot/send?foo=1"}`, keyErrorMissingToken},
		{"invalid scheme", `{"url":"ftp://example.com/robot/send?access_token=example"}`, keyErrorInvalidURL},
		{"unknown msgType", `{"url":"https://example.com/robot/send?access_token=example","msgType":"image"}`, keyErrorInvalidMsgType},
		{"unknown key", `{"url":"https://example.com/robot/send?access_token=example","nope":1}`, "channel.error.invalid_settings"},
		{"atAll as word", `{"url":"https://example.com/robot/send?access_token=example","atAll":"maybe"}`, "channel.error.invalid_settings"},
		{"atAll as number", `{"url":"https://example.com/robot/send?access_token=example","atAll":1}`, "channel.error.invalid_settings"},
		{"settings not object", `["url"]`, "channel.error.invalid_settings"},
	}
	for _, c := range cases {
		_, err := channel.Create("dt", "dingtalk", json.RawMessage(c.raw))
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		prob := mustProblem(t, err)
		if prob.ID != c.wantID {
			t.Errorf("%s: ID = %q, want %q", c.name, prob.ID, c.wantID)
		}
		if !apperr.IsPermanent(err) {
			t.Errorf("%s: error is not permanent", c.name)
		}
	}
}

// TestCreate_InvalidURLMasksToken 覆盖 invalid_url 的 Data 不含 access_token 原值
//
// TestCreate_InvalidURLMasksToken covers Data of invalid_url holding no access_token value as written
func TestCreate_InvalidURLMasksToken(t *testing.T) {
	_, err := channel.Create("dt", "dingtalk", json.RawMessage(`{"url":"http:///"}`))
	if err == nil {
		t.Fatal("expected an error for an address without a host")
	}
	prob := mustProblem(t, err)
	if prob.ID != keyErrorInvalidURL {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorInvalidURL)
	}
	if got, _ := prob.Data["URL"].(string); strings.Contains(got, exampleToken) {
		t.Fatalf("Data.URL = %q, want the token masked", got)
	}
}

// TestCreate_InvalidMsgTypeKeepsRawValue 覆盖 Data.Type 保留原始写法
//
// TestCreate_InvalidMsgTypeKeepsRawValue covers Data.Type keeping the value as written
func TestCreate_InvalidMsgTypeKeepsRawValue(t *testing.T) {
	_, err := channel.Create("dt", "dingtalk", json.RawMessage(`{"url":"https://example.com/robot/send?access_token=example","msgType":"Textual"}`))
	prob := mustProblem(t, err)
	if prob.ID != keyErrorInvalidMsgType {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorInvalidMsgType)
	}
	if got, _ := prob.Data["Type"].(string); got != "Textual" {
		t.Fatalf("Data.Type = %q, want Textual", got)
	}
}

// TestCreate_TemplateURLDeferredToPrepare 覆盖含模板的 url 在 Create 不校验, 到 Prepare 才判定
//
// TestCreate_TemplateURLDeferredToPrepare covers a templated url being unchecked at Create and
// judged in Prepare
func TestCreate_TemplateURLDeferredToPrepare(t *testing.T) {
	ch := newTestChannel(t, `{"url":"{{hook}}","secret":"{{sec}}"}`)
	vars := template.NewVars()
	vars.Set("hook", "https://example.com/robot/send?access_token="+exampleToken)
	vars.Set("sec", "")
	if _, _, err := prepareWith(ch, channel.Message{Body: "example", Vars: vars, Mode: channel.RenderStrict}); err != nil {
		t.Fatalf("Prepare with a resolved hook: %v", err)
	}

	// 渲染出的地址缺 access_token 时报 missing_access_token, 而不是模板错误
	// An address rendering without access_token reports missing_access_token rather than a template error
	missing := template.NewVars()
	missing.Set("hook", "https://example.com/robot/send")
	_, _, err := prepareWith(ch, channel.Message{Body: "example", Vars: missing, Mode: channel.RenderStrict})
	if got := mustProblem(t, err); got.ID != keyErrorMissingToken {
		t.Fatalf("ID = %q, want %q", got.ID, keyErrorMissingToken)
	}
}

// TestCreate_ATAllSpellings 覆盖 atAll 的字符串写法与缺省值
//
// TestCreate_ATAllSpellings covers the string spellings of atAll and its default
func TestCreate_ATAllSpellings(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`{"url":"URL","atAll":true}`, true},
		{`{"url":"URL","atAll":"true"}`, true},
		{`{"url":"URL","atAll":"1"}`, true},
		{`{"url":"URL","atAll":false}`, false},
		{`{"url":"URL","atAll":"0"}`, false},
		{`{"url":"URL"}`, false},
		{`{"url":"URL","atAll":null}`, false},
	}
	base := webhookURL("https://example.com")
	for _, c := range cases {
		raw := strings.ReplaceAll(c.raw, "URL", base)
		ch := newTestChannel(t, raw)
		prepared, _, err := prepareStrict(ch, "", "example body")
		if err != nil {
			t.Fatalf("%s: Prepare: %v", c.raw, err)
		}
		p := mustPrepared(t, prepared)
		body := decodePayload(t, p)
		at, hasAt := body["at"]
		if hasAt != c.want {
			t.Errorf("%s: at present = %v, want %v", c.raw, hasAt, c.want)
		}
		if c.want {
			atMap, ok := at.(map[string]any)
			if !ok || atMap["isAtAll"] != true {
				t.Errorf("%s: at = %v, want isAtAll true", c.raw, at)
			}
		}
	}
}

// TestPrepare_SigningVector 覆盖固定时钟下的签名取值
// 期望值由 HMAC-SHA256(secret, timestamp+"\n"+secret) 独立算出, 地址里的 sign 是转义后的 Base64
//
// TestPrepare_SigningVector covers the signature under a fixed clock
// The expected value comes from HMAC-SHA256(secret, timestamp+"\n"+secret) computed separately, and
// the address carries the escaped Base64 form of the sign
func TestPrepare_SigningVector(t *testing.T) {
	secret := "SECexample"
	ch := newTestChannel(t, `{"url":"`+webhookURL("https://example.com")+`","secret":"`+secret+`"}`)
	prepared, _, err := prepareStrict(ch, "", "example body")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := mustPrepared(t, prepared)

	u, err := url.Parse(p.endpoint)
	if err != nil {
		t.Fatalf("parse endpoint %q: %v", p.endpoint, err)
	}
	q := u.Query()
	if got := q.Get("timestamp"); got != "1767323045000" {
		t.Errorf("timestamp = %q, want 1767323045000 from Message.Now", got)
	}
	const wantSign = "7iWANc1LFWj+ApZkOUnO9etpGZhoblgl+ZoAf+liNCA="
	if got := q.Get("sign"); got != wantSign {
		t.Errorf("sign = %q, want %q", got, wantSign)
	}
	// 原始地址串里 sign 必须是单次转义形态, 不能出现 %25 双重编码
	// The raw address has to hold the sign escaped exactly once, with no double %25 form
	if strings.Contains(p.endpoint, "%25") {
		t.Errorf("endpoint %q looks double-escaped", p.endpoint)
	}
	if !strings.Contains(p.endpoint, "%2B") || !strings.Contains(p.endpoint, "%3D") {
		t.Errorf("endpoint %q: want the Base64 + and = escaped once", p.endpoint)
	}
	if q.Get("access_token") != exampleToken {
		t.Errorf("access_token = %q, want it kept", q.Get("access_token"))
	}
}

// TestPrepare_SigningReplacesExistingParams 覆盖预签名地址里的 timestamp/sign 被替换而非二次追加
//
// TestPrepare_SigningReplacesExistingParams covers a pre-signed address having timestamp and sign
// replaced rather than appended a second time
func TestPrepare_SigningReplacesExistingParams(t *testing.T) {
	base := "https://example.com/robot/send?access_token=" + exampleToken +
		"&timestamp=1000&sign=oldsign&keep=1"
	ch := newTestChannel(t, `{"url":"`+base+`","secret":"SECexample"}`)
	prepared, _, err := prepareStrict(ch, "title", "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := mustPrepared(t, prepared)

	if got := strings.Count(p.endpoint, "timestamp="); got != 1 {
		t.Fatalf("endpoint %q: timestamp appears %d times, want 1", p.endpoint, got)
	}
	if got := strings.Count(p.endpoint, "sign="); got != 1 {
		t.Fatalf("endpoint %q: sign appears %d times, want 1", p.endpoint, got)
	}
	u, err := url.Parse(p.endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	q := u.Query()
	if q.Get("timestamp") != "1767323045000" {
		t.Errorf("timestamp = %q, want the replaced value", q.Get("timestamp"))
	}
	if q.Get("sign") == "oldsign" {
		t.Error("sign kept the pre-signed value instead of being replaced")
	}
	if q.Get("keep") != "1" {
		t.Errorf("keep = %q, want the untouched extra parameter", q.Get("keep"))
	}
}

// TestPrepare_SigningKeepsFragment 覆盖 fragment 与 query 同时存在时都保留
//
// TestPrepare_SigningKeepsFragment covers a fragment and a query both surviving signing
func TestPrepare_SigningKeepsFragment(t *testing.T) {
	base := webhookURL("https://example.com") + "#anchor"
	ch := newTestChannel(t, `{"url":"`+base+`","secret":"SECexample"}`)
	prepared, _, err := prepareStrict(ch, "", "example body")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := mustPrepared(t, prepared)
	u, err := url.Parse(p.endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	if u.Fragment != "anchor" {
		t.Fatalf("fragment = %q, want anchor in %q", u.Fragment, p.endpoint)
	}
	if u.Query().Get("sign") == "" {
		t.Fatalf("endpoint %q lost the sign parameter", p.endpoint)
	}
}

// TestPrepare_NoSecretSkipsSigning 覆盖 secret 为空时不注入 timestamp/sign
//
// TestPrepare_NoSecretSkipsSigning covers an empty secret leaving timestamp and sign out
func TestPrepare_NoSecretSkipsSigning(t *testing.T) {
	for _, raw := range []string{
		`{"url":"` + webhookURL("https://example.com") + `"}`,
		`{"url":"` + webhookURL("https://example.com") + `","secret":"   "}`,
	} {
		ch := newTestChannel(t, raw)
		prepared, _, err := prepareStrict(ch, "", "example body")
		if err != nil {
			t.Fatalf("Prepare %s: %v", raw, err)
		}
		p := mustPrepared(t, prepared)
		q := mustParseQuery(t, p.endpoint)
		if _, ok := q["timestamp"]; ok {
			t.Errorf("%s: endpoint %q carries a timestamp without a secret", raw, p.endpoint)
		}
		if _, ok := q["sign"]; ok {
			t.Errorf("%s: endpoint %q carries a sign without a secret", raw, p.endpoint)
		}
		if q.Get("access_token") != exampleToken {
			t.Errorf("%s: access_token = %q", raw, q.Get("access_token"))
		}
	}
}

// mustParseQuery 解析端点地址的 query
//
// mustParseQuery parses the query of an endpoint address
func mustParseQuery(t *testing.T, endpoint string) url.Values {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint %q: %v", endpoint, err)
	}
	return u.Query()
}

// TestPrepare_MarkdownTitleTemplate 覆盖 secret 与 msgType 的模板渲染
//
// TestPrepare_MarkdownTitleTemplate covers template rendering of secret and msgType
func TestPrepare_MarkdownTitleTemplate(t *testing.T) {
	ch := newTestChannel(t, `{"url":"{{hook}}","secret":"{{sec}}","msgType":"{{kind}}"}`)
	vars := template.NewVars()
	vars.Set("hook", webhookURL("https://example.com"))
	vars.Set("sec", "SECexample")
	vars.Set("kind", "Markdown")
	prepared, _, err := prepareWith(ch, channel.Message{Title: "Example", Body: "body", Vars: vars, Mode: channel.RenderStrict})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := mustPrepared(t, prepared)
	if p.msgType != msgTypeMarkdown {
		t.Errorf("msgType = %q, want markdown after a case-insensitive render", p.msgType)
	}
	if mustParseQuery(t, p.endpoint).Get("sign") == "" {
		t.Error("the rendered secret did not reach the signature")
	}
}

// TestPrepare_MsgTypeRenderedInvalid 覆盖渲染出非法 msgType 时按同一枚举报错
//
// TestPrepare_MsgTypeRenderedInvalid covers a rendered msgType outside the enumeration reporting
// through the same key
func TestPrepare_MsgTypeRenderedInvalid(t *testing.T) {
	ch := newTestChannel(t, `{"url":"{{hook}}","msgType":"{{kind}}"}`)
	vars := template.NewVars()
	vars.Set("hook", webhookURL("https://example.com"))
	vars.Set("kind", "actionCard")
	_, _, err := prepareWith(ch, channel.Message{Body: "example", Vars: vars, Mode: channel.RenderStrict})
	if got := mustProblem(t, err); got.ID != keyErrorInvalidMsgType {
		t.Fatalf("ID = %q, want %q", got.ID, keyErrorInvalidMsgType)
	}
}

// TestPrepare_TextContentMapping 覆盖 text 的标题与正文组合
//
// TestPrepare_TextContentMapping covers the title and body combinations of a text message
func TestPrepare_TextContentMapping(t *testing.T) {
	base := webhookURL("https://example.com")
	cases := []struct {
		name  string
		title string
		body  string
		want  string
	}{
		{name: "both", title: "Example title", body: "Example body", want: "Example title\n\nExample body"},
		{name: "title only", title: "Example title", want: "Example title"},
		{name: "body only", body: "Example body", want: "Example body"},
	}
	for _, c := range cases {
		ch := newTestChannel(t, `{"url":"`+base+`"}`)
		prepared, warns, err := prepareStrict(ch, c.title, c.body)
		if err != nil {
			t.Fatalf("%s: Prepare: %v", c.name, err)
		}
		if len(warns) != 0 {
			t.Errorf("%s: warnings = %v, want none", c.name, warnIDs(warns))
		}
		p := mustPrepared(t, prepared)
		body := decodePayload(t, p)
		if body["msgtype"] != "text" {
			t.Errorf("%s: msgtype = %v, want text", c.name, body["msgtype"])
		}
		text, _ := body["text"].(map[string]any)
		if got := text["content"]; got != c.want {
			t.Errorf("%s: content = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPrepare_EmptyTextContent 覆盖 text 下标题正文都为空
//
// TestPrepare_EmptyTextContent covers a text message whose title and body are both empty
func TestPrepare_EmptyTextContent(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+webhookURL("https://example.com")+`"}`)
	_, _, err := prepareStrict(ch, "", "")
	prob := mustProblem(t, err)
	if prob.ID != keyErrorEmptyContent {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorEmptyContent)
	}
	if !apperr.IsPermanent(err) {
		t.Error("empty content is not permanent")
	}
}

// TestPrepare_MarkdownRules 覆盖 markdown 正文必填与默认标题
//
// TestPrepare_MarkdownRules covers the required markdown body and the default title
func TestPrepare_MarkdownRules(t *testing.T) {
	base := webhookURL("https://example.com")
	ch := newTestChannel(t, `{"url":"`+base+`","msgType":"markdown"}`)

	_, _, err := prepareStrict(ch, "Example title", "")
	if got := mustProblem(t, err); got.ID != keyErrorEmptyMarkdown {
		t.Fatalf("ID = %q, want %q", got.ID, keyErrorEmptyMarkdown)
	}

	prepared, _, err := prepareStrict(ch, "Example title", "**bold**")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	body := decodePayload(t, mustPrepared(t, prepared))
	if body["msgtype"] != "markdown" {
		t.Fatalf("msgtype = %v, want markdown", body["msgtype"])
	}
	md, _ := body["markdown"].(map[string]any)
	if md["title"] != "Example title" || md["text"] != "**bold**" {
		t.Errorf("markdown = %v, want the title and text as given", md)
	}

	// 标题为空时用 catalog 里的默认标题「通知」
	// An empty title takes the default "通知" from the catalog
	prepared, _, err = prepareStrict(ch, "", "body text")
	if err != nil {
		t.Fatalf("Prepare with an empty title: %v", err)
	}
	md, _ = decodePayload(t, mustPrepared(t, prepared))["markdown"].(map[string]any)
	if md["title"] != "通知" {
		t.Errorf("default title = %q, want 通知", md["title"])
	}
}

// TestPrepare_Truncation 覆盖 4000 字节截断与警告键
//
// TestPrepare_Truncation covers the 4000-byte cut and its warning key
func TestPrepare_Truncation(t *testing.T) {
	base := webhookURL("https://example.com")

	textCh := newTestChannel(t, `{"url":"`+base+`"}`)
	long := strings.Repeat("a", contentLimit+1)
	prepared, warns, err := prepareStrict(textCh, long, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := mustPrepared(t, prepared)
	content := decodePayload(t, p)["text"].(map[string]any)["content"].(string)
	if len(content) != contentLimit {
		t.Errorf("text content is %d bytes, want %d", len(content), contentLimit)
	}
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Fatalf("warnings = %v, want one %s", warnIDs(warns), keyWarnTruncated)
	}
	if got := warns[0].Data["Limit"]; got != contentLimit {
		t.Errorf("Limit = %v, want %d", got, contentLimit)
	}
	if !strings.Contains(string(p.payload), `"msgtype":"text"`) {
		t.Errorf("payload lost the msgtype field: %s", p.payload)
	}

	mdCh := newTestChannel(t, `{"url":"`+base+`","msgType":"markdown"}`)
	prepared, warns, err = prepareStrict(mdCh, "Example", strings.Repeat("字", 2001))
	if err != nil {
		t.Fatalf("markdown Prepare: %v", err)
	}
	text := decodePayload(t, mustPrepared(t, prepared))["markdown"].(map[string]any)["text"].(string)
	if len(text) != 3*1333 {
		t.Errorf("markdown body is %d bytes, want 3999 after a rune-boundary cut", len(text))
	}
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Errorf("markdown warnings = %v, want one truncation", warnIDs(warns))
	}
	if !isValidUTF8(text) {
		t.Error("the cut markdown body is not valid UTF-8")
	}

	// 恰好 4000 字节不触发截断
	// A body of exactly 4000 bytes raises no truncation
	exact := strings.Repeat("b", contentLimit)
	prepared, warns, err = prepareStrict(textCh, exact, "")
	if err != nil {
		t.Fatalf("Prepare with the exact limit: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("exact-limit warnings = %v, want none", warnIDs(warns))
	}
	content = decodePayload(t, mustPrepared(t, prepared))["text"].(map[string]any)["content"].(string)
	if len(content) != contentLimit {
		t.Errorf("exact-limit content is %d bytes, want %d", len(content), contentLimit)
	}
}

// isValidUTF8 判断字符串是否为合法 UTF-8
//
// isValidUTF8 reports whether a string is valid UTF-8
func isValidUTF8(s string) bool { return utf8.ValidString(s) }

// TestPrepare_ATAllRules 覆盖 atAll 只在 text 下允许, markdown 下报 at_all_markdown
//
// TestPrepare_ATAllRules covers atAll being allowed under text alone and reported under markdown
func TestPrepare_ATAllRules(t *testing.T) {
	base := webhookURL("https://example.com")
	md := newTestChannel(t, `{"url":"`+base+`","msgType":"markdown","atAll":true}`)
	_, _, err := prepareStrict(md, "Example", "body")
	prob := mustProblem(t, err)
	if prob.ID != keyErrorAtAllMarkdown {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorAtAllMarkdown)
	}
	if !apperr.IsPermanent(err) {
		t.Error("at_all_markdown is not permanent")
	}

	textCh := newTestChannel(t, `{"url":"`+base+`","msgType":"text","atAll":true}`)
	prepared, _, err := prepareStrict(textCh, "", "body")
	if err != nil {
		t.Fatalf("text Prepare with atAll: %v", err)
	}
	body := decodePayload(t, mustPrepared(t, prepared))
	if at, ok := body["at"].(map[string]any); !ok || at["isAtAll"] != true {
		t.Errorf("at = %v, want isAtAll true for text", body["at"])
	}

	// msgType 由模板渲染成 markdown 时同样拒绝 atAll
	// atAll is refused the same way when msgType renders to markdown
	rendered := newTestChannel(t, `{"url":"`+base+`","msgType":"{{kind}}","atAll":true}`)
	vars := template.NewVars()
	vars.Set("kind", "markdown")
	if _, _, err := prepareWith(rendered, channel.Message{Body: "body", Vars: vars, Mode: channel.RenderStrict}); err == nil {
		t.Fatal("expected at_all_markdown for a rendered markdown type")
	} else if got := mustProblem(t, err); got.ID != keyErrorAtAllMarkdown {
		t.Fatalf("ID = %q, want %q", got.ID, keyErrorAtAllMarkdown)
	}
}

// TestPrepare_CheckModeWarnings 覆盖 check 模式下未解析变量产生警告并继续
//
// TestPrepare_CheckModeWarnings covers check mode warning about an unresolved variable and going on
func TestPrepare_CheckModeWarnings(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+webhookURL("https://example.com")+`","secret":"{{missing}}"}`)
	prepared, warns, err := prepareWith(ch, channel.Message{
		Body: "example body",
		Vars: template.NewVars(),
		Mode: channel.RenderCheck,
	})
	if err != nil {
		t.Fatalf("Prepare in check mode: %v", err)
	}
	found := false
	for _, w := range warns {
		if w.ID == "template.warn.unresolved" {
			found = true
			if got, _ := w.Data["Path"].(string); got != "settings.secret" {
				t.Errorf("warning Path = %q, want settings.secret", got)
			}
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want an unresolved-variable warning", warnIDs(warns))
	}
	p := mustPrepared(t, prepared)
	// check 模式下未解析的 secret 保留字面量并告警, 因此这里的加签用的是那段字面文本
	// Under check mode an unresolved secret keeps its literal text with a warning, so the signature
	// is computed over that literal text
	if mustParseQuery(t, p.endpoint).Get("sign") == "" {
		t.Errorf("endpoint %q: the literal secret text should still produce a signature", p.endpoint)
	}
	if !strings.Contains(p.endpoint, "sign=") {
		t.Errorf("endpoint %q lost the signature parameter", p.endpoint)
	}
}

// TestPrepare_StrictModeError 覆盖严格模式下未解析变量报错
//
// TestPrepare_StrictModeError covers strict mode reporting an unresolved variable
func TestPrepare_StrictModeError(t *testing.T) {
	ch := newTestChannel(t, `{"url":"{{hook}}"}`)
	_, _, err := prepareWith(ch, channel.Message{Body: "example", Vars: template.NewVars(), Mode: channel.RenderStrict})
	if err == nil {
		t.Fatal("expected an unresolved-variable error")
	}
	if !apperr.IsPermanent(err) {
		t.Error("the render error is not permanent")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error %T is not *apperr.Problem", err)
	}
	if got, _ := prob.Data["Path"].(string); got != "settings.url" {
		t.Errorf("Data.Path = %q, want settings.url", got)
	}
}

// TestPrepare_SecretNotInPayload 覆盖 secret 只进入 URL, 不出现在请求体与错误文本
//
// TestPrepare_SecretNotInPayload covers the secret reaching the URL alone, not the request body or
// the error text
func TestPrepare_SecretNotInPayload(t *testing.T) {
	secret := "SECsupersecret"
	ch := newTestChannel(t, `{"url":"`+webhookURL("https://example.com")+`","secret":"`+secret+`"}`)
	prepared, _, err := prepareStrict(ch, "", "example body")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := mustPrepared(t, prepared)
	if strings.Contains(string(p.payload), secret) {
		t.Fatalf("payload %s holds the secret", p.payload)
	}
	if !strings.Contains(p.endpoint, "sign=") {
		t.Fatalf("endpoint %q holds no signature", p.endpoint)
	}
}

// TestPreview_Fields 覆盖预览只带 MsgType 与 Targets, 不含地址与 secret
//
// TestPreview_Fields covers Preview carrying MsgType and Targets alone, with no address or secret
func TestPreview_Fields(t *testing.T) {
	secret := "SECpreviewsecret"
	for _, msgType := range []string{"text", "markdown"} {
		ch := newTestChannel(t, `{"url":"`+webhookURL("https://example.com")+`","secret":"`+secret+`","msgType":"`+msgType+`"}`)
		prepared, _, err := prepareStrict(ch, "Example", "example body")
		if err != nil {
			t.Fatalf("Prepare %s: %v", msgType, err)
		}
		prob := prepared.Preview()
		if prob.ID != keyPreviewMessage {
			t.Fatalf("ID = %q, want %q", prob.ID, keyPreviewMessage)
		}
		if got := prob.Data["MsgType"]; got != msgType {
			t.Errorf("MsgType = %v, want %s", got, msgType)
		}
		if got := prob.Data["Targets"]; got != 1 {
			t.Errorf("Targets = %v, want 1", got)
		}
		if len(prob.Data) != 2 {
			t.Errorf("Data = %v, want two entries only", prob.Data)
		}
		encoded, err := json.Marshal(prob.Data)
		if err != nil {
			t.Fatalf("marshal Data: %v", err)
		}
		for _, forbidden := range []string{secret, exampleToken, "example.com", "sign"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Errorf("%s: Preview Data %s leaks %q", msgType, encoded, forbidden)
			}
		}
	}
}

// TestChannelIdentity 覆盖 ID 与 Type 由 Create 设置
//
// TestChannelIdentity covers Create setting the ID and the Type
func TestChannelIdentity(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+webhookURL("https://example.com")+`"}`)
	if ch.ID() != "dt" {
		t.Errorf("ID() = %q, want dt", ch.ID())
	}
	if ch.Type() != "dingtalk" {
		t.Errorf("Type() = %q, want dingtalk", ch.Type())
	}
}

// TestPrepare_NoNetworkUsesMessageNow 覆盖 Prepare 只依赖注入时钟, 同一消息重复准备结果一致
//
// TestPrepare_NoNetworkUsesMessageNow covers Prepare depending on the injected clock alone, so
// preparing the same message twice yields the same result
func TestPrepare_NoNetworkUsesMessageNow(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+webhookURL("https://example.com")+`","secret":"SECexample"}`)
	first, _, err := prepareStrict(ch, "", "example body")
	if err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	second, _, err := prepareStrict(ch, "", "example body")
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	if a, b := mustPrepared(t, first).endpoint, mustPrepared(t, second).endpoint; a != b {
		t.Errorf("endpoints differ across two Prepares: %q vs %q", a, b)
	}
	// 时钟改变时 timestamp 与 sign 随之改变, 证明签名读的是 Message.Now
	// A different clock changes timestamp and sign, which shows the signature reads Message.Now
	shifted, _, err := prepareWith(ch, channel.Message{
		Body: "example body",
		Now:  signedNow.Add(time.Second),
		Mode: channel.RenderStrict,
		Vars: template.NewVars(),
	})
	if err != nil {
		t.Fatalf("Prepare with a shifted clock: %v", err)
	}
	q := mustParseQuery(t, mustPrepared(t, shifted).endpoint)
	if q.Get("timestamp") != "1767323046000" {
		t.Errorf("timestamp = %q, want 1767323046000 one second later", q.Get("timestamp"))
	}
	if q.Get("sign") == mustParseQuery(t, mustPrepared(t, first).endpoint).Get("sign") {
		t.Error("the signature did not change with the clock")
	}
}
