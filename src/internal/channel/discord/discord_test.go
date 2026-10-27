package discord

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// discord 渠道测试: Create 严格解码与静态校验, Fields 完整性, Prepare 的渲染, 内容拼接, UTF-16
// 截断, username 与 avatarUrl 处理, Preview 脱敏与错误键
// 网络相关用例全部走本地 httptest 服务器, 不连公网
//
// Tests for the discord channel: strict decoding and static validation in Create, the completeness
// of Fields, rendering, content joining, UTF-16 truncation, username and avatarUrl handling in
// Prepare, preview redaction and the message keys
// Every network case runs against a local httptest server and never reaches the public internet
// ---------------------------------------------------------------------------

// webhookEndpoint 是一条形态合法的示例 webhook 地址, 域名为占位值
//
// webhookEndpoint is a syntactically valid sample webhook address whose host is a placeholder
const webhookEndpoint = "https://discord.example.com/api/webhooks/123456789/example-token"

// newTestChannel 构造使用指定 settings 的渠道实例
//
// newTestChannel builds a channel instance with the given settings
func newTestChannel(t *testing.T, settingsJSON string) channel.Channel {
	t.Helper()
	ch, err := channel.Create("dc", "discord", json.RawMessage(settingsJSON))
	if err != nil {
		t.Fatalf("channel.Create: %v", err)
	}
	return ch
}

// prepareStrict 以严格模式渲染消息
//
// prepareStrict renders one message under the strict mode
func prepareStrict(ch channel.Channel, title, body string) (channel.Prepared, []apperr.Problem, error) {
	return ch.Prepare(context.Background(), channel.Message{
		Title: title,
		Body:  body,
		Vars:  template.NewVars(),
		Mode:  channel.RenderStrict,
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

// payloadOf 解出 preparedChannel 里的请求体
//
// payloadOf decodes the request body inside a preparedChannel
func payloadOf(t *testing.T, p *preparedChannel) sendPayload {
	t.Helper()
	var payload sendPayload
	if err := json.Unmarshal(p.body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	return payload
}

// TestFactoryRegistration 覆盖 init 注册与工厂元数据
//
// TestFactoryRegistration covers the init registration and the factory metadata
func TestFactoryRegistration(t *testing.T) {
	if !channel.IsRegistered("discord") {
		t.Fatal("discord is not registered")
	}
	var f channel.Factory = factory{}
	if f.Type() != "discord" {
		t.Fatalf("Type() = %q, want discord", f.Type())
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
		{"username", "string", false},
		{"avatarUrl", "string", false},
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
		if got.DescriptionKey == "" || !strings.HasPrefix(got.DescriptionKey, "channel.discord.field.") {
			t.Errorf("field %s: unexpected DescriptionKey %q", got.Name, got.DescriptionKey)
		}
	}
}

// TestCreate_Strict 覆盖未知字段, 字段类型错误与非对象 settings
//
// TestCreate_Strict covers an unknown field, wrong field types and non-object settings
func TestCreate_Strict(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"unknown key", `{"url":"` + webhookEndpoint + `","nope":true}`},
		{"username number", `{"url":"` + webhookEndpoint + `","username":123}`},
		{"avatarUrl bool", `{"url":"` + webhookEndpoint + `","avatarUrl":false}`},
		{"url array", `{"url":["` + webhookEndpoint + `"]}`},
		{"settings not object", `["url"]`},
		{"settings scalar", `"discord"`},
	}
	for _, c := range cases {
		_, err := channel.Create("dc", "discord", json.RawMessage(c.raw))
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		prob := mustProblem(t, err)
		if prob.ID != "channel.error.invalid_settings" {
			t.Errorf("%s: ID = %q, want channel.error.invalid_settings", c.name, prob.ID)
		}
		if !apperr.IsPermanent(err) {
			t.Errorf("%s: error is not permanent", c.name)
		}
	}
}

// TestCreate_MissingURL 覆盖 url 缺失, 空串与纯空白
//
// TestCreate_MissingURL covers a missing, empty and whitespace-only url
func TestCreate_MissingURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing", `{}`},
		{"empty settings", ``},
		{"empty string", `{"url":""}`},
		{"whitespace only", `{"url":"   "}`},
	}
	for _, c := range cases {
		_, err := channel.Create("dc", "discord", json.RawMessage(c.raw))
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		prob := mustProblem(t, err)
		if prob.ID != keyErrorMissingURL {
			t.Errorf("%s: ID = %q, want %q", c.name, prob.ID, keyErrorMissingURL)
		}
		if !apperr.IsPermanent(err) {
			t.Errorf("%s: error is not permanent", c.name)
		}
	}
}

// TestCreate_StaticURLValidation 覆盖 Create 阶段对不含模板的 url 的静态校验
//
// TestCreate_StaticURLValidation covers the static url check Create performs when the value holds no
// template
func TestCreate_StaticURLValidation(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"full address", `{"url":"` + webhookEndpoint + `"}`, false},
		{"scheme added", `{"url":"discord.example.com/api/webhooks/1/token"}`, false},
		{"loopback http", `{"url":"http://127.0.0.1:8080/hook"}`, false},
		{"ftp scheme", `{"url":"ftp://discord.example.com/hook"}`, true},
		{"empty host", `{"url":"https:///hook"}`, true},
		{"template deferred", `{"url":"{{hook}}"}`, false},
	}
	for _, c := range cases {
		_, err := channel.Create("dc", "discord", json.RawMessage(c.raw))
		if c.wantErr && err == nil {
			t.Errorf("%s: expected an error", c.name)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if c.wantErr {
			prob := mustProblem(t, err)
			if prob.ID != keyErrorInvalidURL {
				t.Errorf("%s: ID = %q, want %q", c.name, prob.ID, keyErrorInvalidURL)
			}
			if !apperr.IsPermanent(err) {
				t.Errorf("%s: error is not permanent", c.name)
			}
		}
	}
}

// TestCreateAttributes 覆盖 ID 与 Type 的写入
//
// TestCreateAttributes covers the ID and Type written onto the instance
func TestCreateAttributes(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+webhookEndpoint+`"}`)
	if ch.ID() != "dc" {
		t.Fatalf("ID() = %q, want dc", ch.ID())
	}
	if ch.Type() != "discord" {
		t.Fatalf("Type() = %q, want discord", ch.Type())
	}
}

// TestPrepare_JoinContent 覆盖标题正文组合与请求体字段
//
// TestPrepare_JoinContent covers the title and body composition and the request body fields
func TestPrepare_JoinContent(t *testing.T) {
	cases := []struct {
		name     string
		title    string
		body     string
		wantText string
	}{
		{"both", "Build done", "all green", "Build done\n\nall green"},
		{"title only", "Build done", "", "Build done"},
		{"body only", "", "all green", "all green"},
	}
	for _, c := range cases {
		ch := newTestChannel(t, `{"url":"`+webhookEndpoint+`"}`)
		prepared, warns, err := prepareStrict(ch, c.title, c.body)
		if err != nil {
			t.Fatalf("%s: Prepare: %v", c.name, err)
		}
		if len(warns) != 0 {
			t.Fatalf("%s: unexpected warnings %v", c.name, warns)
		}
		payload := payloadOf(t, preparedOf(t, prepared))
		if payload.Content != c.wantText {
			t.Errorf("%s: content = %q, want %q", c.name, payload.Content, c.wantText)
		}
		if payload.Username != "" || payload.AvatarURL != "" {
			t.Errorf("%s: optional fields were sent: %+v", c.name, payload)
		}
	}
}

// TestPrepare_EmptyContent 覆盖标题正文都空时报 empty_content
//
// TestPrepare_EmptyContent covers the empty_content error when both the title and the body are empty
func TestPrepare_EmptyContent(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+webhookEndpoint+`"}`)
	_, _, err := prepareStrict(ch, "", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	prob := mustProblem(t, err)
	if prob.ID != keyErrorEmptyContent {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorEmptyContent)
	}
	if !apperr.IsPermanent(err) {
		t.Error("empty content is not permanent")
	}
}

// TestPrepare_TruncateUTF16 覆盖 2000 UTF-16 截断: 上限内不动, 超限整体丢字符并发警告
//
// TestPrepare_TruncateUTF16 covers the 2000 UTF-16 cut: a value within the limit stays as it is,
// while an oversized one drops the character that does not fit whole and raises a warning
func TestPrepare_TruncateUTF16(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+webhookEndpoint+`"}`)

	// 正好 2000 单元 (2 ASCII + 999 个非 BMP 字符各占 2 单元) 不截断
	//
	// Exactly 2000 units, two ASCII plus 999 non-BMP characters at two units each, is not cut
	exact := "ab" + strings.Repeat("\U0001F600", 999)
	if got := utf16Len(exact); got != contentLimitUTF16 {
		t.Fatalf("fixture has %d UTF-16 units, want %d", got, contentLimitUTF16)
	}
	prepared, warns, err := prepareStrict(ch, exact, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("exact-limit warnings = %v", warns)
	}
	if got := payloadOf(t, preparedOf(t, prepared)).Content; got != exact {
		t.Fatal("a content exactly at the limit was altered")
	}

	// 1999 个 BMP 字符 + 一个非 BMP 字符: 预算剩 1 单元放不下 2 单元的字符, 整个字符被丢掉
	//
	// 1999 BMP characters plus one non-BMP character: the budget has one unit left and the two-unit
	// character does not fit, so it is dropped whole
	overflow := strings.Repeat("a", contentLimitUTF16-1) + "\U0001F600"
	prepared, warns, err = prepareStrict(ch, overflow, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	got := payloadOf(t, preparedOf(t, prepared)).Content
	if got != strings.Repeat("a", contentLimitUTF16-1) {
		t.Fatalf("cut content has %d UTF-16 units, want %d", utf16Len(got), contentLimitUTF16-1)
	}
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Fatalf("warnings = %v, want one %s", warns, keyWarnTruncated)
	}
	if warns[0].Data["Limit"] != contentLimitUTF16 {
		t.Errorf("warning Limit = %v, want %d", warns[0].Data["Limit"], contentLimitUTF16)
	}

	// 远超上限的 ASCII 内容截到正好 2000 单元
	//
	// A long ASCII content is cut to exactly 2000 units
	prepared, warns, err = prepareStrict(ch, strings.Repeat("a", contentLimitUTF16+50), "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	got = payloadOf(t, preparedOf(t, prepared)).Content
	if utf16Len(got) != contentLimitUTF16 {
		t.Fatalf("cut content has %d UTF-16 units, want %d", utf16Len(got), contentLimitUTF16)
	}
	// 截断告警与模板层观点一致: 只发一次, 键为本渠道的 warn.truncated
	//
	// The truncation warning matches the template layer: one entry with this channel's
	// warn.truncated key
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Fatalf("warnings = %v, want one %s", warns, keyWarnTruncated)
	}
}

// TestPrepare_Username 覆盖 username 空, 非空与渲染后为空
//
// TestPrepare_Username covers an empty username, a non-empty one and one that renders empty
func TestPrepare_Username(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		want     string
	}{
		{"omitted", `{"url":"` + webhookEndpoint + `"}`, ""},
		{"empty", `{"url":"` + webhookEndpoint + `","username":""}`, ""},
		{"whitespace", `{"url":"` + webhookEndpoint + `","username":"   "}`, ""},
		{"value", `{"url":"` + webhookEndpoint + `","username":"TaskPush"}`, "TaskPush"},
		{"trimmed", `{"url":"` + webhookEndpoint + `","username":"  TaskPush  "}`, "TaskPush"},
	}
	for _, c := range cases {
		prepared, _, err := prepareStrict(newTestChannel(t, c.settings), "t", "b")
		if err != nil {
			t.Fatalf("%s: Prepare: %v", c.name, err)
		}
		if got := payloadOf(t, preparedOf(t, prepared)).Username; got != c.want {
			t.Errorf("%s: username = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPrepare_AvatarURL 覆盖 avatarUrl 合法, 非法与空
//
// TestPrepare_AvatarURL covers a valid avatarUrl, an invalid one and an empty one
func TestPrepare_AvatarURL(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		want     string
		wantErr  string
	}{
		{"omitted", `{"url":"` + webhookEndpoint + `"}`, "", ""},
		{"empty", `{"url":"` + webhookEndpoint + `","avatarUrl":""}`, "", ""},
		{"valid", `{"url":"` + webhookEndpoint + `","avatarUrl":"https://example.com/avatar.png"}`, "https://example.com/avatar.png", ""},
		{"no scheme", `{"url":"` + webhookEndpoint + `","avatarUrl":"example.com/avatar.png"}`, "https://example.com/avatar.png", ""},
		{"trimmed", `{"url":"` + webhookEndpoint + `","avatarUrl":"  https://example.com/avatar.png  "}`, "https://example.com/avatar.png", ""},
		{"ftp scheme", `{"url":"` + webhookEndpoint + `","avatarUrl":"ftp://example.com/avatar.png"}`, "", keyErrorInvalidAvatar},
		{"empty host", `{"url":"` + webhookEndpoint + `","avatarUrl":"https:///avatar.png"}`, "", keyErrorInvalidAvatar},
		// 纯空白按未渲染出值处理, 与 username 的判空一致, 不发该字段
		//
		// Whitespace alone counts as no rendered value, matching the emptiness rule of username, so
		// the field is not sent
		{"blank", `{"url":"` + webhookEndpoint + `","avatarUrl":"   "}`, "", ""},
	}
	for _, c := range cases {
		prepared, _, err := prepareStrict(newTestChannel(t, c.settings), "t", "b")
		if c.wantErr != "" {
			if err == nil {
				t.Errorf("%s: expected an error", c.name)
				continue
			}
			prob := mustProblem(t, err)
			if prob.ID != c.wantErr {
				t.Errorf("%s: ID = %q, want %q", c.name, prob.ID, c.wantErr)
			}
			if !apperr.IsPermanent(err) {
				t.Errorf("%s: error is not permanent", c.name)
			}
			if _, ok := prob.Data["URL"]; !ok {
				t.Errorf("%s: Data has no URL placeholder: %v", c.name, prob.Data)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: Prepare: %v", c.name, err)
		}
		if got := payloadOf(t, preparedOf(t, prepared)).AvatarURL; got != c.want {
			t.Errorf("%s: avatar_url = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPrepare_InvalidAvatarURLData 覆盖 avatarUrl 校验错误里的地址经 MaskURL 打码
// MaskURL 保留 path 与普通 query, 只替换 userinfo 与敏感 query 参数的值
//
// TestPrepare_InvalidAvatarURLData covers the avatar address inside a validation error being masked
// by MaskURL
// MaskURL keeps the path and an ordinary query, and replaces a userinfo and a sensitive query value
func TestPrepare_InvalidAvatarURLData(t *testing.T) {
	// 端口写法非法使地址通不过校验, 同时带上 userinfo 与敏感 query 供脱敏断言
	//
	// A malformed port fails the check, and the same address carries a userinfo and a sensitive
	// query for the redaction assertions
	settings := `{"url":"` + webhookEndpoint + `","avatarUrl":"https://user:pass@example.com:notaport/avatar.png?token=example-secret"}`
	_, _, err := prepareStrict(newTestChannel(t, settings), "t", "b")
	prob := mustProblem(t, err)
	if prob.ID != keyErrorInvalidAvatar {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorInvalidAvatar)
	}
	masked, _ := prob.Data["URL"].(string)
	if strings.Contains(masked, "pass") || strings.Contains(masked, "user:") {
		t.Fatalf("Data.URL kept the userinfo: %q", masked)
	}
	if strings.Contains(masked, "example-secret") {
		t.Fatalf("Data.URL kept the token value: %q", masked)
	}
	// MaskURL 对不可解析的地址返回兜底脱敏值, 因此地址的任何片段都不会留在 Data 里
	//
	// MaskURL returns its fallback redaction value for an address it cannot parse, so no fragment of
	// the address stays in Data
	want := common.MaskURL("https://user:pass@example.com:notaport/avatar.png?token=example-secret")
	if masked != want {
		t.Fatalf("Data.URL = %q, want %q", masked, want)
	}
	if cause := prob.Cause; cause != nil && strings.Contains(cause.Error(), "pass") {
		t.Fatalf("Cause kept the userinfo: %v", cause)
	}
}

// TestPrepare_InvalidURLDataMasksWebhookToken 覆盖非法 webhook 地址错误里的 id 与 token 遮蔽
// MaskURL 保留 path, 因此渠道在 webhook 形态的地址上再遮蔽末两段路径
//
// TestPrepare_InvalidURLDataMasksWebhookToken covers the id and the token inside an invalid webhook
// address being masked
// MaskURL keeps the path, so the channel masks the last two path segments of a webhook-shaped
// address as well
func TestPrepare_InvalidURLDataMasksWebhookToken(t *testing.T) {
	// 用带模板的地址把校验落到 Prepare, 其中 ftp 与 token 路径都无法通过 NormalizeURL
	ch := newTestChannel(t, `{"url":"{{hook}}"}`)
	vars := template.NewVars()
	vars.Set("hook", "ftp://discord.example.com/api/webhooks/123456789/example-token")
	_, _, err := ch.Prepare(context.Background(), channel.Message{
		Body: "b", Vars: vars, Mode: channel.RenderStrict,
	})
	prob := mustProblem(t, err)
	if prob.ID != keyErrorInvalidURL {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorInvalidURL)
	}
	masked, _ := prob.Data["URL"].(string)
	if strings.Contains(masked, "example-token") || strings.Contains(masked, "123456789") {
		t.Fatalf("Data.URL leaked a webhook credential: %q", masked)
	}
	if !strings.Contains(masked, "/webhooks/") {
		t.Fatalf("Data.URL lost the webhook path: %q", masked)
	}
}

// TestPrepare_URLNormalization 覆盖 url 渲染后的规范化: 补 scheme, 回环补 http, 解析后仍校验
// 非法 scheme 在 Create 就被拒, 因此这里用带模板的地址让校验落到 Prepare
//
// TestPrepare_URLNormalization covers the normalisation after rendering: a scheme is added, a
// loopback host takes http, and the parsed result is still validated
// An invalid scheme is rejected in Create already, so a templated address is what puts the check
// into Prepare
func TestPrepare_URLNormalization(t *testing.T) {
	cases := []struct {
		name    string
		rawURL  string
		varURL  string
		want    string
		wantErr string
	}{
		{"already normal", webhookEndpoint, "", webhookEndpoint, ""},
		{"scheme added", "discord.example.com/api/webhooks/1/token", "", "https://discord.example.com/api/webhooks/1/token", ""},
		{"loopback", "127.0.0.1:8080/hook", "", "http://127.0.0.1:8080/hook", ""},
		{"surrounding space", "  " + webhookEndpoint + "  ", "", webhookEndpoint, ""},
		// 非法 scheme 在 Create 就被拒, 因此这两条用带模板的地址把校验落到 Prepare
		//
		// An invalid scheme is rejected in Create already, so these two use a templated address to
		// put the check into Prepare
		{"ftp scheme", "{{url}}", "ftp://discord.example.com/hook", "", keyErrorInvalidURL},
		{"empty host", "{{url}}", "https:///hook", "", keyErrorInvalidURL},
	}
	for _, c := range cases {
		ch := newTestChannel(t, `{"url":"`+c.rawURL+`"}`)
		vars := template.NewVars()
		if c.varURL != "" {
			vars.Set("url", c.varURL)
		}
		prepared, _, err := ch.Prepare(context.Background(), channel.Message{
			Body: "b", Vars: vars, Mode: channel.RenderStrict,
		})
		if c.wantErr != "" {
			if err == nil {
				t.Errorf("%s: expected an error", c.name)
				continue
			}
			if prob := mustProblem(t, err); prob.ID != c.wantErr {
				t.Errorf("%s: ID = %q, want %q", c.name, prob.ID, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: Prepare: %v", c.name, err)
		}
		if got := preparedOf(t, prepared).endpoint; got != c.want {
			t.Errorf("%s: endpoint = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPrepare_EmptyRenderedURL 覆盖渲染后 url 变空时报 missing_url
//
// TestPrepare_EmptyRenderedURL covers a url that renders empty reporting missing_url
func TestPrepare_EmptyRenderedURL(t *testing.T) {
	ch := newTestChannel(t, `{"url":"{{hook}}"}`)
	vars := template.NewVars()
	vars.Set("hook", "   ")
	_, _, err := ch.Prepare(context.Background(), channel.Message{
		Body: "b", Vars: vars, Mode: channel.RenderStrict,
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	prob := mustProblem(t, err)
	if prob.ID != keyErrorMissingURL {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorMissingURL)
	}
}

// TestPrepare_TemplateRendering 覆盖 settings 模板渲染与 Strict/Check 两种模式
//
// TestPrepare_TemplateRendering covers settings template rendering under both Strict and Check
func TestPrepare_TemplateRendering(t *testing.T) {
	ch := newTestChannel(t, `{"url":"{{hook}}","username":"{{name}}","avatarUrl":"{{avatar}}"}`)
	vars := template.NewVars()
	vars.Set("hook", "discord.example.com/api/webhooks/1/token")
	vars.Set("name", "TaskPush")
	vars.Set("avatar", "https://example.com/avatar.png")

	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{
		Title: "t", Body: "b", Vars: vars, Mode: channel.RenderStrict,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings %v", warns)
	}
	p := preparedOf(t, prepared)
	if p.endpoint != "https://discord.example.com/api/webhooks/1/token" {
		t.Fatalf("endpoint = %q", p.endpoint)
	}
	payload := payloadOf(t, p)
	if payload.Username != "TaskPush" || payload.AvatarURL != "https://example.com/avatar.png" {
		t.Fatalf("payload = %+v", payload)
	}

	// 严格模式: 未解析变量报错, 且 Path 被改写为触发的字段路径
	//
	// Strict mode: an unresolved variable fails, with Path rewritten to the field that raised it
	strict := newTestChannel(t, `{"url":"{{hook}}"}`)
	_, _, err = prepareStrict(strict, "t", "b")
	prob := mustProblem(t, err)
	if prob.ID != "template.error.unresolved" {
		t.Fatalf("ID = %q, want template.error.unresolved", prob.ID)
	}
	if prob.Data["Path"] != settingsPathURL {
		t.Fatalf("Path = %v, want %s", prob.Data["Path"], settingsPathURL)
	}

	// check 模式: 未解析变量保留字面并告警, url 校验随后按该字面值判定
	//
	// Check mode: an unresolved variable stays literal with a warning, and the url check then reads
	// that literal value
	_, warns, err = strict.Prepare(context.Background(), channel.Message{
		Title: "t", Body: "b", Vars: template.NewVars(), Mode: channel.RenderCheck,
	})
	foundUnresolved := false
	for _, w := range warns {
		if w.ID == "template.warn.unresolved" && w.Data["Path"] == settingsPathURL {
			foundUnresolved = true
		}
	}
	if !foundUnresolved {
		t.Fatalf("warnings = %v, want an unresolved warning for %s", warns, settingsPathURL)
	}
	if err == nil {
		t.Fatal("check mode accepted the retained literal as a webhook address")
	}
	if prob := mustProblem(t, err); prob.ID != keyErrorInvalidURL {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorInvalidURL)
	}
}

// TestPrepare_NoNetwork 覆盖 Prepare 阶段不发请求
//
// TestPrepare_NoNetwork covers Prepare making no request
func TestPrepare_NoNetwork(t *testing.T) {
	var hits int
	srv := newWebhookServer(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusNoContent)
	})
	ch := newTestChannel(t, `{"url":`+strconvQuote(srv.URL+"/api/webhooks/1/token")+`}`)
	if _, _, err := prepareStrict(ch, "t", "b"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if hits != 0 {
		t.Fatalf("Prepare reached the network %d times, want 0", hits)
	}
}

// TestPreview_TargetsAndNoSecrets 覆盖预览的目标数与脱敏
//
// TestPreview_TargetsAndNoSecrets covers the preview target count and its redaction
func TestPreview_TargetsAndNoSecrets(t *testing.T) {
	settings := `{"url":"` + webhookEndpoint + `","username":"TaskPush"}`
	prepared, _, err := prepareStrict(newTestChannel(t, settings), "Build done", "commit abc123")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	preview := prepared.Preview()
	if preview.ID != keyPreviewMessage {
		t.Fatalf("preview ID = %q, want %q", preview.ID, keyPreviewMessage)
	}
	if preview.Data["Targets"] != 1 {
		t.Fatalf("preview Targets = %v, want 1", preview.Data["Targets"])
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	for _, secret := range []string{"example-token", "123456789", "discord.example.com", "Build done", "abc123"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the preview leaked %q: %s", secret, encoded)
		}
	}
}

// TestPrepare_EndpointNoHTMLEscapeLoss 覆盖请求体里的正文原样保留, 不做 HTML 转义
//
// TestPrepare_EndpointNoHTMLEscapeLoss covers the content staying as written in the request body
// without HTML escaping
func TestPrepare_EndpointNoHTMLEscapeLoss(t *testing.T) {
	prepared, _, err := prepareStrict(newTestChannel(t, `{"url":"`+webhookEndpoint+`"}`), "a <b> & c", "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := payloadOf(t, preparedOf(t, prepared)).Content; got != "a <b> & c" {
		t.Fatalf("content = %q, want the original text", got)
	}
}

// TestPrepare_TemplateWarningPath 覆盖 username 字段模板告警里的 Path 改写
//
// TestPrepare_TemplateWarningPath covers the Path rewritten inside a template warning raised on the
// username field
func TestPrepare_TemplateWarningPath(t *testing.T) {
	ch := newTestChannel(t, `{"url":"`+webhookEndpoint+`","username":"{{name}}"}`)
	_, warns, err := ch.Prepare(context.Background(), channel.Message{
		Title: "t", Body: "b", Vars: template.NewVars(), Mode: channel.RenderCheck,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want one", warns)
	}
	if warns[0].ID != "template.warn.unresolved" || warns[0].Data["Path"] != settingsPathUsername {
		t.Fatalf("warning = %+v, want an unresolved warning for %s", warns[0], settingsPathUsername)
	}
	if _, ok := warns[0].Data["Variable"]; !ok {
		t.Fatalf("warning Data has no Variable: %v", warns[0].Data)
	}
}

// TestPreparedInterface 覆盖 Prepared 的接口形态与重复 Preview 的无副作用
//
// TestPreparedInterface covers the shape of the Prepared implementation and the lack of a side
// effect in a repeated Preview
func TestPreparedInterface(t *testing.T) {
	prepared, _, err := prepareStrict(newTestChannel(t, `{"url":"`+webhookEndpoint+`"}`), "t", "b")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if first, second := prepared.Preview(), prepared.Preview(); first.ID != second.ID {
		t.Fatalf("Preview IDs differ: %q and %q", first.ID, second.ID)
	}
}

// TestWebhookSecrets 覆盖 webhook 地址里 id 与 token 的提取
//
// TestWebhookSecrets covers reading the id and the token out of a webhook address
func TestWebhookSecrets(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"full address", webhookEndpoint, "example-token,123456789"},
		{"trailing slash", webhookEndpoint + "/", "example-token,123456789"},
		{"one segment", "https://discord.example.com/token", "token"},
		{"no path", "https://discord.example.com", ""},
		{"empty", "", ""},
		{"unparsable", "https://exam ple.com/hook/token", ""},
	}
	for _, c := range cases {
		if got := strings.Join(webhookSecrets(c.endpoint), ","); got != c.want {
			t.Errorf("%s: webhookSecrets = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestMaskURLSecrets 覆盖 MaskURL 结果上的路径级遮蔽
//
// TestMaskURLSecrets covers the path-level masking applied on top of MaskURL
func TestMaskURLSecrets(t *testing.T) {
	got := maskURLSecrets(webhookEndpoint)
	// MaskURL 保留 scheme, host 与 path, 而 id 与 token 两段被替换成 ***
	//
	// MaskURL keeps the scheme, the host and the path, while the id and the token segments become
	// the mask value
	want := "https://discord.example.com/api/webhooks/***/***"
	if got != want {
		t.Fatalf("maskURLSecrets = %q, want %q", got, want)
	}
	// avatarUrl 这类无 webhook 路径的地址不受影响, 敏感 query 仍由 MaskURL 处理
	//
	// An address without a webhook path, such as an avatarUrl, is unaffected, and a sensitive query
	// is still handled by MaskURL
	if plain := maskURLSecrets("https://example.com/avatar.png?token=abc"); plain != "https://example.com/avatar.png?token=***" {
		t.Fatalf("maskURLSecrets changed an unrelated address: %q", plain)
	}
}

// TestMaskServerText 覆盖服务端回显文本里的地址与 token 遮蔽
//
// TestMaskServerText covers masking the address and the token inside server-supplied text
func TestMaskServerText(t *testing.T) {
	p := &preparedChannel{endpoint: webhookEndpoint}
	got := p.maskServerText("rejected " + webhookEndpoint + " with token example-token")
	if strings.Contains(got, "example-token") || strings.Contains(got, "discord.example.com") {
		t.Fatalf("maskServerText leaked a credential: %q", got)
	}
	if !strings.Contains(got, maskValue) {
		t.Fatalf("maskServerText produced no mask: %q", got)
	}
	// endpoint 为空时 sanitize 结果原样返回, 不因缺少 token 而丢弃地址打码
	//
	// With an empty endpoint the sanitised text comes back as it is, so the address masking is not
	// skipped just because no token exists
	empty := (&preparedChannel{}).maskServerText("see https://example.com/path")
	if strings.Contains(empty, "example.com") {
		t.Fatalf("maskServerText left an address unmasked: %q", empty)
	}
}

// TestHTTPSStatusErrorFallback 覆盖非 Problem 错误经 SanitizeError 返回
//
// TestHTTPSStatusErrorFallback covers a non-Problem error coming back through SanitizeError
func TestHTTPSStatusErrorFallback(t *testing.T) {
	p := &preparedChannel{endpoint: webhookEndpoint}
	err := p.httpStatusError(errors.New("dial https://discord.example.com/api/webhooks/1/token: refused"))
	if strings.Contains(err.Error(), "example-token") || strings.Contains(err.Error(), "discord.example.com") {
		t.Fatalf("the fallback error leaked the address: %v", err)
	}
}

// TestHTTPStatusErrorMessage 覆盖状态码错误里的 token 遮蔽
//
// TestHTTPStatusErrorMessage covers masking the token inside a status-code error
func TestHTTPStatusErrorMessage(t *testing.T) {
	p := &preparedChannel{endpoint: webhookEndpoint}
	wrapped := p.httpStatusError(&apperr.Problem{
		ID: "common.error.http_status",
		Data: map[string]any{
			"Status": http.StatusBadRequest,
			"Body":   "bad webhook https://discord.example.com/api/webhooks/1/example-token",
		},
	})
	prob := mustProblem(t, wrapped)
	if prob.ID != keyErrorHTTPStatus {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorHTTPStatus)
	}
	if body, _ := prob.Data["Body"].(string); strings.Contains(body, "example-token") {
		t.Fatalf("the status error leaked the token: %q", body)
	}
	if prob.Data["Status"] != http.StatusBadRequest {
		t.Fatalf("Status = %v, want %d", prob.Data["Status"], http.StatusBadRequest)
	}
}

// strconvQuote 输出带引号的 JSON 字符串字面量
//
// strconvQuote renders a JSON string literal with quotes
func strconvQuote(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// utf16Len 计算 UTF-16 code unit 数
//
// utf16Len counts the UTF-16 code units
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// Example 是占位示例, 说明一次 Prepare 与 Preview 的用法
//
// Example is a placeholder example showing one Prepare and Preview round
func Example() {
	ch, err := (factory{}).Create("example", json.RawMessage(`{"url":"https://discord.example.com/api/webhooks/123456789/example-token"}`))
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
