package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildInline_WebhookDefault 覆盖 --webhook 的默认类型, 默认 POST 与默认正文
//
// TestBuildInline_WebhookDefault covers --webhook's default type, method and body
func TestBuildInline_WebhookDefault(t *testing.T) {
	channel, err := BuildInline(InlineOptions{Webhook: "https://example.com/notify", LookupEnv: lookupFrom(nil)})
	if err != nil {
		t.Fatalf("BuildInline: %v", err)
	}
	if channel.ID != "inline" || channel.Type != "webhook" {
		t.Fatalf("channel = %+v, want id inline and type webhook", channel)
	}
	if !channel.IsEnabled() {
		t.Fatal("inline channel is disabled")
	}
	settings := settingsOf(t, channel)
	if settings["url"] != "https://example.com/notify" {
		t.Fatalf("url = %#v", settings["url"])
	}
	if settings["method"] != "POST" {
		t.Fatalf("method = %#v, want POST", settings["method"])
	}
	body := decodeBody(t, settings)
	if body["format"] != "text" || body["text"] != "{{title}}\n\n{{body}}" {
		t.Fatalf("body = %#v", body)
	}
}

// TestBuildInline_Payload 覆盖 --payload 与 --webhook 组合出的 JSON 正文
//
// TestBuildInline_Payload covers the JSON body produced by --payload together with --webhook
func TestBuildInline_Payload(t *testing.T) {
	channel, err := BuildInline(InlineOptions{
		Webhook:   "https://example.com/notify",
		Payload:   `{"text":"hello","count":1}`,
		LookupEnv: lookupFrom(nil),
	})
	if err != nil {
		t.Fatalf("BuildInline: %v", err)
	}
	settings := settingsOf(t, channel)
	body := decodeBody(t, settings)
	if body["format"] != "json" {
		t.Fatalf("format = %#v, want json", body["format"])
	}
	payload, ok := body["json"].(map[string]any)
	if !ok {
		t.Fatalf("json = %#v, want an object", body["json"])
	}
	if payload["text"] != "hello" || payload["count"] != float64(1) {
		t.Fatalf("payload = %#v", payload)
	}
	if _, ok := settings["method"]; ok {
		t.Fatalf("settings = %#v, want no default method when --payload is used", settings)
	}
}

// TestBuildInline_Sets 覆盖一级键, 点路径与数组值的 --set
//
// TestBuildInline_Sets covers --set at a top-level key, through a dotted path and with a JSON
// array value
func TestBuildInline_Sets(t *testing.T) {
	channel, err := BuildInline(InlineOptions{
		Type:      "example_alpha",
		Sets:      []string{"url=https://example.com/notify", "method=PUT", "headers.X-Token=abc", "body.format=text", "body.text=hello", "tags=a,b"},
		LookupEnv: lookupFrom(nil),
	})
	if err != nil {
		t.Fatalf("BuildInline: %v", err)
	}
	if channel.Type != "example_alpha" {
		t.Fatalf("type = %q", channel.Type)
	}
	settings := settingsOf(t, channel)
	if settings["url"] != "https://example.com/notify" || settings["method"] != "PUT" || settings["tags"] != "a,b" {
		t.Fatalf("settings = %#v", settings)
	}
	headers := settings["headers"].(map[string]any)
	if headers["X-Token"] != "abc" {
		t.Fatalf("headers = %#v", headers)
	}
	body := decodeBody(t, settings)
	if body["format"] != "text" || body["text"] != "hello" {
		t.Fatalf("body = %#v", body)
	}
}

// TestBuildInline_SetsDeep 覆盖多级点路径与中间对象自动创建
//
// TestBuildInline_SetsDeep covers a multi-level dotted path and the automatic creation of
// intermediate objects
func TestBuildInline_SetsDeep(t *testing.T) {
	channel, err := BuildInline(InlineOptions{
		Type:      "example_alpha",
		Sets:      []string{"body.json.title={{title}}", "body.json.nested.value=example"},
		LookupEnv: lookupFrom(nil),
	})
	if err != nil {
		t.Fatalf("BuildInline: %v", err)
	}
	settings := settingsOf(t, channel)
	body := decodeBody(t, settings)
	payload := body["json"].(map[string]any)
	if payload["title"] != "{{title}}" {
		t.Fatalf("payload = %#v, want the template text kept", payload)
	}
	if payload["nested"].(map[string]any)["value"] != "example" {
		t.Fatalf("payload = %#v", payload)
	}
}

// TestBuildInline_SetFirstEquals 覆盖 KEY=VALUE 按第一个等号切分
//
// TestBuildInline_SetFirstEquals covers KEY=VALUE splitting at the first equals sign
func TestBuildInline_SetFirstEquals(t *testing.T) {
	channel, err := BuildInline(InlineOptions{
		Type:      "example_alpha",
		Sets:      []string{"body.text=a=b=c"},
		LookupEnv: lookupFrom(nil),
	})
	if err != nil {
		t.Fatalf("BuildInline: %v", err)
	}
	settings := settingsOf(t, channel)
	if decodeBody(t, settings)["text"] != "a=b=c" {
		t.Fatalf("text = %#v", decodeBody(t, settings)["text"])
	}
}

// TestBuildInline_Env 覆盖 --set 与 --payload 字符串里的 ${VAR} 展开
//
// TestBuildInline_Env covers ${VAR} expansion inside a --set value and a --payload string
func TestBuildInline_Env(t *testing.T) {
	lookup := lookupFrom(map[string]string{"TOKEN": "example-token", "HOST": "example.com"})
	channel, err := BuildInline(InlineOptions{
		Webhook:   "https://${HOST}/notify",
		Sets:      []string{"headers.X-Token=${TOKEN}", "body.text=${TOKEN}"},
		LookupEnv: lookup,
	})
	if err != nil {
		t.Fatalf("BuildInline: %v", err)
	}
	settings := settingsOf(t, channel)
	if settings["url"] != "https://example.com/notify" {
		t.Fatalf("url = %#v", settings["url"])
	}
	if settings["headers"].(map[string]any)["X-Token"] != "example-token" {
		t.Fatalf("headers = %#v", settings["headers"])
	}
	if decodeBody(t, settings)["text"] != "example-token" {
		t.Fatalf("body = %#v", decodeBody(t, settings))
	}

	payload, err := BuildInline(InlineOptions{
		Webhook:   "https://example.com/notify",
		Payload:   `{"token":"${TOKEN}"}`,
		LookupEnv: lookup,
	})
	if err != nil {
		t.Fatalf("BuildInline payload: %v", err)
	}
	decoded := decodeBody(t, settingsOf(t, payload))["json"].(map[string]any)
	if decoded["token"] != "example-token" {
		t.Fatalf("payload = %#v", decoded)
	}
}

// TestBuildInline_Conflicts 覆盖重复键, webhook+url, payload+body 与 payload 无 URL 四种冲突
//
// TestBuildInline_Conflicts covers a duplicate key, webhook plus url, payload plus body and
// payload without a URL
func TestBuildInline_Conflicts(t *testing.T) {
	cases := []struct {
		name string
		opts InlineOptions
		key  string
	}{
		{
			name: "duplicate set",
			opts: InlineOptions{Type: "example_alpha", Sets: []string{"method=POST", "method=PUT"}},
			key:  "config.inline.set_duplicate",
		},
		{
			name: "webhook and url set",
			opts: InlineOptions{Webhook: "https://example.com/notify", Sets: []string{"url=https://example.com/other"}},
			key:  "config.inline.set_conflict",
		},
		{
			name: "payload and body set",
			opts: InlineOptions{Webhook: "https://example.com/notify", Payload: `{"text":"hello"}`, Sets: []string{"body.text=hello"}},
			key:  "config.inline.set_conflict",
		},
		{
			name: "payload without url",
			opts: InlineOptions{Type: "example_alpha", Payload: `{"text":"hello"}`},
			key:  "config.inline.payload",
		},
		{
			name: "invalid set",
			opts: InlineOptions{Type: "example_alpha", Sets: []string{"noequals"}},
			key:  "config.inline.set",
		},
		{
			name: "empty mode",
			opts: InlineOptions{},
			key:  "config.inline.mode",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.LookupEnv = lookupFrom(nil)
			_, err := BuildInline(tc.opts)
			assertProblemID(t, err, tc.key)
		})
	}
}

// TestBuildInline_SetOnlyNoDefaultBody 覆盖 --set body.* 时不再注入默认正文
//
// TestBuildInline_SetOnlyNoDefaultBody covers the default body staying out when --set body.* is
// present
func TestBuildInline_SetOnlyNoDefaultBody(t *testing.T) {
	channel, err := BuildInline(InlineOptions{
		Webhook:   "https://example.com/notify",
		Sets:      []string{"body.format=json", "body.json.text=hello"},
		LookupEnv: lookupFrom(nil),
	})
	if err != nil {
		t.Fatalf("BuildInline: %v", err)
	}
	settings := settingsOf(t, channel)
	body := decodeBody(t, settings)
	if body["format"] != "json" {
		t.Fatalf("body = %#v, want the explicit format", body)
	}
	if _, ok := settings["method"]; ok {
		t.Fatalf("settings = %#v, want no default method alongside an explicit body", settings)
	}
}

// TestBuildInline_TypeFromFlags 覆盖显式 --type 优先于 --webhook 推断出的 webhook 类型
//
// TestBuildInline_TypeFromFlags covers an explicit --type winning over the webhook type inferred
// from --webhook
func TestBuildInline_TypeFromFlags(t *testing.T) {
	channel, err := BuildInline(InlineOptions{
		Type:      " example_beta ",
		Webhook:   "https://example.com/notify",
		LookupEnv: lookupFrom(nil),
	})
	if err != nil {
		t.Fatalf("BuildInline: %v", err)
	}
	if channel.Type != "example_beta" {
		t.Fatalf("type = %q, want the trimmed explicit type", channel.Type)
	}
}

// TestBuildInline_EmptyWebhook 覆盖 --webhook 展开后为空时的用法错误
//
// TestBuildInline_EmptyWebhook covers a --webhook that expands to an empty value failing as a
// usage error
func TestBuildInline_EmptyWebhook(t *testing.T) {
	_, err := BuildInline(InlineOptions{Webhook: "${EMPTY}", LookupEnv: lookupFrom(map[string]string{"EMPTY": ""})})
	assertProblemID(t, err, "config.inline.webhook_url")
}

// TestBuildInline_InvalidPayload 覆盖非法 --payload 报 config.inline.payload
//
// TestBuildInline_InvalidPayload covers an invalid --payload reporting config.inline.payload
func TestBuildInline_InvalidPayload(t *testing.T) {
	_, err := BuildInline(InlineOptions{
		Webhook:   "https://example.com/notify",
		Payload:   `{"text":`,
		LookupEnv: lookupFrom(nil),
	})
	assertProblemID(t, err, "config.inline.payload")
}

// TestBuildInline_SettingsShape 覆盖返回的 settings 是 JSON 对象, 值保持字符串
//
// TestBuildInline_SettingsShape covers the returned settings being a JSON object with string
// values
func TestBuildInline_SettingsShape(t *testing.T) {
	channel, err := BuildInline(InlineOptions{
		Type:      "example_alpha",
		Sets:      []string{"count=3", "enabled=true"},
		LookupEnv: lookupFrom(nil),
	})
	if err != nil {
		t.Fatalf("BuildInline: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(channel.Settings, &decoded); err != nil {
		t.Fatalf("settings: %v", err)
	}
	if decoded["count"] != "3" || decoded["enabled"] != "true" {
		t.Fatalf("settings = %#v, want string values left to the factory", decoded)
	}
	if !strings.HasPrefix(string(channel.Settings), "{") {
		t.Fatalf("settings = %s, want a JSON object", channel.Settings)
	}
}
