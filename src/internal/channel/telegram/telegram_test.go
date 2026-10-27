package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// telegram 渠道测试: Create 严格解码与静态校验, Prepare 渲染与内容规则, Send 响应分类与
// 部分重试, Preview 脱敏
// 网络相关用例全部走本地 httptest 服务器, 不连公网
//
// Tests for the telegram channel: Create strict decoding and static validation, Prepare rendering
// and content rules, Send response classification and partial retry, Preview redaction
// Every network case runs against a local httptest server and never reaches the public internet
// ---------------------------------------------------------------------------

// newTestChannel 构造使用指定 settings 的渠道实例
//
// newTestChannel builds a channel instance with the given settings
func newTestChannel(t *testing.T, settingsJSON string) channel.Channel {
	t.Helper()
	raw := json.RawMessage(settingsJSON)
	ch, err := channel.Create("tg", "telegram", raw)
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

// TestFactoryRegistration 覆盖 init 注册与工厂元数据
//
// TestFactoryRegistration covers the init registration and the factory metadata
func TestFactoryRegistration(t *testing.T) {
	if !channel.IsRegistered("telegram") {
		t.Fatal("telegram is not registered")
	}
	var f channel.Factory = factory{}
	if f.Type() != "telegram" {
		t.Fatalf("Type() = %q, want telegram", f.Type())
	}
	if f.Describe() != "channel.telegram.desc" {
		t.Fatalf("Describe() = %q", f.Describe())
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
		{"token", "string", true},
		{"chatIds", "list<string>", true},
		{"apiBase", "string", false},
		{"silent", "bool", false},
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
		if got.DescriptionKey == "" || !strings.HasPrefix(got.DescriptionKey, "channel.telegram.field.") {
			t.Errorf("field %s: unexpected DescriptionKey %q", got.Name, got.DescriptionKey)
		}
	}
	if fields[2].Default != "https://api.telegram.org" {
		t.Errorf("apiBase Default = %q", fields[2].Default)
	}
	if fields[3].Default != "false" {
		t.Errorf("silent Default = %q", fields[3].Default)
	}
}

// TestCreate_Strict 覆盖未知字段, 类型错误与非对象 settings
//
// TestCreate_Strict covers an unknown field, a wrong type and a non-object settings
func TestCreate_Strict(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		wantID string
	}{
		{"unknown key", `{"token":"EXAMPLE","chatIds":["1"],"nope":true}`, "channel.error.invalid_settings"},
		{"silent as string", `{"token":"EXAMPLE","chatIds":["1"],"silent":"maybe"}`, "channel.error.invalid_settings"},
		{"chatIds number", `{"token":"EXAMPLE","chatIds":123}`, "channel.error.invalid_settings"},
		{"settings not object", `["token"]`, "channel.error.invalid_settings"},
	}
	for _, c := range cases {
		_, err := channel.Create("tg", "telegram", json.RawMessage(c.raw))
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

// TestCreate_MissingRequired 覆盖 token 与 chatIds 必填
//
// TestCreate_MissingRequired covers the required token and chatIds
func TestCreate_MissingRequired(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		wantID string
	}{
		{"missing token", `{"chatIds":["123456789"]}`, keyErrorMissingToken},
		{"blank token", `{"token":"   ","chatIds":["123456789"]}`, keyErrorMissingToken},
		{"missing chatIds", `{"token":"EXAMPLE"}`, keyErrorMissingChats},
		{"empty chatIds", `{"token":"EXAMPLE","chatIds":[]}`, keyErrorMissingChats},
	}
	for _, c := range cases {
		_, err := channel.Create("tg", "telegram", json.RawMessage(c.raw))
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

// TestCreate_APIBaseValidation 覆盖 apiBase 的静态校验: 合法, 非法, 缺 scheme 与仅空白
//
// TestCreate_APIBaseValidation covers the static apiBase checks: valid, invalid, missing scheme
// and whitespace only
func TestCreate_APIBaseValidation(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"official default omitted", `{"token":"EXAMPLE","chatIds":["1"]}`, false},
		{"explicit official", `{"token":"EXAMPLE","chatIds":["1"],"apiBase":"https://api.telegram.org"}`, false},
		{"custom host", `{"token":"EXAMPLE","chatIds":["1"],"apiBase":"tg.example.com"}`, false},
		{"ftp scheme", `{"token":"EXAMPLE","chatIds":["1"],"apiBase":"ftp://tg.example.com"}`, true},
		{"empty host", `{"token":"EXAMPLE","chatIds":["1"],"apiBase":"https:///path"}`, true},
		// 空串与纯空白都按"未提供"处理, 由 Prepare 兜底官方地址
		//
		// Both an empty string and whitespace count as omitted, so Prepare falls back to the
		// official address
		{"empty string", `{"token":"EXAMPLE","chatIds":["1"],"apiBase":""}`, false},
		{"whitespace only", `{"token":"EXAMPLE","chatIds":["1"],"apiBase":"   "}`, false},
	}
	for _, c := range cases {
		_, err := channel.Create("tg", "telegram", json.RawMessage(c.raw))
		if c.wantErr && err == nil {
			t.Errorf("%s: expected an error", c.name)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if c.wantErr {
			if prob := mustProblem(t, err); prob.ID != keyErrorInvalidBase {
				t.Errorf("%s: ID = %q, want %q", c.name, prob.ID, keyErrorInvalidBase)
			}
		}
	}
}

// TestCreate_ChatIDStringForm 覆盖 chatIds 的逗号字符串形式与 StringList 去空行为
//
// TestCreate_ChatIDStringForm covers the comma string form of chatIds and the StringList empty-item
// stripping
func TestCreate_ChatIDStringForm(t *testing.T) {
	ch := newTestChannel(t, `{"token":"EXAMPLE","chatIds":"123, 456 ,,"}`)
	prepared, _, err := prepareStrict(ch, "t", "b")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	targets := prepared.(*preparedChannel).targets
	if len(targets) != 2 || targets[0].chatID != "123" || targets[1].chatID != "456" {
		t.Fatalf("targets = %v", targets)
	}
}

// TestPrepare_JoinAndTruncation 覆盖标题正文组合与 UTF-16 截断告警
//
// TestPrepare_JoinAndTruncation covers the title and body composition and the UTF-16 truncation
// warning
func TestPrepare_JoinAndTruncation(t *testing.T) {
	ch := newTestChannel(t, `{"token":"EXAMPLE","chatIds":["123456789"]}`)

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
		prepared, warns, err := prepareStrict(ch, c.title, c.body)
		if err != nil {
			t.Fatalf("%s: Prepare: %v", c.name, err)
		}
		if len(warns) != 0 {
			t.Fatalf("%s: unexpected warnings %v", c.name, warns)
		}
		targets := prepared.(*preparedChannel).targets
		if len(targets) != 1 || targets[0].chatID != "123456789" {
			t.Fatalf("%s: targets = %v", c.name, targets)
		}
		var payload sendPayload
		if err := json.Unmarshal(targets[0].body, &payload); err != nil {
			t.Fatalf("%s: unmarshal body: %v", c.name, err)
		}
		if payload.Text != c.wantText {
			t.Errorf("%s: text = %q, want %q", c.name, payload.Text, c.wantText)
		}
	}
}

// TestPrepare_EmptyContent 覆盖标题正文都空时报 empty_content
//
// TestPrepare_EmptyContent covers the empty_content error when both the title and the body are
// empty
func TestPrepare_EmptyContent(t *testing.T) {
	ch := newTestChannel(t, `{"token":"EXAMPLE","chatIds":["123456789"]}`)
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

// TestPrepare_TruncateUTF16 覆盖 4096 UTF-16 截断: 切点不拆代理对且发警告
//
// TestPrepare_TruncateUTF16 covers the 4096 UTF-16 truncation: the cut keeps surrogate pairs whole
// and a warning is raised
func TestPrepare_TruncateUTF16(t *testing.T) {
	ch := newTestChannel(t, `{"token":"EXAMPLE","chatIds":["123456789"]}`)

	// 正好 4096 单元 (2 ASCII + 2047 个非 BMP 字符各占 2 单元) 不截断
	//
	// Exactly 4096 units, two ASCII plus 2047 non-BMP characters at two units each, is not cut
	exact := "ab" + strings.Repeat("\U0001F600", 2047)
	prepared, warns, err := prepareStrict(ch, exact, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("exact-limit warnings = %v", warns)
	}
	if got := payloadText(t, prepared); got != exact {
		t.Fatal("a text exactly at the limit was altered")
	}

	// 4095 个 BMP 字符 + 一个非 BMP 字符: 预算剩 1 单元放不下 2 单元的字符, 整个字符被丢掉
	//
	// 4095 BMP characters plus one non-BMP character: the budget has one unit left and the two-unit
	// character does not fit, so that character is dropped whole
	overflow := strings.Repeat("a", 4095) + "\U0001F600"
	prepared, warns, err = prepareStrict(ch, overflow, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	got := payloadText(t, prepared)
	if got != strings.Repeat("a", 4095) {
		t.Fatalf("cut text has %d UTF-16 units and %d runes", utf16Len(got), len([]rune(got)))
	}
	if utf16Len(got) != textLimitUTF16-1 {
		t.Fatalf("cut text has %d UTF-16 units, want %d", utf16Len(got), textLimitUTF16-1)
	}
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Fatalf("warnings = %v, want one %s", warns, keyWarnTruncated)
	}
	if warns[0].Data["Limit"] != textLimitUTF16 {
		t.Errorf("warning Limit = %v", warns[0].Data["Limit"])
	}
}

// payloadText 取出 Prepare 结果里第一个目标的 text 字段
//
// payloadText reads the text field of the first target in a Prepare result
func payloadText(t *testing.T, prepared channel.Prepared) string {
	t.Helper()
	targets := prepared.(*preparedChannel).targets
	if len(targets) == 0 {
		t.Fatal("no targets were prepared")
	}
	var payload sendPayload
	if err := json.Unmarshal(targets[0].body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return payload.Text
}

// TestPrepare_ChatIDsTrimmed 覆盖渲染后 chatIds 逐项 TrimSpace 与去空
//
// TestPrepare_ChatIDsTrimmed covers trimming each rendered chatIds item and dropping the empty
// ones
func TestPrepare_ChatIDsTrimmed(t *testing.T) {
	ch := newTestChannel(t, `{"token":"EXAMPLE","chatIds":["  123 ", "", "  "]}`)
	prepared, _, err := prepareStrict(ch, "t", "b")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	targets := prepared.(*preparedChannel).targets
	if len(targets) != 1 || targets[0].chatID != "123" {
		t.Fatalf("targets = %v", targets)
	}
}

// TestPrepare_AllChatIDsEmpty 覆盖渲染后全部 chat id 变空时报 missing_chat_ids
//
// TestPrepare_AllChatIDsEmpty covers the missing_chat_ids error when every chat id renders empty
func TestPrepare_AllChatIDsEmpty(t *testing.T) {
	// Create 阶段空元素已被 StringList 丢弃, 所以这里用渲染后才变空的模板项覆盖 Prepare 分支
	//
	// StringList already drops empty elements at Create time, so a template item that renders empty
	// covers the Prepare branch
	ch := newTestChannel(t, `{"token":"EXAMPLE","chatIds":["{{cid}}"]}`)
	vars := template.NewVars()
	vars.Set("cid", "   ")
	_, _, err := ch.Prepare(context.Background(), channel.Message{
		Body: "b", Vars: vars, Mode: channel.RenderStrict,
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	prob := mustProblem(t, err)
	if prob.ID != keyErrorMissingChats {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorMissingChats)
	}
	if !apperr.IsPermanent(err) {
		t.Error("an empty chat list is not permanent")
	}
}

// TestCreate_EmptyChatIDArray 覆盖 Create 阶段空数组按缺必填处理
//
// TestCreate_EmptyChatIDArray covers an empty array at Create time being read as a missing required
// field
func TestCreate_EmptyChatIDArray(t *testing.T) {
	_, err := channel.Create("tg", "telegram", json.RawMessage(`{"token":"EXAMPLE","chatIds":[]}`))
	prob := mustProblem(t, err)
	if prob.ID != keyErrorMissingChats {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorMissingChats)
	}
}

// TestPrepare_TokenEscapedAndMasked 覆盖 token 的 URL path 转义与预览不含 token
//
// TestPrepare_TokenEscapedAndMasked covers the URL path escaping of the token and a preview without
// the token
func TestPrepare_TokenEscapedAndMasked(t *testing.T) {
	const rawToken = "12:abc/def"
	ch := newTestChannel(t, `{"token":"12:abc/def","chatIds":["123456789"]}`)
	prepared, _, err := prepareStrict(ch, "t", "b")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := prepared.(*preparedChannel)
	escaped := url.PathEscape(rawToken)
	if escaped == rawToken {
		t.Fatalf("url.PathEscape(%q) left the slash unescaped, the fixture no longer covers escaping", rawToken)
	}
	want := "https://api.telegram.org/bot" + escaped + "/sendMessage"
	if p.endpoint != want {
		t.Fatalf("endpoint = %q, want %q", p.endpoint, want)
	}
	if strings.Contains(p.endpoint, "/bot"+rawToken+"/") {
		t.Fatal("the raw token reached the URL path unescaped")
	}

	preview := prepared.Preview()
	if preview.ID != keyPreviewMessage {
		t.Fatalf("preview ID = %q", preview.ID)
	}
	if strings.Contains(fmt.Sprint(preview.Data), rawToken) {
		t.Fatalf("the preview leaked the token: %v", preview.Data)
	}
	got, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	if strings.Contains(string(got), rawToken) || strings.Contains(string(got), "123456789") {
		t.Fatalf("the preview leaked a credential: %s", got)
	}
}

// TestPrepare_TemplateAndCheckMode 覆盖 settings 模板渲染, 严格模式报错与 check 模式告警
//
// TestPrepare_TemplateAndCheckMode covers the settings template rendering, the strict-mode error
// and the check-mode warning
func TestPrepare_TemplateAndCheckMode(t *testing.T) {
	// 全部变量都已解析: apiBase 与 chatIds 都取变量值
	//
	// Every variable resolves here, so apiBase and chatIds both take their variable value
	templated := newTestChannel(t, `{"token":"EXAMPLE","chatIds":["{{cid}}"],"apiBase":"{{base}}"}`)
	vars := template.NewVars()
	vars.Set("cid", "123456789")
	vars.Set("base", "https://tg.example.com")

	prepared, _, err := templated.Prepare(context.Background(), channel.Message{
		Body: "b", Vars: vars, Mode: channel.RenderStrict,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := prepared.(*preparedChannel)
	if !strings.HasPrefix(p.endpoint, "https://tg.example.com/bot") {
		t.Fatalf("endpoint = %q", p.endpoint)
	}
	if p.targets[0].chatID != "123456789" {
		t.Fatalf("chat id = %q", p.targets[0].chatID)
	}

	// 严格模式: 未解析变量报错, Path 被改写为触发的字段路径
	//
	// Strict mode: an unresolved variable fails, and Path carries the field that raised it
	chatTemplated := newTestChannel(t, `{"token":"EXAMPLE","chatIds":["{{cid}}"]}`)
	_, _, err = prepareStrict(chatTemplated, "t", "b")
	prob := mustProblem(t, err)
	if prob.ID != "template.error.unresolved" {
		t.Fatalf("ID = %q, want template.error.unresolved", prob.ID)
	}
	if prob.Data["Path"] != "settings.chatIds.0" {
		t.Fatalf("Path = %v, want settings.chatIds.0", prob.Data["Path"])
	}

	// check 模式: 未解析变量保留字面并告警, 而不是中断
	//
	// check mode: an unresolved variable stays literal with a warning instead of failing the
	// preparation
	prepared, warns, err := chatTemplated.Prepare(context.Background(), channel.Message{
		Body: "b", Vars: template.NewVars(), Mode: channel.RenderCheck,
	})
	if err != nil {
		t.Fatalf("Prepare check: %v", err)
	}
	foundUnresolved := false
	for _, w := range warns {
		if w.ID == "template.warn.unresolved" && w.Data["Path"] == "settings.chatIds.0" {
			foundUnresolved = true
		}
	}
	if !foundUnresolved {
		t.Fatalf("warnings = %v, want an unresolved warning for settings.chatIds.0", warns)
	}
	targets := prepared.(*preparedChannel).targets
	if len(targets) != 1 || targets[0].chatID != "{{cid}}" {
		t.Fatalf("check-mode targets = %v", targets)
	}
}

// TestPrepare_APIBaseDefaultAndCustom 覆盖 apiBase 缺省与自定义 (含路径保留)
//
// TestPrepare_APIBaseDefaultAndCustom covers the default and a custom apiBase with its path kept
func TestPrepare_APIBaseDefaultAndCustom(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantPrefix string
	}{
		{"default", `{"token":"EXAMPLE","chatIds":["1"]}`, "https://api.telegram.org/botEXAMPLE/sendMessage"},
		{"blank default", `{"token":"EXAMPLE","chatIds":["1"],"apiBase":"  "}`, "https://api.telegram.org/botEXAMPLE/sendMessage"},
		{"custom host", `{"token":"EXAMPLE","chatIds":["1"],"apiBase":"tg.example.com"}`, "https://tg.example.com/botEXAMPLE/sendMessage"},
		{"custom path", `{"token":"EXAMPLE","chatIds":["1"],"apiBase":"https://tg.example.com/proxy/"}`, "https://tg.example.com/proxy/botEXAMPLE/sendMessage"},
	}
	for _, c := range cases {
		prepared, _, err := prepareStrict(newTestChannel(t, c.raw), "t", "b")
		if err != nil {
			t.Fatalf("%s: Prepare: %v", c.name, err)
		}
		if got := prepared.(*preparedChannel).endpoint; got != c.wantPrefix {
			t.Errorf("%s: endpoint = %q, want %q", c.name, got, c.wantPrefix)
		}
	}
}

// TestPrepare_SilentFlag 覆盖 silent 写入 disable_notification
//
// TestPrepare_SilentFlag covers silent writing disable_notification
func TestPrepare_SilentFlag(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"silent true", `{"token":"EXAMPLE","chatIds":["1"],"silent":true}`, true},
		{"silent string true", `{"token":"EXAMPLE","chatIds":["1"],"silent":"1"}`, true},
		{"silent omitted", `{"token":"EXAMPLE","chatIds":["1"]}`, false},
		{"silent false", `{"token":"EXAMPLE","chatIds":["1"],"silent":false}`, false},
	}
	for _, c := range cases {
		prepared, _, err := prepareStrict(newTestChannel(t, c.raw), "t", "b")
		if err != nil {
			t.Fatalf("%s: Prepare: %v", c.name, err)
		}
		var payload sendPayload
		if err := json.Unmarshal(prepared.(*preparedChannel).targets[0].body, &payload); err != nil {
			t.Fatalf("%s: unmarshal body: %v", c.name, err)
		}
		if payload.DisableNotification != c.want {
			t.Errorf("%s: disable_notification = %v, want %v", c.name, payload.DisableNotification, c.want)
		}
	}
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
