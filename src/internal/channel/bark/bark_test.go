package bark

/*
Bark 渠道测试: Create 静态校验, Prepare 渲染与截断, Send 单设备与批量判定, Preview 脱敏,
以及 en/zh-CN catalog 的键与占位符一致性

Bark channel tests: Create-time validation, Prepare rendering and truncation, single and batch
Send verdicts, Preview redaction, and the key plus placeholder parity of the en and zh-CN catalogs
*/

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

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// newChannel 按 settings JSON 构造一个 bark 渠道, 失败即中止测试
//
// newChannel builds a bark channel from settings JSON and stops the test on failure
func newChannel(t *testing.T, raw string) channel.Channel {
	t.Helper()
	ch, err := (factory{}).Create("bark-test", json.RawMessage(raw))
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

// decodePayload 解出请求体, 供断言实际发送的字段
//
// decodePayload unmarshals the request body so a test can assert the fields actually sent
func decodePayload(t *testing.T, p channel.Prepared) map[string]any {
	t.Helper()
	prepared, ok := p.(*preparedChannel)
	if !ok {
		t.Fatalf("Prepared is %T, want *preparedChannel", p)
	}
	var out map[string]any
	if err := json.Unmarshal(prepared.body, &out); err != nil {
		t.Fatalf("decode body %s: %v", prepared.body, err)
	}
	return out
}

// TestFactory_Metadata 校验类型, 描述键与字段元数据完整
//
// TestFactory_Metadata checks the type, the description key and the completeness of the field metadata
func TestFactory_Metadata(t *testing.T) {
	if got := (factory{}).Type(); got != "bark" {
		t.Errorf("Type() = %q, want bark", got)
	}
	if got := (factory{}).Describe(); got != keyDesc {
		t.Errorf("Describe() = %q, want %q", got, keyDesc)
	}

	want := []string{
		"token", "tokens", "server", "subtitle", "group", "level", "sound",
		"icon", "image", "clickUrl", "copy", "badge", "ttl", "autoCopy",
	}
	fields := (factory{}).Fields()
	if len(fields) != len(want) {
		t.Fatalf("Fields() has %d entries, want %d", len(fields), len(want))
	}
	for i, field := range fields {
		if field.Name != want[i] {
			t.Errorf("Fields()[%d].Name = %q, want %q", i, field.Name, want[i])
		}
		if field.DescriptionKey != "channel.bark.field."+field.Name+".desc" {
			t.Errorf("Fields()[%d].DescriptionKey = %q", i, field.DescriptionKey)
		}
		if field.Type == "" {
			t.Errorf("Fields()[%d].Type is empty", i)
		}
	}
}

// TestCreate_TokenOrTokens 覆盖 token/tokens 二选一的三条分支
//
// TestCreate_TokenOrTokens covers the three branches of the token-or-tokens rule
func TestCreate_TokenOrTokens(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"token only", `{"token":"example-token"}`, false},
		{"tokens only", `{"tokens":["example-one"]}`, false},
		{"both", `{"token":"example-token","tokens":["example-one"]}`, true},
		{"neither", `{}`, true},
		{"blank token", `{"token":"   "}`, true},
		{"empty tokens", `{"tokens":[]}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch, err := factory{}.Create("bark-test", json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Create(%s) succeeded, want an error", tc.raw)
				}
				if !apperr.IsPermanent(err) {
					t.Errorf("Create(%s) error must be permanent, got %v", tc.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create(%s): %v", tc.raw, err)
			}
			if ch.ID() != "bark-test" || ch.Type() != "bark" {
				t.Errorf("ID()/Type() = %q/%q", ch.ID(), ch.Type())
			}
		})
	}
}

// TestCreate_Server 覆盖 server 的缺省, 补 scheme 与非法取值
//
// TestCreate_Server covers the server default, scheme completion and invalid values
func TestCreate_Server(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"default", `{"token":"example-token"}`, false},
		{"host only", `{"token":"example-token","server":"bark.example.com"}`, false},
		{"loopback http", `{"token":"example-token","server":"http://127.0.0.1:8080"}`, false},
		{"template server", `{"token":"example-token","server":"{{base}}"}`, false},
		{"blank server", `{"token":"example-token","server":"   "}`, true},
		{"ftp scheme", `{"token":"example-token","server":"ftp://example.com"}`, true},
		{"no host", `{"token":"example-token","server":"https://"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := factory{}.Create("bark-test", json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil || !apperr.IsPermanent(err) {
					t.Fatalf("Create(%s) error = %v, want a permanent error", tc.raw, err)
				}
				var prob *apperr.Problem
				if !asProblem(err, &prob) || prob.ID != keyErrorInvalidServer {
					t.Errorf("Create(%s) problem = %#v, want %s", tc.raw, prob, keyErrorInvalidServer)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create(%s): %v", tc.raw, err)
			}
		})
	}
}

// TestCreate_ScalarTypes 覆盖 badge/ttl/autoCopy 的取值与类型错误
//
// TestCreate_ScalarTypes covers the accepted values and type errors of badge, ttl and autoCopy
func TestCreate_ScalarTypes(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		key     string
		wantErr bool
	}{
		{"badge number", `{"token":"t","badge":1}`, "", false},
		{"badge zero", `{"token":"t","badge":0}`, "", false},
		{"badge string", `{"token":"t","badge":"3"}`, "", false},
		{"badge negative", `{"token":"t","badge":-1}`, keyErrorInvalidBadge, true},
		{"badge float", `{"token":"t","badge":1.5}`, keyErrorInvalidBadge, true},
		{"badge text", `{"token":"t","badge":"many"}`, keyErrorInvalidBadge, true},
		{"badge bool", `{"token":"t","badge":true}`, keyErrorInvalidBadge, true},
		{"ttl number", `{"token":"t","ttl":60}`, "", false},
		{"ttl zero", `{"token":"t","ttl":0}`, "", false},
		{"ttl negative", `{"token":"t","ttl":-5}`, keyErrorInvalidTTL, true},
		{"ttl text", `{"token":"t","ttl":"soon"}`, keyErrorInvalidTTL, true},
		{"autoCopy bool", `{"token":"t","autoCopy":true}`, "", false},
		{"autoCopy string", `{"token":"t","autoCopy":"1"}`, "", false},
		{"autoCopy number", `{"token":"t","autoCopy":1}`, "", false},
		{"autoCopy text", `{"token":"t","autoCopy":"yes"}`, keyErrorInvalidAutoCopy, true},
		{"autoCopy out of range", `{"token":"t","autoCopy":2}`, keyErrorInvalidAutoCopy, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := factory{}.Create("bark-test", json.RawMessage(tc.raw))
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Create(%s): %v", tc.raw, err)
				}
				return
			}
			if err == nil || !apperr.IsPermanent(err) {
				t.Fatalf("Create(%s) error = %v, want a permanent error", tc.raw, err)
			}
			var prob *apperr.Problem
			if !asProblem(err, &prob) || prob.ID != tc.key {
				t.Fatalf("Create(%s) problem = %#v, want %s", tc.raw, prob, tc.key)
			}
			if prob.Data["Value"] == nil {
				t.Errorf("Create(%s) problem Data has no Value", tc.raw)
			}
		})
	}
}

// TestCreate_StrictDecode 覆盖未知字段与顶层非对象
//
// TestCreate_StrictDecode covers an unknown field and a non-object top level
func TestCreate_StrictDecode(t *testing.T) {
	for _, raw := range []string{`{"token":"t","unknown":1}`, `[]`, `"text"`} {
		if _, err := (factory{}).Create("bark-test", json.RawMessage(raw)); err == nil {
			t.Errorf("Create(%s) succeeded, want a strict decoding error", raw)
		}
	}
}

// TestPrepare_Content 覆盖标题正文至少一个非空的规则
//
// TestPrepare_Content covers the rule that at least one of the title and the body is required
func TestPrepare_Content(t *testing.T) {
	ch := newChannel(t, `{"token":"example-token"}`)
	if _, _, err := ch.Prepare(context.Background(), channel.Message{Vars: template.NewVars()}); err == nil {
		t.Fatal("Prepare with no content succeeded, want an error")
	} else if !apperr.IsPermanent(err) {
		t.Errorf("empty content error must be permanent, got %v", err)
	} else {
		var prob *apperr.Problem
		if !asProblem(err, &prob) || prob.ID != keyErrorEmptyContent {
			t.Errorf("problem = %#v, want %s", prob, keyErrorEmptyContent)
		}
	}

	// 只有标题, 只有正文, 两者都有: 都应当通过, 且不把标题拼进正文
	// Title only, body only and both: all pass, and the title is never joined into the body
	for _, tc := range []struct{ title, body string }{{"example-title", ""}, {"", "example-body"}, {"example-title", "example-body"}} {
		prepared, _ := prepare(t, ch, tc.title, tc.body)
		payload := decodePayload(t, prepared)
		if got, _ := payload["title"].(string); got != tc.title {
			t.Errorf("payload title = %q, want %q", got, tc.title)
		}
		if got, _ := payload["body"].(string); got != tc.body {
			t.Errorf("payload body = %q, want %q", got, tc.body)
		}
	}
}

// TestPrepare_Endpoint 覆盖单设备与批量端点, 以及 token 中的斜杠保留
//
// TestPrepare_Endpoint covers the single and batch endpoints and the slash kept inside a token
func TestPrepare_Endpoint(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"default server", `{"token":"example-token"}`, "https://api.day.app/example-token"},
		{"custom server", `{"token":"example-token","server":"https://bark.example.com"}`, "https://bark.example.com/example-token"},
		{"trailing slash", `{"token":"example-token","server":"https://bark.example.com/"}`, "https://bark.example.com/example-token"},
		{"group key", `{"token":"example-group/device"}`, "https://api.day.app/example-group/device"},
		{"batch", `{"tokens":["example-one","example-two"]}`, "https://api.day.app/push"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newChannel(t, tc.raw)
			prepared, _ := prepare(t, ch, "example-title", "")
			got := prepared.(*preparedChannel).endpoint
			if got != tc.want {
				t.Errorf("endpoint = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPrepare_OptionalFields 覆盖可选字段的渲染与请求体字段名
//
// TestPrepare_OptionalFields covers the rendering of the optional fields and their payload names
func TestPrepare_OptionalFields(t *testing.T) {
	ch := newChannel(t, `{
		"token":"example-token",
		"subtitle":"example-subtitle",
		"group":"example-group",
		"level":"active",
		"sound":"example-sound",
		"icon":"https://example.com/icon.png",
		"image":"https://example.com/image.png",
		"clickUrl":"https://example.com/build/1",
		"copy":"example-copy",
		"badge":7,
		"ttl":60,
		"autoCopy":true
	}`)
	prepared, warns := prepare(t, ch, "example-title", "example-body")
	if len(warns) != 0 {
		t.Fatalf("warnings = %v, want none", warns)
	}
	payload := decodePayload(t, prepared)

	wantStrings := map[string]string{
		"subtitle": "example-subtitle",
		"group":    "example-group",
		"level":    "active",
		"sound":    "example-sound",
		"icon":     "https://example.com/icon.png",
		"image":    "https://example.com/image.png",
		"url":      "https://example.com/build/1",
		"copy":     "example-copy",
	}
	for key, want := range wantStrings {
		if got, _ := payload[key].(string); got != want {
			t.Errorf("payload[%q] = %q, want %q", key, got, want)
		}
	}
	if got, _ := payload["badge"].(float64); got != 7 {
		t.Errorf("payload badge = %v, want 7", payload["badge"])
	}
	if got, _ := payload["ttl"].(float64); got != 60 {
		t.Errorf("payload ttl = %v, want 60", payload["ttl"])
	}
	if got, _ := payload["autoCopy"].(float64); got != 1 {
		t.Errorf("payload autoCopy = %v, want 1", payload["autoCopy"])
	}
}

// TestPrepare_OptionalEmpty 覆盖渲染后为空的字段不被发送, 以及 0/false 仍然发送
//
// TestPrepare_OptionalEmpty covers the fields that render empty and are dropped, while 0 and false
// are still sent
func TestPrepare_OptionalEmpty(t *testing.T) {
	vars := template.NewVars()
	vars.Set("blank", "")
	ch := newChannel(t, `{
		"token":"example-token",
		"subtitle":"{{blank}}",
		"group":"   ",
		"badge":0,
		"ttl":0,
		"autoCopy":false
	}`)
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  vars,
		Mode:  channel.RenderStrict,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	payload := decodePayload(t, prepared)
	for _, key := range []string{"subtitle", "group"} {
		if _, present := payload[key]; present {
			t.Errorf("payload[%q] is present, want it dropped", key)
		}
	}
	if got, _ := payload["badge"].(float64); got != 0 {
		t.Errorf("payload badge = %v, want 0 to be sent", payload["badge"])
	}
	if got, _ := payload["ttl"].(float64); got != 0 {
		t.Errorf("payload ttl = %v, want 0 to be sent", payload["ttl"])
	}
	if got, _ := payload["autoCopy"].(float64); got != 0 {
		t.Errorf("payload autoCopy = %v, want 0 to be sent", payload["autoCopy"])
	}
}

// TestPrepare_TokensTrimAndDrop 覆盖 tokens 的逐项渲染, TrimSpace 与去空
//
// TestPrepare_TokensTrimAndDrop covers the per-item rendering, TrimSpace and empty dropping of tokens
func TestPrepare_TokensTrimAndDrop(t *testing.T) {
	vars := template.NewVars()
	vars.Set("blank", "")
	ch := newChannel(t, `{"tokens":["  example-one  ","{{blank}}","example-two"]}`)
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  vars,
		Mode:  channel.RenderStrict,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	keys, _ := decodePayload(t, prepared)["device_keys"].([]any)
	if len(keys) != 2 || keys[0] != "example-one" || keys[1] != "example-two" {
		t.Errorf("device_keys = %v, want [example-one example-two]", keys)
	}

	// 全部渲染为空时没有可发送的设备: 按配置错误处理
	// When every item renders empty there is no device left, which counts as a configuration error
	empty := newChannel(t, `{"tokens":["{{blank}}"]}`)
	if _, _, err := empty.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  vars,
		Mode:  channel.RenderStrict,
	}); err == nil || !apperr.IsPermanent(err) {
		t.Fatalf("Prepare with only blank tokens error = %v, want a permanent error", err)
	}
}

// TestPrepare_URLFields 覆盖 icon/image/clickUrl 的 URL 校验
//
// TestPrepare_URLFields covers the URL validation of icon, image and clickUrl
func TestPrepare_URLFields(t *testing.T) {
	for _, field := range []string{"icon", "image", "clickUrl"} {
		t.Run(field+" invalid", func(t *testing.T) {
			ch := newChannel(t, `{"token":"example-token","`+field+`":"ftp://example.com/x"}`)
			_, _, err := ch.Prepare(context.Background(), channel.Message{Title: "example-title", Vars: template.NewVars()})
			if err == nil || !apperr.IsPermanent(err) {
				t.Fatalf("Prepare error = %v, want a permanent error", err)
			}
			var prob *apperr.Problem
			if !asProblem(err, &prob) || prob.ID != keyErrorInvalidURL {
				t.Fatalf("problem = %#v, want %s", prob, keyErrorInvalidURL)
			}
			if got, _ := prob.Data["Field"].(string); got != "settings."+field {
				t.Errorf("problem Field = %q, want settings.%s", got, field)
			}
		})
	}

	// 缺 scheme 的地址按回环/非回环规则补全, 因此仍是合法取值
	// A scheme-less address is completed by the loopback rule, so it stays valid
	ch := newChannel(t, `{"token":"example-token","icon":"example.com/icon.png"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	if got, _ := decodePayload(t, prepared)["icon"].(string); got != "https://example.com/icon.png" {
		t.Errorf("payload icon = %q, want the normalised https address", got)
	}
}

// TestPrepare_NoContentTruncation 确认渠道不对标题正文做截断
// 按 bark spec 第 7 节, 长度只由模板层 64KB 上限约束, 因此超长内容原样进入请求体且不产生警告
//
// TestPrepare_NoContentTruncation confirms the channel truncates neither the title nor the body
// Under section 7 of the bark spec the template-layer 64KB cap is the only bound, so oversized
// content reaches the request body as it stands and raises no warning
func TestPrepare_NoContentTruncation(t *testing.T) {
	ch := newChannel(t, `{"token":"example-token"}`)
	long := strings.Repeat("a", 64*1024+16)
	prepared, warns := prepare(t, ch, long, long)
	if len(warns) != 0 {
		t.Fatalf("warnings = %v, want none", warns)
	}
	payload := decodePayload(t, prepared)
	if got, _ := payload["title"].(string); got != long {
		t.Errorf("title length = %d, want the content untouched at %d", len(got), len(long))
	}
	if got, _ := payload["body"].(string); got != long {
		t.Errorf("body length = %d, want the content untouched at %d", len(got), len(long))
	}
}

// TestPrepare_UnresolvedTemplate 覆盖严格模式报错与宽松模式告警后继续
//
// TestPrepare_UnresolvedTemplate covers the strict error and the check-mode warning that continues
func TestPrepare_UnresolvedTemplate(t *testing.T) {
	ch := newChannel(t, `{"token":"example-token","group":"{{missing}}"}`)
	if _, _, err := ch.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  template.NewVars(),
		Mode:  channel.RenderStrict,
	}); err == nil {
		t.Fatal("strict mode accepted an unresolved variable")
	}

	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{
		Title: "example-title",
		Vars:  template.NewVars(),
		Mode:  channel.RenderCheck,
	})
	if err != nil {
		t.Fatalf("check mode: %v", err)
	}
	if len(warns) == 0 {
		t.Error("check mode returned no warning for the unresolved variable")
	}
	if got, _ := decodePayload(t, prepared)["group"].(string); got != "{{missing}}" {
		t.Errorf("payload group = %q, want the literal kept", got)
	}
}

// TestPreview_NoSecrets 覆盖单设备与批量预览, 并确认预览不含 token 与 device key
//
// TestPreview_NoSecrets covers the single and batch previews and confirms that neither a token nor a
// device key appears inside them
func TestPreview_NoSecrets(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantID  string
		targets int
		secrets []string
	}{
		{"single", `{"token":"example-token"}`, keyPreviewSingle, 1, []string{"example-token"}},
		{"batch", `{"tokens":["example-one","example-two"]}`, keyPreviewBatch, 2, []string{"example-one", "example-two"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newChannel(t, tc.raw)
			prepared, _ := prepare(t, ch, "example-title", "")
			preview := prepared.Preview()
			if preview.ID != tc.wantID {
				t.Errorf("Preview().ID = %q, want %q", preview.ID, tc.wantID)
			}
			if got, _ := preview.Data["Targets"].(int); got != tc.targets {
				t.Errorf("Preview().Data[Targets] = %v, want %d", preview.Data["Targets"], tc.targets)
			}
			rendered, err := json.Marshal(preview)
			if err != nil {
				t.Fatalf("marshal preview: %v", err)
			}
			// 预览只有 Problem 本身参与渲染: 端点虽然带 token, 但从不放进 Data, 因此单独核对 Data
			//
			// Only the Problem itself takes part in the preview rendering: the endpoint does hold
			// the token, but it never reaches Data, which is what this check covers
			exposed := string(rendered)
			for _, secret := range tc.secrets {
				if strings.Contains(exposed, secret) {
					t.Errorf("preview exposes %q: %s", secret, exposed)
				}
			}
			// Prepare 的警告同样不得带出密钥
			//
			// The warnings from Prepare must not carry a credential either
			_, warns := prepare(t, ch, "example-title", "")
			for i, warn := range warns {
				text, _ := json.Marshal(warn)
				for _, secret := range tc.secrets {
					if strings.Contains(string(text), secret) {
						t.Errorf("warning %d exposes %q: %s", i, secret, text)
					}
				}
			}
		})
	}
}

// TestSend_Single 覆盖单设备发送的 200, 业务码与坏响应分支
//
// TestSend_Single covers the 200, business-code and bad-response branches of a single-device send
func TestSend_Single(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		status    int
		wantKey   string
		permanent bool
		wantErr   bool
	}{
		{"ok", `{"code":200}`, http.StatusOK, "", false, false},
		{"api 400", `{"code":400,"message":"bad key"}`, http.StatusOK, keyErrorAPICode, true, true},
		{"api 500", `{"code":500,"message":"busy"}`, http.StatusOK, keyErrorAPICode, false, true},
		{"bad json", `not json`, http.StatusOK, "common.error.bad_json", false, true},
		{"empty body", ``, http.StatusOK, "common.error.bad_response", false, true},
		{"http 404", `{"code":200}`, http.StatusNotFound, "", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
					t.Errorf("Content-Type = %q, want JSON", got)
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			ch := newChannel(t, `{"token":"example-token","server":"`+srv.URL+`"}`)
			prepared, _ := prepare(t, ch, "example-title", "")
			err := prepared.Send(context.Background(), srv.Client())
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Send: %v", err)
				}
				// 成功之后重复发送不再打网络
				// After a success a repeated send reaches no network
				if err := prepared.Send(context.Background(), srv.Client()); err != nil {
					t.Fatalf("repeated Send: %v", err)
				}
				if calls != 1 {
					t.Errorf("calls = %d, want 1", calls)
				}
				return
			}
			if err == nil {
				t.Fatal("Send succeeded, want an error")
			}
			if apperr.IsPermanent(err) != tc.permanent {
				t.Errorf("IsPermanent(%v) = %v, want %v", err, apperr.IsPermanent(err), tc.permanent)
			}
			if tc.wantKey != "" {
				var prob *apperr.Problem
				if !asProblem(err, &prob) || prob.ID != tc.wantKey {
					t.Errorf("problem = %#v, want %s", prob, tc.wantKey)
				}
			}
		})
	}
}

// TestSend_SingleNoRetryAfterSuccess 覆盖成功后重复 Send 不产生第二次请求
//
// TestSend_SingleNoRetryAfterSuccess covers a repeated Send after a success making no second request
func TestSend_SingleNoRetryAfterSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"code":200}`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"token":"example-token","server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	for i := 0; i < 3; i++ {
		if err := prepared.Send(context.Background(), srv.Client()); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

// TestSend_BatchAllSuccess 覆盖批量全部成功
//
// TestSend_BatchAllSuccess covers a batch where every device succeeds
func TestSend_BatchAllSuccess(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"code":200,"data":[{"device_key":"example-one","code":200},{"device_key":"example-two","code":200}]}`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"tokens":["example-one","example-two"],"server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "example-body")
	if err := prepared.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	keys, _ := decodePayload(t, prepared)["device_keys"].([]any)
	if len(keys) != 2 {
		t.Errorf("device_keys = %v, want both devices", keys)
	}
	if !strings.Contains(string(gotBody), "example-one") {
		t.Errorf("request body = %s, want the device keys", gotBody)
	}
	if warns := prepared.(*preparedChannel).Warnings(); len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

// TestSend_BatchPartialFailure 覆盖部分成功: 只重试失败设备, 之后全部成功
//
// TestSend_BatchPartialFailure covers a partial failure: only the failing device is retried, and the
// next round succeeds
func TestSend_BatchPartialFailure(t *testing.T) {
	var attempts [][]string
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			DeviceKeys []string `json:"device_keys"`
		}
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &payload)
		attempts = append(attempts, payload.DeviceKeys)
		if fail {
			io.WriteString(w, `{"code":200,"data":[{"device_key":"example-one","code":200},{"device_key":"example-two","code":400,"message":"bad device"}]}`)
			return
		}
		io.WriteString(w, `{"code":200,"data":[{"device_key":"example-two","code":200}]}`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"tokens":["example-one","example-two"],"server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	err := prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("Send succeeded, want a batch failure")
	}
	// 失败设备只有永久码 400, 因而整体是永久错误
	// The only failing device carries the permanent code 400, so the whole error is permanent
	if !apperr.IsPermanent(err) {
		t.Errorf("IsPermanent(%v) = false, want true", err)
	}
	var prob *apperr.Problem
	if !asProblem(err, &prob) || prob.ID != keyErrorBatchFailed {
		t.Fatalf("problem = %#v, want %s", prob, keyErrorBatchFailed)
	}
	if got, _ := prob.Data["Failed"].(int); got != 1 {
		t.Errorf("Failed = %v, want 1", prob.Data["Failed"])
	}
	if got, _ := prob.Data["Total"].(int); got != 2 {
		t.Errorf("Total = %v, want 2", prob.Data["Total"])
	}
	if strings.Contains(prob.Error(), "example-two") {
		t.Errorf("error text exposes a device key: %v", prob.Error())
	}

	fail = false
	if err := prepared.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("retry Send: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %v, want two rounds", attempts)
	}
	if len(attempts[1]) != 1 || attempts[1][0] != "example-two" {
		t.Errorf("retry targets = %v, want only the failing device", attempts[1])
	}
}

// TestSend_BatchTransientFailure 覆盖瞬时业务码保持可重试
//
// TestSend_BatchTransientFailure covers a transient business code staying retryable
func TestSend_BatchTransientFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":200,"data":[{"device_key":"example-one","code":429},{"device_key":"example-two","code":200}]}`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"tokens":["example-one","example-two"],"server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	err := prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("Send succeeded, want a failure")
	}
	if apperr.IsPermanent(err) {
		t.Errorf("IsPermanent(%v) = true, want false for code 429", err)
	}
}

// TestSend_BatchTopLevelFailure 覆盖顶层业务码失败
//
// TestSend_BatchTopLevelFailure covers a failing top-level business code
func TestSend_BatchTopLevelFailure(t *testing.T) {
	cases := []struct {
		body      string
		permanent bool
	}{
		{`{"code":400,"message":"bad key"}`, true},
		{`{"code":503,"message":"unavailable"}`, false},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, tc.body)
		}))
		ch := newChannel(t, `{"tokens":["example-one"],"server":"`+srv.URL+`"}`)
		prepared, _ := prepare(t, ch, "example-title", "")
		err := prepared.Send(context.Background(), srv.Client())
		srv.Close()
		if err == nil {
			t.Fatalf("Send(%s) succeeded, want an error", tc.body)
		}
		if apperr.IsPermanent(err) != tc.permanent {
			t.Errorf("Send(%s) IsPermanent = %v, want %v", tc.body, apperr.IsPermanent(err), tc.permanent)
		}
		var prob *apperr.Problem
		if !asProblem(err, &prob) || prob.ID != keyErrorAPICode {
			t.Errorf("problem = %#v, want %s", prob, keyErrorAPICode)
		}
	}
}

// TestSend_BatchBareArray 覆盖部分部署返回裸数组的形态
//
// TestSend_BatchBareArray covers the bare-array shape some deployments answer with
func TestSend_BatchBareArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"device_key":"example-one","code":200},{"device_key":"example-two","code":400}]`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"tokens":["example-one","example-two"],"server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	err := prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("Send succeeded, want the failing device reported")
	}
	var prob *apperr.Problem
	if !asProblem(err, &prob) || prob.ID != keyErrorBatchFailed {
		t.Fatalf("problem = %#v, want %s", prob, keyErrorBatchFailed)
	}
	if !apperr.IsPermanent(err) {
		t.Errorf("IsPermanent(%v) = false, want true", err)
	}
	if got, _ := prob.Data["Failed"].(int); got != 1 {
		t.Errorf("Failed = %v, want 1", prob.Data["Failed"])
	}
}

// TestSend_BatchWithoutData 覆盖响应缺 data 时按成功处理并告警
//
// TestSend_BatchWithoutData covers a missing data field counting as success with a warning
func TestSend_BatchWithoutData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":200,"message":"success"}`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"tokens":["example-one","example-two"],"server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	if err := prepared.Send(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	warns := prepared.(*preparedChannel).Warnings()
	if len(warns) != 1 || warns[0].ID != keyWarnBatchUnverified {
		t.Fatalf("warnings = %v, want one %s", warns, keyWarnBatchUnverified)
	}
}

// TestSend_BatchWithoutDeviceKey 覆盖响应缺 device_key: 无法收窄, 下一轮全量重发并告警
//
// TestSend_BatchWithoutDeviceKey covers a response without device_key: the range cannot be narrowed,
// the next round resends to every device and a warning is recorded
func TestSend_BatchWithoutDeviceKey(t *testing.T) {
	var attempts [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			DeviceKeys []string `json:"device_keys"`
		}
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &payload)
		attempts = append(attempts, payload.DeviceKeys)
		io.WriteString(w, `{"code":200,"data":[{"code":400,"message":"bad device"}]}`)
	}))
	defer srv.Close()

	ch := newChannel(t, `{"tokens":["example-one","example-two"],"server":"`+srv.URL+`"}`)
	prepared, _ := prepare(t, ch, "example-title", "")
	err := prepared.Send(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("Send succeeded, want a failure")
	}
	// 结果映射不回设备, 无法确定哪些设备失败, 因此整体保持可重试
	// The outcome maps back to no device, so which devices failed is unknown and the round stays retryable
	if apperr.IsPermanent(err) {
		t.Errorf("IsPermanent(%v) = true, want false without a device_key", err)
	}
	warns := prepared.(*preparedChannel).Warnings()
	if len(warns) != 1 || warns[0].ID != keyWarnNoDeviceKey {
		t.Fatalf("warnings = %v, want one %s", warns, keyWarnNoDeviceKey)
	}
	if got, _ := warns[0].Data["Count"].(int); got != 2 {
		t.Errorf("warning Count = %v, want 2", warns[0].Data["Count"])
	}

	if err := prepared.Send(context.Background(), srv.Client()); err == nil {
		t.Fatal("retry Send succeeded, want a failure")
	}
	if len(attempts) != 2 || len(attempts[1]) != 2 {
		t.Fatalf("attempts = %v, want the retry to reach both devices", attempts)
	}
}

// TestSend_BatchBadResponses 覆盖批量响应为空与不可解析
//
// TestSend_BatchBadResponses covers an empty and an unparseable batch response
func TestSend_BatchBadResponses(t *testing.T) {
	cases := []struct {
		body   string
		wantID string
	}{
		{``, "common.error.bad_response"},
		{`not json`, "common.error.bad_json"},
		{`{"message":"no code"}`, "common.error.bad_response"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, tc.body)
		}))
		ch := newChannel(t, `{"tokens":["example-one"],"server":"`+srv.URL+`"}`)
		prepared, _ := prepare(t, ch, "example-title", "")
		err := prepared.Send(context.Background(), srv.Client())
		srv.Close()
		if err == nil {
			t.Fatalf("Send(%q) succeeded, want an error", tc.body)
		}
		var prob *apperr.Problem
		if !asProblem(err, &prob) || prob.ID != tc.wantID {
			t.Errorf("Send(%q) problem = %#v, want %s", tc.body, prob, tc.wantID)
		}
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
		if strings.Join(enPlaceholders(enText), ",") != strings.Join(enPlaceholders(zhText), ",") {
			t.Errorf("%s placeholders differ: en %v, zh-CN %v", key, enPlaceholders(enText), enPlaceholders(zhText))
		}
		for _, text := range []string{enText, zhText} {
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

	// spec 第 9 节要求的键必须全部存在
	// Every key section 9 of the spec requires has to be present
	required := []string{
		keyDesc,
		keyFieldToken, keyFieldTokens, keyFieldServer, keyFieldSubtitle, keyFieldGroup,
		keyFieldLevel, keyFieldSound, keyFieldIcon, keyFieldImage, keyFieldClickURL,
		keyFieldCopy, keyFieldBadge, keyFieldTTL, keyFieldAutoCopy,
		keyErrorTokenOrTokens, keyErrorEmptyContent, keyErrorInvalidServer,
		keyErrorInvalidBadge, keyErrorInvalidTTL, keyErrorInvalidAutoCopy,
		keyErrorInvalidURL, keyErrorAPICode, keyErrorBatchFailed,
		keyWarnBatchUnverified, keyWarnNoDeviceKey, keyWarnTruncated,
		keyPreviewSingle, keyPreviewBatch,
	}
	for _, key := range required {
		if _, ok := en[key]; !ok {
			t.Errorf("en catalog is missing the required key %s", key)
		}
	}
}

// readCatalog 读取一份 channel_bark.json 并解成键值表
// 测试从包目录出发, 因此路径相对仓库的 src/internal 层
//
// readCatalog reads one channel_bark.json into a key-value table
// The test starts in the package directory, so the path steps up to the src/internal level
func readCatalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "i18n", "catalog", lang, "channel_bark.json")
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

// placeholderPattern 匹配文案里的 {{.Name}} 占位符
// placeholderPattern matches a {{.Name}} placeholder inside copy
var placeholderPattern = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_]+)\s*\}\}`)

// enPlaceholders 返回一份文案里的占位符名, 顺序稳定
//
// enPlaceholders returns the placeholder names of one message in a stable order
func enPlaceholders(text string) []string {
	matches := placeholderPattern.FindAllStringSubmatch(text, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	sort.Strings(names)
	return names
}

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
	ch, err := (factory{}).Create("example", json.RawMessage(`{"token":"example-token"}`))
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
