package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// TestLoad_Defaults 覆盖 version/timeout/retryDelay 的缺省填充与显式值保留
//
// TestLoad_Defaults covers the default filling of version, timeout and retryDelay along with an
// explicit value being kept
func TestLoad_Defaults(t *testing.T) {
	root := loadJSON(t, `{"channels":[]}`)
	if root.Version != 1 {
		t.Fatalf("Version = %d, want 1", root.Version)
	}
	if root.Timeout.Duration() != 15*time.Second {
		t.Fatalf("Timeout = %v, want 15s", root.Timeout.Duration())
	}
	if root.RetryDelay.Duration() != time.Second {
		t.Fatalf("RetryDelay = %v, want 1s", root.RetryDelay.Duration())
	}
	if root.Retries != 0 || root.Proxy != "" {
		t.Fatalf("Retries/Proxy = %d/%q, want 0 and empty", root.Retries, root.Proxy)
	}

	root = loadJSON(t, `{"version":1,"timeout":"30s","retries":2,"retryDelay":"0s","proxy":"env","channels":[]}`)
	if root.Timeout.Duration() != 30*time.Second || root.Retries != 2 || root.RetryDelay.Duration() != 0 || root.Proxy != "env" {
		t.Fatalf("explicit values = %+v", root)
	}
}

// TestLoad_Version 覆盖 version 0 视为缺省, 非 1 的值报错
//
// TestLoad_Version covers version 0 counting as absent while another value fails
func TestLoad_Version(t *testing.T) {
	if root := loadJSON(t, `{"version":0,"channels":[]}`); root.Version != 1 {
		t.Fatalf("Version = %d, want the default 1", root.Version)
	}
	problem := loadProblem(t, `{"version":2,"channels":[]}`, "config.error.version")
	if problem.Data["Version"] != 2 {
		t.Fatalf("Version data = %#v", problem.Data["Version"])
	}
}

// TestLoad_UnknownFields 覆盖顶层与渠道公共字段的未知键
//
// TestLoad_UnknownFields covers unknown keys at the top level and in the channel common fields
func TestLoad_UnknownFields(t *testing.T) {
	cases := []struct {
		name string
		data string
		path string
	}{
		{
			name: "top level",
			data: `{"version":1,"channels":[],"timeouts":"15s"}`,
			path: "timeouts",
		},
		{
			name: "channel common",
			data: `{"version":1,"channels":[{"id":"example","type":"example_alpha","timeoutMilliseconds":5,"settings":{}}]}`,
			path: "channels[1]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problem := loadProblem(t, tc.data, "config.error.unknown_field")
			got, _ := problem.Data["Path"].(string)
			if !strings.Contains(got, tc.path) {
				t.Fatalf("Path = %q, want it to contain %q", got, tc.path)
			}
		})
	}
}

// TestLoad_TypeMismatch 覆盖顶层与渠道字段的基础类型错误
//
// TestLoad_TypeMismatch covers base type errors of the top-level and channel fields
func TestLoad_TypeMismatch(t *testing.T) {
	cases := []struct {
		name string
		data string
		key  string
	}{
		{name: "channels not array", data: `{"channels":{}}`, key: "config.error.type_mismatch"},
		{name: "id not string", data: `{"channels":[{"id":1,"type":"example_alpha","settings":{}}]}`, key: "config.error.type_mismatch"},
		{name: "enabled not bool", data: `{"channels":[{"id":"example","type":"example_alpha","enabled":1,"settings":{}}]}`, key: "config.error.bool"},
		{name: "timeout not string", data: `{"timeout":15000,"channels":[]}`, key: "config.error.duration"},
		{name: "retries not int", data: `{"retries":"many","channels":[]}`, key: "config.error.int"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loadProblem(t, tc.data, tc.key)
		})
	}
}

// TestLoad_IDValidation 覆盖 ID 的正则, 长度, 大小写与重复
//
// TestLoad_IDValidation covers the ID pattern, its length, case sensitivity and duplicates
func TestLoad_IDValidation(t *testing.T) {
	valid := []string{"a", "Example-1", "example_1.2", strings.Repeat("a", 64)}
	for _, id := range valid {
		data := `{"channels":[{"id":"` + id + `","type":"example_alpha","settings":{}}]}`
		root := loadJSON(t, data)
		if root.Channels[0].ID != id {
			t.Fatalf("ID = %q, want %q", root.Channels[0].ID, id)
		}
	}

	invalid := []string{"", "-example", ".example", "example space", strings.Repeat("a", 65)}
	for _, id := range invalid {
		data := `{"channels":[{"id":"` + id + `","type":"example_alpha","settings":{}}]}`
		loadProblem(t, data, "config.error.id_invalid")
	}

	loadProblem(t, `{"channels":[
		{"id":"example","type":"example_alpha","settings":{}},
		{"id":"example","type":"example_beta","settings":{}}
	]}`, "config.error.id_duplicate")
	root := loadJSON(t, `{"channels":[
		{"id":"Example","type":"example_alpha","settings":{}},
		{"id":"example","type":"example_beta","settings":{}}
	]}`)
	if len(root.Channels) != 2 {
		t.Fatalf("channels = %d, want two case-sensitive ids", len(root.Channels))
	}
}

// TestLoad_TypeUnknown 覆盖未注册渠道类型的报错与可用列表
//
// TestLoad_TypeUnknown covers an unregistered channel type failing with the available list
func TestLoad_TypeUnknown(t *testing.T) {
	problem := loadProblem(t, `{"channels":[{"id":"example","type":"example_missing","settings":{}}]}`, "config.error.type_unknown")
	if !strings.Contains(problem.Data["Available"].(string), fakeTypeAlpha) {
		t.Fatalf("Available = %#v, want the registered fake type", problem.Data["Available"])
	}
}

// TestLoad_EnabledDefaults 覆盖 enabled 缺省为 true, true/false 与字符串形式的解析
//
// TestLoad_EnabledDefaults covers enabled defaulting to true and the true, false and string forms
func TestLoad_EnabledDefaults(t *testing.T) {
	root := loadJSON(t, `{"channels":[
		{"id":"default","type":"example_alpha","settings":{}},
		{"id":"on","type":"example_alpha","enabled":true,"settings":{}},
		{"id":"off","type":"example_alpha","enabled":false},
		{"id":"string-off","type":"example_alpha","enabled":"0"}
	]}`)
	want := map[string]bool{"default": true, "on": true, "off": false, "string-off": false}
	for _, channel := range root.Channels {
		if got := channel.IsEnabled(); got != want[channel.ID] {
			t.Fatalf("%s IsEnabled() = %v, want %v", channel.ID, got, want[channel.ID])
		}
	}
}

// TestLoad_DisabledScalarsDefer 覆盖禁用渠道公共标量延迟解析: 未展开变量与坏字面量都不结束加载
//
// TestLoad_DisabledScalarsDefer covers the deferred parsing of a disabled channel's common
// scalars, where neither an unexpanded variable nor a broken literal ends the load
func TestLoad_DisabledScalarsDefer(t *testing.T) {
	// timeout 写未设置的变量: 加载成功, 渠道保持禁用, settings 原文保留
	// The timeout names an unset variable: the load succeeds, the channel stays disabled and the
	// settings keep their raw text
	root := loadJSON(t, `{"channels":[
		{"id":"off","type":"example_alpha","enabled":false,"timeout":"${EXAMPLE_MISSING_VAR}","settings":{"url":"https://example.com"}}
	]}`)
	if len(root.Deferred) != 1 {
		t.Fatalf("Deferred = %#v, want one entry", root.Deferred)
	}
	if root.Deferred[0].ID != "config.error.env_unset" {
		t.Fatalf("Deferred ID = %q", root.Deferred[0].ID)
	}
	if root.Deferred[0].Data["Path"] != "channels[1].timeout" {
		t.Fatalf("Deferred path = %#v, want channels[1].timeout", root.Deferred[0].Data["Path"])
	}
	if root.Channels[0].Timeout != nil {
		t.Fatalf("Timeout = %v, want nil so the top level stays in force", root.Channels[0].Timeout)
	}
	if !strings.Contains(string(root.Channels[0].Settings), "https://example.com") {
		t.Fatalf("settings = %s, want the raw text kept", root.Channels[0].Settings)
	}

	// 展开后值合法: 归一化写入展开结果, Deferred 为空
	// The value parses after expansion: the expanded form is written back and Deferred stays empty
	t.Setenv("EXAMPLE_SPAN", "7s")
	root = loadJSON(t, `{"channels":[
		{"id":"off","type":"example_alpha","enabled":false,"timeout":"${EXAMPLE_SPAN}","retries":"2","settings":{}}
	]}`)
	if len(root.Deferred) != 0 {
		t.Fatalf("Deferred = %#v, want it empty", root.Deferred)
	}
	if root.Channels[0].Timeout.Duration() != 7*time.Second {
		t.Fatalf("Timeout = %v, want 7s", root.Channels[0].Timeout.Duration())
	}
	if root.Channels[0].Retries == nil || *root.Channels[0].Retries != 2 {
		t.Fatalf("Retries = %v, want 2", root.Channels[0].Retries)
	}

	// 非法字面量: 加载成功, 解析问题进入 Deferred, 键被删掉
	// A broken literal: the load succeeds, the parse problem lands in Deferred and the key is
	// dropped
	root = loadJSON(t, `{"channels":[
		{"id":"off","type":"example_alpha","enabled":false,"timeout":"abc","settings":{}}
	]}`)
	if len(root.Deferred) != 1 {
		t.Fatalf("Deferred = %#v, want one entry", root.Deferred)
	}
	if root.Deferred[0].ID != "config.error.duration" {
		t.Fatalf("Deferred ID = %q", root.Deferred[0].ID)
	}
	if root.Deferred[0].Data["Path"] != "channels[1].timeout" {
		t.Fatalf("Deferred path = %#v, want channels[1].timeout", root.Deferred[0].Data["Path"])
	}
	if root.Channels[0].Timeout != nil {
		t.Fatalf("Timeout = %v, want nil so the top level stays in force", root.Channels[0].Timeout)
	}

	// 启用渠道保持既有严格性: 同样的写法立即失败
	// An enabled channel keeps the existing strictness: the same form fails immediately
	loadProblem(t, `{"channels":[{"id":"on","type":"example_alpha","timeout":"${EXAMPLE_MISSING_VAR}","settings":{}}]}`, "config.error.env_unset")
	loadProblem(t, `{"channels":[{"id":"on","type":"example_alpha","timeout":"abc","settings":{}}]}`, "config.error.duration")

	// 禁用渠道的 id, type 与 enabled 仍然严格: 身份与启用状态不属于延迟范围
	// A disabled channel still treats id, type and enabled strictly: identity and enabled state
	// are out of the deferral's scope
	loadProblem(t, `{"channels":[{"id":"off","type":"example_alpha","enabled":"maybe","settings":{}}]}`, "config.error.bool")
}

// TestLoad_DeferredCarriesChannelIdentity 覆盖延迟问题带上渠道 ID 与类型, 路径用 1 基下标
// settings 与公共标量两条延迟路径都要带上归属, 否则 check --all 的 ID 列只能显示配置路径
//
// TestLoad_DeferredCarriesChannelIdentity covers a deferred problem carrying its channel ID and
// type, with a one-based subscript in the path
// Both the settings path and the common-scalar path have to carry the owner, otherwise the ID
// column of check --all can only show the configuration path
func TestLoad_DeferredCarriesChannelIdentity(t *testing.T) {
	// 第二个渠道才出问题, 下标因此能区分 1 基与 0 基
	// The second channel is the one at fault, so the subscript tells one-based from zero-based
	data := `{"channels":[
		{"id":"on","type":"example_alpha","settings":{}},
		{"id":"off","type":"example_beta","enabled":false,"timeout":"${EXAMPLE_MISSING_VAR}","settings":{"url":"${EXAMPLE_MISSING_VAR}"}}
	]}`
	root := loadJSON(t, data)
	if len(root.Deferred) != 2 {
		t.Fatalf("Deferred = %#v, want two entries", root.Deferred)
	}
	wantPaths := map[string]bool{
		"channels[2].settings.url": false,
		"channels[2].timeout":      false,
	}
	for _, problem := range root.Deferred {
		path, _ := problem.Data["Path"].(string)
		if _, ok := wantPaths[path]; !ok {
			t.Fatalf("Deferred path = %q, want one of the two one-based paths", path)
		}
		wantPaths[path] = true
		if problem.Data["ChannelID"] != "off" {
			t.Fatalf("%s ChannelID = %#v, want off", path, problem.Data["ChannelID"])
		}
		if problem.Data["ChannelType"] != "example_beta" {
			t.Fatalf("%s ChannelType = %#v, want example_beta", path, problem.Data["ChannelType"])
		}
	}
	for path, seen := range wantPaths {
		if !seen {
			t.Fatalf("Deferred is missing the problem at %s: %#v", path, root.Deferred)
		}
	}

	// 默认值校验路径同样用 1 基下标
	// The default-value validation path uses a one-based subscript as well
	problem := loadProblem(t, `{"channels":[
		{"id":"on","type":"example_alpha","settings":{}},
		{"id":"bad","type":"example_alpha","timeout":"0s","settings":{}}
	]}`, "config.error.timeout")
	if problem.Data["Path"] != "channels[2].timeout" {
		t.Fatalf("Path = %#v, want channels[2].timeout", problem.Data["Path"])
	}
}

// TestLoad_SettingsShape 覆盖启用渠道的 settings 必填与对象形状, 禁用渠道可省略
//
// TestLoad_SettingsShape covers settings being required as an object for an enabled channel and
// optional for a disabled one
func TestLoad_SettingsShape(t *testing.T) {
	loadProblem(t, `{"channels":[{"id":"example","type":"example_alpha"}]}`, "config.error.settings_missing")
	for _, raw := range []string{`[]`, `"text"`, `1`, `null`, `true`} {
		data := `{"channels":[{"id":"example","type":"example_alpha","settings":` + raw + `}]}`
		loadProblem(t, data, "config.error.settings_not_object")
	}
	root := loadJSON(t, `{"channels":[{"id":"example","type":"example_alpha","enabled":false}]}`)
	if root.Channels[0].Settings != nil {
		t.Fatalf("disabled settings = %s, want it omitted", root.Channels[0].Settings)
	}
	empty := loadJSON(t, `{"channels":[{"id":"example","type":"example_alpha","settings":{}}]}`)
	if string(empty.Channels[0].Settings) != `{}` {
		t.Fatalf("empty settings = %s, want {}", empty.Channels[0].Settings)
	}
}

// TestLoad_SettingsRaw 覆盖 settings 原文按 JSON 语义保留, 加载不调用渠道工厂
//
// TestLoad_SettingsRaw covers settings staying as raw JSON and loading not calling a factory
func TestLoad_SettingsRaw(t *testing.T) {
	root := loadJSON(t, `{"channels":[{"id":"example","type":"example_alpha","settings":{"nested":{"value":1},"list":["a","b"],"unknown":true}}]}`)
	var decoded map[string]any
	if err := json.Unmarshal(root.Channels[0].Settings, &decoded); err != nil {
		t.Fatalf("settings: %v", err)
	}
	if decoded["unknown"] != true {
		t.Fatalf("settings = %#v, want the unknown key kept for the factory", decoded)
	}
}

// TestLoad_Durations 覆盖 duration 字符串语法与范围
//
// TestLoad_Durations covers the duration string syntax and its ranges
func TestLoad_Durations(t *testing.T) {
	root := loadJSON(t, `{"timeout":"1h30m","retryDelay":"0s","channels":[]}`)
	if root.Timeout.Duration() != 90*time.Minute || root.RetryDelay.Duration() != 0 {
		t.Fatalf("durations = %v/%v", root.Timeout.Duration(), root.RetryDelay.Duration())
	}

	loadProblem(t, `{"timeout":"0s","channels":[]}`, "config.error.timeout")
	loadProblem(t, `{"timeout":"-1s","channels":[]}`, "config.error.timeout")
	loadProblem(t, `{"retryDelay":"-1s","channels":[]}`, "config.error.retry_delay")
	loadProblem(t, `{"timeout":"15","channels":[]}`, "config.error.duration")
	loadProblem(t, `{"retries":-1,"channels":[]}`, "config.error.retries")
	loadProblem(t, `{"channels":[{"id":"example","type":"example_alpha","settings":{},"timeout":"0s"}]}`, "config.error.timeout")
	loadProblem(t, `{"channels":[{"id":"example","type":"example_alpha","settings":{},"retries":-1}]}`, "config.error.retries")
	loadProblem(t, `{"channels":[{"id":"example","type":"example_alpha","settings":{},"retryDelay":"-1s"}]}`, "config.error.retry_delay")
}

// TestLoad_Proxy 覆盖 proxy 三态与 socks5 拒绝
//
// TestLoad_Proxy covers the three proxy states and the socks5 rejection
func TestLoad_Proxy(t *testing.T) {
	for _, value := range []string{"", "env", "http://127.0.0.1:8080", "https://example.com:8443"} {
		root := loadJSON(t, `{"proxy":"`+value+`","channels":[]}`)
		if root.Proxy != value {
			t.Fatalf("Proxy = %q, want %q", root.Proxy, value)
		}
	}
	for _, value := range []string{"socks5://127.0.0.1:1080", "ftp://example.com", "example.com", "http://"} {
		problem := loadProblem(t, `{"proxy":"`+value+`","channels":[]}`, "config.error.proxy")
		if strings.Contains(problem.Data["Value"].(string), "socks5://") == false && strings.HasPrefix(value, "socks5://") {
			t.Fatalf("proxy data = %#v, want the offending value", problem.Data["Value"])
		}
	}
}

// TestLoad_BOMAndEmpty 覆盖 Load 自身的空内容与 BOM 处理
//
// TestLoad_BOMAndEmpty covers empty content and BOM handling inside Load itself
func TestLoad_BOMAndEmpty(t *testing.T) {
	loadProblem(t, "   \n", "config.error.empty")
	root, err := Load(Source{Kind: SourceFile, Path: "/example/taskpush.json", Data: []byte("\xEF\xBB\xBF{\"channels\":[]}")})
	if err != nil {
		t.Fatalf("Load with BOM: %v", err)
	}
	if root.Version != 1 {
		t.Fatalf("Version = %d, want 1", root.Version)
	}
}

// TestLoad_JSONErrors 覆盖 JSON 语法错误与顶层非对象
//
// TestLoad_JSONErrors covers a JSON syntax error and a non-object top level
func TestLoad_JSONErrors(t *testing.T) {
	loadProblem(t, `{"channels":`, "config.error.json")
	loadProblem(t, `[]`, "config.error.json")
	loadProblem(t, `null`, "config.error.json")
}

// TestLoad_ProblemAs 覆盖错误可以用 errors.As 取出 *apperr.Problem
//
// TestLoad_ProblemAs covers errors.As reaching the *apperr.Problem
func TestLoad_ProblemAs(t *testing.T) {
	_, err := Load(testSource(`{"version":9,"channels":[]}`))
	var problem *apperr.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	if problem.ID != "config.error.version" {
		t.Fatalf("ID = %q", problem.ID)
	}
	if problem.Error() == "" {
		t.Fatal("Problem.Error() is empty")
	}
}
