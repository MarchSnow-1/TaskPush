package config

import (
	"strings"
	"testing"
)

// TestExpandString_Basic 覆盖 ${VAR} 读取已设置变量
//
// TestExpandString_Basic covers ${VAR} reading a variable that is set
func TestExpandString_Basic(t *testing.T) {
	lookup := lookupFrom(map[string]string{"TOKEN": "example-token", "EMPTY": ""})
	got, err := expandString("file:/example/taskpush.json", "settings.url", "https://example.com/${TOKEN}", lookup)
	if err != nil {
		t.Fatalf("expandString: %v", err)
	}
	if got != "https://example.com/example-token" {
		t.Fatalf("expanded = %q", got)
	}
}

// TestExpandString_Default 覆盖 ${VAR:-默认值} 在未设置与为空时的两种回退
//
// TestExpandString_Default covers ${VAR:-default} falling back both when the variable is unset
// and when it is empty
func TestExpandString_Default(t *testing.T) {
	lookup := lookupFrom(map[string]string{"SET": "value", "EMPTY": ""})
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "set wins", in: "${SET:-fallback}", want: "value"},
		{name: "empty uses default", in: "${EMPTY:-fallback}", want: "fallback"},
		{name: "unset uses default", in: "${MISSING:-fallback}", want: "fallback"},
		{name: "empty default", in: "${MISSING:-}", want: ""},
		{name: "default keeps second separator", in: "${MISSING:-a:-b}", want: "a:-b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandString("stdin", "path", tc.in, lookup)
			if err != nil {
				t.Fatalf("expandString: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expanded = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExpandString_Escape 覆盖 $${ 转义为字面 ${, 以及转义后的内容不再展开
//
// TestExpandString_Escape covers $${ escaping to a literal ${ and the escaped text staying
// unexpanded
func TestExpandString_Escape(t *testing.T) {
	lookup := lookupFrom(map[string]string{"VAR": "value"})
	got, err := expandString("stdin", "path", "$${VAR}", lookup)
	if err != nil {
		t.Fatalf("expandString: %v", err)
	}
	if got != "${VAR}" {
		t.Fatalf("expanded = %q, want the literal ${VAR}", got)
	}

	got, err = expandString("stdin", "path", "$${MISSING}", lookup)
	if err != nil {
		t.Fatalf("expandString escaped unset: %v", err)
	}
	if got != "${MISSING}" {
		t.Fatalf("expanded = %q, want the literal ${MISSING}", got)
	}
}

// TestExpandString_NoSecondPass 覆盖展开结果里的 ${} 不参与第二次展开
//
// TestExpandString_NoSecondPass covers ${} inside an expanded value staying unexpanded
func TestExpandString_NoSecondPass(t *testing.T) {
	lookup := lookupFrom(map[string]string{"OUTER": "${INNER}", "INNER": "value"})
	got, err := expandString("stdin", "path", "${OUTER}", lookup)
	if err != nil {
		t.Fatalf("expandString: %v", err)
	}
	if got != "${INNER}" {
		t.Fatalf("expanded = %q, want the single-pass result", got)
	}
}

// TestExpandString_Errors 覆盖未设置, 非法变量名, 未闭合与嵌套默认值的错误键
//
// TestExpandString_Errors covers the error keys for an unset variable, an invalid name, an
// unclosed brace and a nested default
func TestExpandString_Errors(t *testing.T) {
	lookup := lookupFrom(nil)
	cases := []struct {
		name string
		in   string
		key  string
	}{
		{name: "unset", in: "${MISSING}", key: "config.error.env_unset"},
		{name: "digit first", in: "${1BAD}", key: "config.error.env_name"},
		{name: "hyphen in name", in: "${BAD-NAME}", key: "config.error.env_name"},
		{name: "empty name", in: "${}", key: "config.error.env_name"},
		{name: "unclosed", in: "${MISSING", key: "config.error.env_unclosed"},
		{name: "nested default", in: "${MISSING:-${OTHER}}", key: "config.error.env_nested"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := expandString("file:/example/taskpush.json", "channels[1].settings.url", tc.in, lookup)
			assertProblemID(t, err, tc.key)
		})
	}

	problem := loadProblem(t, `{"channels":[{"id":"example","type":"example_alpha","settings":{"url":"${EXAMPLE_MISSING_VAR}"}}]}`, "config.error.env_unset")
	if problem.Data["Path"] != "channels[1].settings.url" {
		t.Fatalf("Path = %#v, want channels[1].settings.url", problem.Data["Path"])
	}
	if problem.Data["Variable"] != "EXAMPLE_MISSING_VAR" {
		t.Fatalf("Variable = %#v", problem.Data["Variable"])
	}
	if problem.Data["Source"] != "file:/example/taskpush.json" {
		t.Fatalf("Source = %#v", problem.Data["Source"])
	}
}

// TestExpand_NonStrings 覆盖数字, 布尔与 null 不参与展开, 以及对象键名不展开
//
// TestExpand_NonStrings covers numbers, booleans and null staying as they are and object keys
// staying unexpanded
func TestExpand_NonStrings(t *testing.T) {
	data := `{
		"version": 1,
		"channels": [{
			"id": "example",
			"type": "example_alpha",
			"settings": {
				"number": 42,
				"ratio": 1.5,
				"flag": true,
				"nothing": null,
				"${KEY}": "value"
			}
		}]
	}`
	root := loadJSON(t, data)
	var settings map[string]any
	if err := jsonUnmarshal(root.Channels[0].Settings, &settings); err != nil {
		t.Fatalf("settings: %v", err)
	}
	if settings["number"] != float64(42) || settings["ratio"] != 1.5 || settings["flag"] != true {
		t.Fatalf("settings = %#v", settings)
	}
	if _, ok := settings["${KEY}"]; !ok {
		t.Fatalf("settings = %#v, want the object key left unexpanded", settings)
	}
}

// TestExpand_NestedContainers 覆盖数组与嵌套对象里的字符串展开及路径编号
//
// TestExpand_NestedContainers covers string expansion inside arrays and nested objects along
// with the path indices
func TestExpand_NestedContainers(t *testing.T) {
	t.Setenv("EXAMPLE_HOST", "example.com")
	t.Setenv("EXAMPLE_TOKENS", "one")
	data := `{"channels":[{"id":"example","type":"example_alpha","settings":{
		"url":"https://${EXAMPLE_HOST}/notify",
		"list":["${EXAMPLE_TOKENS}","plain",{"deep":"${EXAMPLE_HOST}"}],
		"nested":{"value":"${EXAMPLE_HOST}"}
	}}]}`
	root := loadJSON(t, data)
	var settings map[string]any
	if err := jsonUnmarshal(root.Channels[0].Settings, &settings); err != nil {
		t.Fatalf("settings: %v", err)
	}
	if settings["url"] != "https://example.com/notify" {
		t.Fatalf("url = %#v", settings["url"])
	}
	list := settings["list"].([]any)
	if list[0] != "one" || list[1] != "plain" {
		t.Fatalf("list = %#v", list)
	}
	if list[2].(map[string]any)["deep"] != "example.com" {
		t.Fatalf("nested list object = %#v", list[2])
	}
	if settings["nested"].(map[string]any)["value"] != "example.com" {
		t.Fatalf("nested = %#v", settings["nested"])
	}
}

// TestExpand_DisabledDeferred 覆盖禁用渠道展开失败进入 Deferred 且原文保留
//
// TestExpand_DisabledDeferred covers a failed expansion of a disabled channel landing in
// Deferred while its raw text is kept
func TestExpand_DisabledDeferred(t *testing.T) {
	root := loadJSON(t, `{"channels":[{"id":"off","type":"example_alpha","enabled":false,"settings":{"url":"${EXAMPLE_MISSING_VAR}"}}]}`)
	if len(root.Deferred) != 1 {
		t.Fatalf("Deferred = %#v, want one entry", root.Deferred)
	}
	if root.Deferred[0].ID != "config.error.env_unset" {
		t.Fatalf("Deferred ID = %q", root.Deferred[0].ID)
	}
	if root.Deferred[0].Data["Path"] != "channels[1].settings.url" {
		t.Fatalf("Deferred path = %#v", root.Deferred[0].Data["Path"])
	}
	if !strings.Contains(string(root.Channels[0].Settings), "${EXAMPLE_MISSING_VAR}") {
		t.Fatalf("settings = %s, want the unexpanded original kept", root.Channels[0].Settings)
	}
}

// TestExpand_DisabledDeferredSkipped 覆盖启用渠道展开失败直接报错, 不会进入 Deferred
//
// TestExpand_DisabledDeferredSkipped covers an enabled channel's failed expansion returning an
// error instead of a Deferred entry
func TestExpand_DisabledDeferredSkipped(t *testing.T) {
	loadProblem(t, `{"channels":[{"id":"on","type":"example_alpha","settings":{"url":"${EXAMPLE_MISSING_VAR}"}}]}`, "config.error.env_unset")
}

// TestExpand_DeferredDisabledOnly 覆盖禁用渠道展开成功时 Deferred 为空
//
// TestExpand_DeferredDisabledOnly covers Deferred staying empty when a disabled channel expands
// successfully
func TestExpand_DeferredDisabledOnly(t *testing.T) {
	t.Setenv("EXAMPLE_URL", "https://example.com")
	root := loadJSON(t, `{"channels":[{"id":"off","type":"example_alpha","enabled":false,"settings":{"url":"${EXAMPLE_URL}"}}]}`)
	if len(root.Deferred) != 0 {
		t.Fatalf("Deferred = %#v, want it empty", root.Deferred)
	}
}

// TestExpand_TopLevelAlways 覆盖顶层字段无论渠道禁用与否都会展开
//
// TestExpand_TopLevelAlways covers top-level fields being expanded regardless of a disabled
// channel
func TestExpand_TopLevelAlways(t *testing.T) {
	loadProblem(t, `{"proxy":"${EXAMPLE_MISSING_VAR}","channels":[{"id":"off","type":"example_alpha","enabled":false}]}`, "config.error.env_unset")
}

// TestExpand_EnvNameErrors 覆盖展开错误的路径精确到数组下标与嵌套字段
//
// TestExpand_EnvNameErrors covers an expansion error path precise down to an array index and a
// nested field
func TestExpand_EnvNameErrors(t *testing.T) {
	data := `{"channels":[{"id":"on","type":"example_alpha","settings":{"headers":{"X-Token":"${EXAMPLE_MISSING_VAR}"}}}]}`
	problem := loadProblem(t, data, "config.error.env_unset")
	if problem.Data["Path"] != "channels[1].settings.headers.X-Token" {
		t.Fatalf("Path = %#v, want channels[1].settings.headers.X-Token", problem.Data["Path"])
	}
}
